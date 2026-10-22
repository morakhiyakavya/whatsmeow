package main

import (
	"context"
	"log"
	"strings"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types/events"
)

const recoveryPrefix = "VENWEB: approve replacing my POS sessions "

// Only live, unedited self-chat messages from the primary phone prove ownership.
func recoveryMessageHash(device *store.Device, event *events.Message) ([]byte, bool) {
	if event == nil || !event.Info.IsFromMe || event.Info.IsGroup || event.Info.Sender.Device != 0 ||
		event.IsEdit || event.Info.Edit != "" || event.IsBotInvoke || event.IsViewOnce || event.IsViewOnceV2 || event.IsViewOnceV2Extension ||
		event.SourceWebMsg != nil || event.UnavailableRequestID != "" {
		return nil, false
	}
	own, lid := device.GetJID().ToNonAD(), device.GetLID().ToNonAD()
	sender, chat := event.Info.Sender.ToNonAD(), event.Info.Chat.ToNonAD()
	if own.IsEmpty() || (sender != own && (lid.IsEmpty() || sender != lid)) ||
		(chat != own && (lid.IsEmpty() || chat != lid)) || event.Info.ID == "" {
		return nil, false
	}
	text := event.Message.GetConversation()
	if text == "" {
		extended := event.Message.GetExtendedTextMessage()
		// Forwarded/quoted instructions are not an explicit recovery approval.
		info := extended.GetContextInfo()
		if info.GetIsForwarded() || info.GetForwardingScore() != 0 || info.GetQuotedMessage() != nil || info.GetStanzaID() != "" {
			return nil, false
		}
		text = extended.GetText()
	}
	token, ok := strings.CutPrefix(text, recoveryPrefix)
	if !ok {
		return nil, false
	}
	return secretHash(token)
}

func (s *whatsAppService) approveRecovery(ctx context.Context, device *store.Device, user actor, account, binding string, event *events.Message) (resultErr error) {
	span := startOperation(ctx, "whatsapp.lifecycle", "whatsapp.recovery")
	span.SetTag("worker.key", performanceKey(user.BusinessID+"/"+account))
	defer func() { finishOperation(span, resultErr) }()
	ctx = span.Context()
	hash, ok := recoveryMessageHash(device, event)
	if !ok {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the live binding before attempts/sessions, as in exchange and unlink.
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT auth_generation FROM ipo_pos.whatsapp_accounts
		WHERE business_id=$1 AND id=$2 AND linked_by_user_id=$3 AND device_jid=$4 AND unlinked_at IS NULL
		FOR SHARE`, user.BusinessID, account, user.UserID, binding).Scan(&generation)
	if err != nil {
		return err
	}
	if err = lockSessionLink(ctx, tx, user.UserID, user.BusinessID, account, generation); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE ipo_pos.login_attempts a SET state='verified'
		WHERE recovery_hash=$1 AND user_id=$2 AND business_id=$3 AND whatsapp_account_id=$4
		AND auth_generation=$5 AND phone_number=$6 AND state='pending' AND consumed_at IS NULL
		AND expires_at>clock_timestamp() AND $7::timestamptz>=date_trunc('second',created_at)
		AND $7::timestamptz<=clock_timestamp()+interval '5 seconds'`, hash, user.UserID, user.BusinessID,
		account, generation, "+"+device.GetJID().User, event.Info.Timestamp)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Reasons remain server-side; public authentication failures stay generic.
func (a *api) recordRefreshRejection(ctx context.Context, hash []byte) {
	_, err := a.db.ExecContext(ctx, `INSERT INTO ipo_pos.auth_events(user_id,session_id,reason,release)
		SELECT s.user_id,s.id,CASE
		WHEN s.id IS NULL THEN 'unknown_refresh'
		WHEN u.disabled_at IS NOT NULL THEN 'user_disabled'
		WHEN s.refresh_expires_at<=now() THEN 'refresh_expired'
		WHEN wa.id IS NULL OR wa.device_jid IS NULL OR wa.unlinked_at IS NOT NULL
		  OR wa.auth_generation<>s.auth_generation OR wa.linked_by_user_id<>s.user_id THEN 'link_changed'
		WHEN m.user_id IS NULL OR m.disabled_at IS NOT NULL THEN 'membership_disabled'
		WHEN b.archived_at IS NOT NULL THEN 'business_archived'
		ELSE 'unknown_refresh' END,$2
		FROM (SELECT 1) seed LEFT JOIN ipo_pos.login_sessions s ON s.refresh_hash=$1
		LEFT JOIN ipo_pos.users u ON u.id=s.user_id
		LEFT JOIN ipo_pos.whatsapp_accounts wa ON wa.business_id=s.business_id AND wa.id=s.whatsapp_account_id
		LEFT JOIN ipo_pos.business_memberships m ON m.business_id=s.business_id AND m.user_id=s.user_id
		LEFT JOIN ipo_pos.businesses b ON b.id=s.business_id`, hash, a.release)
	if err != nil {
		// Never log the query, driver error or supplied credential.
		log.Print("authentication diagnostic write failed")
	}
}

func (a *api) deleteSessions(ctx context.Context, reason, query string, args ...any) error {
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('venweb.auth_reason',$1,true),set_config('venweb.auth_release',$2,true)`, reason, a.release); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}
