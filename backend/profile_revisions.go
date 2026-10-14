package main

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// A revision and its outbox entries become visible in the same commit.
func saveProfileObservation(ctx context.Context, db *sql.DB, user actor, account, binding, phone, state, pictureID string, eventAt time.Time) (string, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var generation int64
	err = tx.QueryRowContext(ctx, `SELECT wa.auth_generation FROM ipo_pos.whatsapp_accounts wa
		JOIN ipo_pos.users u ON u.id=wa.linked_by_user_id
		JOIN ipo_pos.business_memberships m ON m.business_id=wa.business_id AND m.user_id=u.id
		JOIN ipo_pos.businesses b ON b.id=m.business_id
		WHERE wa.business_id=$1 AND wa.id=$2 AND wa.linked_by_user_id=$3 AND wa.device_jid=$4
		AND wa.unlinked_at IS NULL AND u.disabled_at IS NULL AND m.disabled_at IS NULL AND b.archived_at IS NULL
		FOR SHARE OF wa,u,m,b`, user.BusinessID, account, user.UserID, binding).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errProfileUnavailable
	}
	if err != nil {
		return "", err
	}
	key := strings.ToLower(user.BusinessID+"/"+account) + "/" + phone
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "profile/"+key); err != nil {
		return "", err
	}
	var revision int64
	var previousState, previousID string
	var previousEvent sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT profile_revision,picture_state,picture_id,event_at
		FROM ipo_pos.contact_profile_revisions WHERE business_id=$1 AND whatsapp_account_id=$2 AND phone_number=$3
		FOR UPDATE`, user.BusinessID, account, phone).Scan(&revision, &previousState, &previousID, &previousEvent)
	initial := errors.Is(err, sql.ErrNoRows)
	if err != nil && !initial {
		return "", err
	}
	if !eventAt.IsZero() && previousEvent.Valid && (eventAt.Before(previousEvent.Time) ||
		(eventAt.Equal(previousEvent.Time) && state == previousState && pictureID == previousID)) {
		return strconv.FormatInt(revision, 10), tx.Commit()
	}
	changed := previousState != state || previousID != pictureID
	if state == "changed" && previousState == "available" && pictureID != "" && previousID == pictureID {
		changed = false
	}
	// Fetching the image announced by an event resolves its state without another invalidation.
	if previousState == "changed" && state == "available" && previousID != "" && previousID == pictureID {
		changed = false
	}
	if state == "changed" && pictureID == "" {
		changed = true
	}
	if initial {
		revision = 1
	} else if changed {
		revision++
	}
	var eventArg any
	if !eventAt.IsZero() {
		eventArg = eventAt
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.contact_profile_revisions
		(business_id,whatsapp_account_id,phone_number,profile_revision,picture_state,picture_id,event_at)
		VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(business_id,whatsapp_account_id,phone_number)
		DO UPDATE SET profile_revision=EXCLUDED.profile_revision,picture_state=EXCLUDED.picture_state,
		picture_id=EXCLUDED.picture_id,event_at=COALESCE(EXCLUDED.event_at,ipo_pos.contact_profile_revisions.event_at),
		updated_at=clock_timestamp()`, user.BusinessID, account, phone, revision, state, pictureID, eventArg)
	if err == nil && (changed && !initial || !eventAt.IsZero()) {
		_, err = tx.ExecContext(ctx, `INSERT INTO ipo_pos.profile_push_outbox AS q
			(installation_id,device_generation,user_id,business_id,whatsapp_account_id,auth_generation,phone_number,profile_revision)
			SELECT p.installation_id,p.generation,p.user_id,$1,$2,$3,$4,$5
			FROM ipo_pos.push_devices p JOIN ipo_pos.login_sessions s ON s.id=p.session_id
			WHERE p.user_id=$6 AND COALESCE(s.refresh_expires_at,s.expires_at)>now()
			ON CONFLICT(installation_id,business_id,whatsapp_account_id,phone_number)
			DO UPDATE SET device_generation=EXCLUDED.device_generation,user_id=EXCLUDED.user_id,
			auth_generation=EXCLUDED.auth_generation,profile_revision=EXCLUDED.profile_revision,
			attempts=0,next_attempt_at=clock_timestamp(),expires_at=clock_timestamp()+interval '24 hours'
			WHERE q.profile_revision<EXCLUDED.profile_revision OR q.device_generation<>EXCLUDED.device_generation`,
			user.BusinessID, account, generation, phone, revision, user.UserID)
	}
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(revision, 10), tx.Commit()
}

func pictureEventPhone(ctx context.Context, device *store.Device, jid types.JID) (string, error) {
	jid = jid.ToNonAD()
	if jid.Server == types.HiddenUserServer {
		var err error
		jid, err = device.LIDs.GetPNForLID(ctx, jid)
		if err != nil {
			return "", err
		}
	}
	if jid.Server != types.DefaultUserServer || !phonePattern.MatchString("+"+jid.User) {
		return "", nil
	}
	return "+" + jid.User, nil
}

func (s *whatsAppService) pictureChanged(ctx context.Context, device *store.Device, user actor, account, binding string, event *events.Picture) error {
	phone, err := pictureEventPhone(ctx, device, event.JID)
	if err != nil || phone == "" {
		return err
	}
	state, pictureID := "changed", event.PictureID
	if event.Remove {
		state, pictureID = "not_set", ""
	}
	eventAt := event.Timestamp
	if eventAt.IsZero() {
		eventAt = time.Now().UTC()
	}
	_, err = saveProfileObservation(ctx, s.db, user, account, binding, phone, state, pictureID, eventAt)
	return err
}
