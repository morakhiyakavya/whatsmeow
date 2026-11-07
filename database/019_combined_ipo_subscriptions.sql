BEGIN;

ALTER TABLE ipo_pos.ipo_subscriptions DROP CONSTRAINT ipo_subscriptions_coverage_check;
ALTER TABLE ipo_pos.ipo_subscriptions ADD CONSTRAINT ipo_subscriptions_coverage_check
    CHECK (coverage IN ('NSE only','NSE+BSE'));
ALTER TABLE ipo_pos.ipo_subscriptions ADD COLUMN source_observed_at timestamptz;

COMMENT ON COLUMN ipo_pos.ipo_subscriptions.coverage IS
    'NSE publishes mainboard NSE+BSE figures in activeCat. Existing NSE-only observations retain their scope until replaced by a consolidated observation.';
COMMENT ON COLUMN ipo_pos.ipo_subscriptions.source_observed_at IS
    'Upstream publication time when available. observed_at remains the local observation time.';

COMMIT;
