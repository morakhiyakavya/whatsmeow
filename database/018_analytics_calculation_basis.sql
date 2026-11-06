BEGIN;

CREATE FUNCTION ipo_pos.valid_calculation_basis(basis jsonb) RETURNS boolean
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
DECLARE item record;
BEGIN
    IF jsonb_typeof(basis) IS DISTINCT FROM 'object' OR octet_length(basis::text)>4096 THEN RETURN false; END IF;
    FOR item IN SELECT * FROM jsonb_each(basis) LOOP
        IF item.key NOT IN ('RETAIL','RETAIL_199','SHNI','BHNI','SHAREHOLDER')
            OR jsonb_typeof(item.value) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
        IF (SELECT count(*) FROM jsonb_object_keys(item.value))<>3
            OR jsonb_typeof(item.value->'shares_per_application') IS DISTINCT FROM 'string'
            OR jsonb_typeof(item.value->'capacity') IS DISTINCT FROM 'string'
            OR jsonb_typeof(item.value->'unit') IS DISTINCT FROM 'string' THEN RETURN false; END IF;
        IF (item.value->>'shares_per_application') !~ '^[0-9]{1,12}(\.[0-9]{1,6})?$'
            OR (item.value->>'capacity') !~ '^[0-9]{1,24}$' THEN RETURN false; END IF;
        IF (item.value->>'shares_per_application')::numeric<=0 OR (item.value->>'capacity')::numeric<=0
            OR (item.value->>'unit') NOT IN ('applications','bid_amount_crore')
            OR ((item.value->>'unit')='bid_amount_crore' AND item.key<>'SHAREHOLDER') THEN RETURN false; END IF;
    END LOOP;
    RETURN true;
END $$;

ALTER TABLE ipo_pos.ipo_dashboard_assumption_versions ADD COLUMN calculation_basis jsonb NOT NULL DEFAULT '{}'
    CHECK (ipo_pos.valid_calculation_basis(calculation_basis));
ALTER TABLE ipo_pos.ipo_dashboard_assumption_versions ADD COLUMN additional_cost numeric(18,2) NOT NULL DEFAULT 0
    CHECK (additional_cost<>'NaN'::numeric);
COMMENT ON COLUMN ipo_pos.ipo_dashboard_assumption_versions.calculation_basis IS
 'Attributed business planning assumptions, not confirmed allotments. Capacity and expected total must use the stated matching unit; bid_amount_crore uses bid shares per application.';
COMMIT;
