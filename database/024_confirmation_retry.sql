BEGIN;

CREATE FUNCTION ipo_pos.retry_transaction_confirmation(p_business uuid, p_transaction uuid, p_version integer, p_user uuid)
RETURNS jsonb LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
    receipt ipo_pos.transaction_confirmations;
BEGIN
    UPDATE ipo_pos.transaction_confirmations q
    SET status='pending', error_code=NULL
    FROM ipo_pos.transactions t
    JOIN ipo_pos.customers c ON c.business_id=t.business_id AND c.id=t.customer_id
    JOIN ipo_pos.businesses b ON b.id=t.business_id
    JOIN ipo_pos.business_memberships m ON m.business_id=t.business_id AND m.user_id=p_user
    JOIN ipo_pos.users u ON u.id=m.user_id,
         ipo_pos.whatsapp_accounts wa
    WHERE q.business_id=p_business AND q.transaction_id=p_transaction AND q.transaction_version=p_version
      AND q.created_by_user_id=p_user AND q.status='failed' AND q.attempted_at IS NULL AND q.wa_message_id IS NULL
      AND q.error_code IN ('sender_changed','recipient_changed')
      AND t.business_id=q.business_id AND t.id=q.transaction_id AND t.version=q.transaction_version AND t.status='confirmed'
      AND c.archived_at IS NULL AND c.phone_number=q.recipient_phone_number
      AND wa.business_id=q.business_id AND wa.id=q.whatsapp_account_id AND wa.linked_by_user_id=p_user
      AND wa.device_jid=q.sender_device_jid AND wa.unlinked_at IS NULL
      AND m.disabled_at IS NULL AND u.disabled_at IS NULL AND b.archived_at IS NULL
    RETURNING q.* INTO receipt;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;
    PERFORM pg_notify('ipo_worker_wake', receipt.business_id::text||'/'||receipt.whatsapp_account_id::text);
    RETURN to_jsonb(receipt)-'business_id'-'sender_device_jid';
END $$;
REVOKE ALL ON FUNCTION ipo_pos.retry_transaction_confirmation(uuid,uuid,integer,uuid) FROM PUBLIC;

COMMIT;
