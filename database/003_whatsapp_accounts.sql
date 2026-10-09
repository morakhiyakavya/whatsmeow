BEGIN;
CREATE INDEX whatsapp_accounts_created_idx
    ON ipo_pos.whatsapp_accounts (business_id, created_at DESC, id DESC);
COMMIT;
