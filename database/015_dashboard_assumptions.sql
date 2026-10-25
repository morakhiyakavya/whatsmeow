BEGIN;
SET LOCAL search_path = ipo_pos, pg_catalog;

CREATE TABLE ipo_dashboard_basis_versions (
    ipo_id uuid NOT NULL REFERENCES ipos(id),
    version integer NOT NULL CHECK (version > 0),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    shareholder_availability text NOT NULL CHECK (shareholder_availability IN ('available','not_offered','unknown')),
    snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
    PRIMARY KEY (ipo_id, version)
);

CREATE FUNCTION capture_ipo_dashboard_basis(offering uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = ipo_pos, pg_catalog AS $$
DECLARE
    facts jsonb;
    eligibility text;
    previous ipo_dashboard_basis_versions%ROWTYPE;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('ipo-dashboard-basis/' || offering::text, 0));
    SELECT CASE
        WHEN bool_or(coalesce(reserved_shares,0)>0 OR coalesce(percentage,0)>0) THEN 'available'
        WHEN count(*)>0 THEN 'not_offered'
        ELSE 'unknown' END INTO eligibility
    FROM ipo_reservations WHERE ipo_id=offering
        AND lower(category) ~ 'share[[:space:]_-]*holder';

    SELECT jsonb_build_object('schema_version',1,'ipo',jsonb_build_object(
        'id',i.id,'code',i.code,'name',i.name,'board',i.board,'exchange',i.exchange,
        'opens_on',i.opens_on,'closes_on',i.closes_on,'listing_on',i.listing_on,
        'price_low',i.price_low::text,'price_high',i.price_high::text,
        'lot_size',i.lot_size,'source',i.catalogue_source,'source_url',i.source_url),
        'reservations',coalesce((SELECT jsonb_agg(jsonb_build_object(
            'category',r.category,'reserved_shares',r.reserved_shares::text,
            'percentage',r.percentage::text,'basis',r.basis,'source_url',r.source_url)
            ORDER BY r.category) FROM ipo_reservations r WHERE r.ipo_id=i.id),'[]'::jsonb),
        'subscriptions',coalesce((SELECT jsonb_agg(jsonb_build_object(
            'category',s.category,'label',s.label,'source',s.source,'coverage',s.coverage,
            'offered_shares',s.offered_shares::text,'bid_shares',s.bid_shares::text,
            'applications',s.applications::text,'multiple',s.multiple::text)
            ORDER BY s.category) FROM ipo_subscriptions s WHERE s.ipo_id=i.id),'[]'::jsonb))
    INTO facts FROM ipos i WHERE i.id=offering;
    IF NOT FOUND THEN RAISE EXCEPTION 'IPO not found'; END IF;
    SELECT * INTO previous FROM ipo_dashboard_basis_versions
        WHERE ipo_id=offering ORDER BY version DESC LIMIT 1;
    IF NOT FOUND OR previous.snapshot IS DISTINCT FROM facts
        OR previous.shareholder_availability IS DISTINCT FROM eligibility THEN
        INSERT INTO ipo_dashboard_basis_versions(ipo_id,version,shareholder_availability,snapshot)
            VALUES(offering,coalesce(previous.version,0)+1,eligibility,facts);
    END IF;
END;
$$;
REVOKE ALL ON FUNCTION capture_ipo_dashboard_basis(uuid) FROM PUBLIC;

CREATE FUNCTION initial_ipo_dashboard_basis() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = ipo_pos, pg_catalog AS $$
BEGIN
    PERFORM capture_ipo_dashboard_basis(NEW.id);
    RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION initial_ipo_dashboard_basis() FROM PUBLIC;
CREATE TRIGGER ipo_initial_dashboard_basis AFTER INSERT ON ipos
    FOR EACH ROW EXECUTE FUNCTION initial_ipo_dashboard_basis();

