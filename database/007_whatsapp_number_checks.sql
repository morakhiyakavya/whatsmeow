BEGIN;
CREATE TABLE ipo_pos.whatsapp_number_checks (
    business_id uuid NOT NULL,
    phone_number text NOT NULL CHECK (phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    registered boolean NOT NULL,
    checked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    whatsapp_account_id uuid NOT NULL,
    PRIMARY KEY (business_id,phone_number),
    FOREIGN KEY (business_id,whatsapp_account_id) REFERENCES ipo_pos.whatsapp_accounts(business_id,id)
);
COMMENT ON TABLE ipo_pos.whatsapp_number_checks IS
    'Latest successful registration check per business and number; an absent row means unknown. Only the trusted WhatsApp worker writes results.';
COMMIT;
