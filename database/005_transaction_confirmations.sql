BEGIN;
CREATE TABLE ipo_pos.transaction_confirmations (
    business_id uuid NOT NULL,
    transaction_id uuid NOT NULL,
    transaction_version integer NOT NULL,
    whatsapp_account_id uuid NOT NULL,
    created_by_user_id uuid NOT NULL,
    sender_device_jid text NOT NULL,
    recipient_phone_number text NOT NULL CHECK (recipient_phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    body text NOT NULL CHECK (btrim(body) <> ''),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sending','sent','uncertain','failed','cancelled')),
    wa_message_id text CHECK (btrim(wa_message_id) <> ''),
    error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    attempted_at timestamptz,
    sent_at timestamptz,
    PRIMARY KEY (business_id,transaction_id),
    UNIQUE (business_id,whatsapp_account_id,wa_message_id),
    FOREIGN KEY (business_id,transaction_id,transaction_version)
        REFERENCES ipo_pos.transaction_versions(business_id,id,version),
    FOREIGN KEY (business_id,whatsapp_account_id) REFERENCES ipo_pos.whatsapp_accounts(business_id,id),
    FOREIGN KEY (business_id,created_by_user_id) REFERENCES ipo_pos.business_memberships(business_id,user_id),
    CHECK (status NOT IN ('sending','sent','uncertain') OR (wa_message_id IS NOT NULL AND attempted_at IS NOT NULL)),
    CHECK ((status = 'sent') = (sent_at IS NOT NULL))
);
CREATE INDEX transaction_confirmations_pending_idx ON ipo_pos.transaction_confirmations
    (business_id,whatsapp_account_id,created_at,transaction_id) WHERE status IN ('pending','sending');
COMMIT;
