BEGIN;
SET LOCAL lock_timeout = '10s';
ALTER TABLE ipo_pos.transactions
    DROP CONSTRAINT transactions_quantity_check,
    ADD CONSTRAINT transactions_nonzero_quantity_check
        CHECK (quantity <> 0 AND quantity <> 'NaN'::numeric);
ALTER TABLE ipo_pos.transaction_versions
    DROP CONSTRAINT transactions_quantity_check,
    ADD CONSTRAINT transaction_versions_nonzero_quantity_check
        CHECK (quantity <> 0 AND quantity <> 'NaN'::numeric);
COMMENT ON COLUMN ipo_pos.transactions.quantity IS
    'Signed form quantity from the business perspective: positive means buy, negative means sell. Zero is invalid.';
COMMENT ON COLUMN ipo_pos.transaction_versions.quantity IS
    'Immutable signed quantity snapshot: positive means buy, negative means sell.';
COMMIT;
