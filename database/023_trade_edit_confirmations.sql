BEGIN;
ALTER TABLE ipo_pos.transaction_confirmations
    DROP CONSTRAINT transaction_confirmations_pkey,
    ADD CONSTRAINT transaction_confirmations_version_pkey PRIMARY KEY (business_id,transaction_id,transaction_version);
COMMIT;
