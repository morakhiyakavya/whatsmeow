BEGIN;

CREATE FUNCTION ipo_pos.valid_bid_forecasts(forecasts jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE item record;
BEGIN
    IF jsonb_typeof(forecasts) IS DISTINCT FROM 'object' OR octet_length(forecasts::text)>1024 THEN RETURN false; END IF;
    FOR item IN SELECT * FROM jsonb_each(forecasts) LOOP
        IF item.key NOT IN ('RETAIL','SHNI','BHNI') OR jsonb_typeof(item.value) IS DISTINCT FROM 'object' THEN
            RETURN false;
        END IF;
        IF item.value='{}'::jsonb OR item.value - ARRAY['expected_bid_shares','bid_lots'] <> '{}'::jsonb THEN RETURN false; END IF;
        IF item.value ? 'expected_bid_shares' AND (
            jsonb_typeof(item.value->'expected_bid_shares') IS DISTINCT FROM 'string'
            OR (item.value->>'expected_bid_shares') !~ '^(0|[1-9][0-9]{0,23})$'
        ) THEN RETURN false; END IF;
        IF item.value ? 'bid_lots' AND (
            jsonb_typeof(item.value->'bid_lots') IS DISTINCT FROM 'string'
            OR (item.value->>'bid_lots') !~ '^[1-9][0-9]{0,23}$'
        ) THEN RETURN false; END IF;
    END LOOP;
    RETURN true;
END $$;

ALTER TABLE ipo_pos.ipo_dashboard_assumption_versions
    ADD COLUMN bid_forecasts jsonb NOT NULL DEFAULT '{}'
    CHECK (ipo_pos.valid_bid_forecasts(bid_forecasts));

COMMIT;