CREATE TABLE ipo_dashboard_assumption_versions (
    business_id uuid NOT NULL REFERENCES businesses(id),
    ipo_id uuid NOT NULL REFERENCES ipos(id),
    version integer NOT NULL CHECK (version > 0),
    gmp numeric(18,6) CHECK (gmp <> 'NaN'::numeric),
    subject2_pricing_factor numeric(9,6) NOT NULL DEFAULT 0.8 CHECK (subject2_pricing_factor BETWEEN 0 AND 1),
    retail_expected_applications numeric CHECK (retail_expected_applications > 0 AND retail_expected_applications < 1e24 AND retail_expected_applications=trunc(retail_expected_applications)),
    shni_expected_applications numeric CHECK (shni_expected_applications > 0 AND shni_expected_applications < 1e24 AND shni_expected_applications=trunc(shni_expected_applications)),
    bhni_expected_applications numeric CHECK (bhni_expected_applications > 0 AND bhni_expected_applications < 1e24 AND bhni_expected_applications=trunc(bhni_expected_applications)),
    shareholder_expected_applications numeric CHECK (shareholder_expected_applications > 0 AND shareholder_expected_applications < 1e24 AND shareholder_expected_applications=trunc(shareholder_expected_applications)),
    recorded_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    changed_by_user_id uuid NOT NULL,
    change_reason text NOT NULL CHECK (btrim(change_reason)<>'' AND length(change_reason)<=2048),
    PRIMARY KEY (business_id,ipo_id,version),
    FOREIGN KEY (business_id,changed_by_user_id) REFERENCES business_memberships(business_id,user_id)
);
CREATE INDEX dashboard_assumptions_recorded_idx
    ON ipo_dashboard_assumption_versions(business_id,ipo_id,recorded_at DESC,version DESC);

CREATE FUNCTION guard_dashboard_assumption() RETURNS trigger LANGUAGE plpgsql
SET search_path = ipo_pos, pg_catalog AS $$
DECLARE
    last_version integer;
    eligibility text;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(
        'ipo-dashboard-assumptions/' || NEW.business_id::text || '/' || NEW.ipo_id::text,0));
    PERFORM 1 FROM businesses WHERE id=NEW.business_id AND archived_at IS NULL FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'business is unavailable'; END IF;
    PERFORM 1 FROM business_memberships m JOIN users u ON u.id=m.user_id
        WHERE m.business_id=NEW.business_id AND m.user_id=NEW.changed_by_user_id
        AND m.role='admin' AND m.disabled_at IS NULL AND u.disabled_at IS NULL
        FOR SHARE OF m,u;
    IF NOT FOUND THEN RAISE EXCEPTION 'assumptions require an active business admin'; END IF;
    PERFORM 1 FROM ipos WHERE id=NEW.ipo_id AND archived_at IS NULL;
    IF NOT FOUND THEN RAISE EXCEPTION 'IPO is unavailable'; END IF;
    SELECT version INTO last_version FROM ipo_dashboard_assumption_versions
        WHERE business_id=NEW.business_id AND ipo_id=NEW.ipo_id ORDER BY version DESC LIMIT 1;
    IF NEW.version<>coalesce(last_version,0)+1 THEN
        RAISE EXCEPTION 'assumption version changed' USING ERRCODE='40001';
    END IF;
    SELECT shareholder_availability INTO eligibility FROM ipo_dashboard_basis_versions
        WHERE ipo_id=NEW.ipo_id ORDER BY version DESC LIMIT 1;
    IF eligibility='not_offered' AND NEW.shareholder_expected_applications IS NOT NULL THEN
        RAISE EXCEPTION 'IPO does not offer a shareholder quota';
    END IF;
    NEW.recorded_at := clock_timestamp();
    RETURN NEW;
END;
$$;
CREATE TRIGGER dashboard_assumptions_guard BEFORE INSERT ON ipo_dashboard_assumption_versions
    FOR EACH ROW EXECUTE FUNCTION guard_dashboard_assumption();

CREATE TRIGGER dashboard_assumptions_immutable BEFORE UPDATE OR DELETE ON ipo_dashboard_assumption_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER dashboard_assumptions_no_truncate BEFORE TRUNCATE ON ipo_dashboard_assumption_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER dashboard_basis_immutable BEFORE UPDATE OR DELETE ON ipo_dashboard_basis_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER dashboard_basis_no_truncate BEFORE TRUNCATE ON ipo_dashboard_basis_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();

-- Existing catalogue facts are a migration-time baseline, not reconstructed past observations.
SELECT capture_ipo_dashboard_basis(id) FROM ipos ORDER BY id;
COMMIT;
