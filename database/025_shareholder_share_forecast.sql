BEGIN;

ALTER TABLE ipo_pos.ipo_dashboard_assumption_versions
    ADD COLUMN shareholder_expected_bid_shares numeric,
    ADD COLUMN shareholder_shares_per_bid numeric,
    ADD CONSTRAINT shareholder_share_forecast_check CHECK (
        (shareholder_expected_bid_shares IS NULL AND shareholder_shares_per_bid IS NULL)
        OR (
            shareholder_expected_bid_shares IS NOT NULL AND shareholder_shares_per_bid IS NOT NULL
            AND shareholder_shares_per_bid > 0
            AND shareholder_shares_per_bid <= shareholder_expected_bid_shares
            AND shareholder_expected_bid_shares < 1e24
            AND shareholder_expected_bid_shares = trunc(shareholder_expected_bid_shares)
            AND shareholder_shares_per_bid = trunc(shareholder_shares_per_bid)
        )
    );

COMMIT;
