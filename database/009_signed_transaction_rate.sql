BEGIN;
SET LOCAL lock_timeout = '10s';
ALTER TABLE ipo_pos.transactions
    DROP CONSTRAINT transactions_rate_check,
    ADD CONSTRAINT transactions_signed_rate_check CHECK (rate <> 'NaN'::numeric);
ALTER TABLE ipo_pos.transaction_versions
    DROP CONSTRAINT transactions_rate_check,
    ADD CONSTRAINT transaction_versions_signed_rate_check CHECK (rate <> 'NaN'::numeric);
COMMENT ON COLUMN ipo_pos.transactions.rate IS
    'Signed rate per form; positive, negative and zero are valid. Direction is determined only by quantity.';
COMMENT ON COLUMN ipo_pos.transaction_versions.rate IS
    'Immutable signed rate snapshot; direction is determined only by quantity.';
COMMIT;
