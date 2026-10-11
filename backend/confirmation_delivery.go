package main

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type confirmationSender interface {
	GenerateMessageID() types.MessageID
	SendMessage(context.Context, types.JID, *waE2E.Message, ...whatsmeow.SendRequestExtra) (whatsmeow.SendResponse, error)
}

// Called only after obtaining the account's cross-process session lock.
func recoverConfirmations(ctx context.Context, conn *sql.Conn, business, account string) error {
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	_, err := conn.ExecContext(ctx, `UPDATE ipo_pos.transaction_confirmations
        SET status='uncertain',error_code='interrupted_send'
        WHERE business_id=$1 AND whatsapp_account_id=$2 AND status='sending'`, business, account)
	return err
}

func deliverConfirmation(ctx context.Context, conn *sql.Conn, user actor, account, jid string, client confirmationSender) error {
	readyCtx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	tx, err := conn.BeginTx(readyCtx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current sql.NullString
	if err = tx.QueryRowContext(readyCtx, activeWhatsAppAccount, user.BusinessID, account, user.UserID).Scan(&current); err != nil {
		return err
	}
	if !current.Valid || current.String != jid {
		return sql.ErrNoRows
	}
	var id, phone, body, sender, creator, status string
	var version, receiptVersion int
	var recipientCurrent bool
	err = tx.QueryRowContext(readyCtx, `SELECT q.transaction_id,q.recipient_phone_number,q.body,q.sender_device_jid,
        q.created_by_user_id,t.status,t.version,q.transaction_version,
        COALESCE(c.archived_at IS NULL AND c.phone_number=q.recipient_phone_number,false)
        FROM ipo_pos.transaction_confirmations q
        JOIN ipo_pos.transactions t ON t.business_id=q.business_id AND t.id=q.transaction_id
        JOIN ipo_pos.customers c ON c.business_id=t.business_id AND c.id=t.customer_id
        WHERE q.business_id=$1 AND q.whatsapp_account_id=$2 AND q.status='pending'
        ORDER BY q.created_at,q.transaction_id LIMIT 1 FOR UPDATE OF q SKIP LOCKED`, user.BusinessID, account).
		Scan(&id, &phone, &body, &sender, &creator, &status, &version, &receiptVersion, &recipientCurrent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	terminal, code := "failed", ""
	switch {
	case creator != user.UserID || sender != jid:
		code = "sender_changed"
	case status == "cancelled":
		terminal, code = "cancelled", "transaction_cancelled"
	case version != receiptVersion:
		code = "transaction_changed"
	case !recipientCurrent:
		code = "recipient_changed"
	}
	if code != "" {
		if _, err = tx.ExecContext(readyCtx, `UPDATE ipo_pos.transaction_confirmations SET status=$3,error_code=$4
            WHERE business_id=$1 AND transaction_id=$2`, user.BusinessID, id, terminal, code); err != nil {
			return err
		}
		return tx.Commit()
	}
	messageID := client.GenerateMessageID()
	if _, err = tx.ExecContext(readyCtx, `UPDATE ipo_pos.transaction_confirmations
        SET status='sending',wa_message_id=$3,attempted_at=now()
        WHERE business_id=$1 AND transaction_id=$2`, user.BusinessID, id, messageID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	// shortcut: one send attempt; uncertain outcomes need review before adding a safe retry workflow.
	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	response, sendErr := client.SendMessage(sendCtx, types.NewJID(strings.TrimPrefix(phone, "+"), types.DefaultUserServer),
		&waE2E.Message{Conversation: proto.String(body)}, whatsmeow.SendRequestExtra{ID: messageID, Timeout: 20 * time.Second})
	cancel()
	outcome := "sent"
	var failure any
	if sendErr != nil || response.ID != messageID {
		outcome, failure = "uncertain", "send_unconfirmed"
	}
	finishCtx, finish := context.WithTimeout(ctx, 5*time.Second)
	defer finish()
	_, err = conn.ExecContext(finishCtx, `UPDATE ipo_pos.transaction_confirmations
        SET status=$3,error_code=$4,sent_at=CASE WHEN $3='sent' THEN now() END
        WHERE business_id=$1 AND transaction_id=$2 AND status='sending'`, user.BusinessID, id, outcome, failure)
	return err
}
