BEGIN;

CREATE TABLE ipo_pos.business_data_revisions (
    business_id uuid PRIMARY KEY REFERENCES ipo_pos.businesses(id),
    revision bigint NOT NULL CHECK (revision > 0)
);
CREATE TABLE ipo_pos.business_data_changes (
    business_id uuid NOT NULL REFERENCES ipo_pos.businesses(id),
    kind text NOT NULL CHECK (kind IN ('transaction','assumptions','ipo')),
    record_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    PRIMARY KEY (business_id,kind,record_id)
);
CREATE INDEX business_data_changes_since_idx ON ipo_pos.business_data_changes(business_id,revision,kind,record_id);
CREATE TABLE ipo_pos.business_push_outbox (LIKE ipo_pos.directory_push_outbox INCLUDING ALL);
ALTER TABLE ipo_pos.business_push_outbox
    ADD FOREIGN KEY (installation_id) REFERENCES ipo_pos.push_devices(installation_id) ON DELETE CASCADE,
    ADD FOREIGN KEY (user_id) REFERENCES ipo_pos.users(id),
    ADD FOREIGN KEY (business_id) REFERENCES ipo_pos.businesses(id);

-- The revision row serialises writers until commit; a delta can never skip a late commit.
CREATE FUNCTION ipo_pos.record_business_data_change(business uuid, record_kind text, record uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE next_revision bigint;
BEGIN
    INSERT INTO ipo_pos.business_data_revisions AS r(business_id,revision) VALUES(business,1)
        ON CONFLICT(business_id) DO UPDATE SET revision=r.revision+1 RETURNING revision INTO next_revision;
    INSERT INTO ipo_pos.business_data_changes AS c VALUES(business,record_kind,record,next_revision)
        ON CONFLICT(business_id,kind,record_id) DO UPDATE SET revision=EXCLUDED.revision;
    INSERT INTO ipo_pos.business_push_outbox AS q
        (installation_id,device_generation,user_id,business_id,revision)
        SELECT p.installation_id,p.generation,p.user_id,business,next_revision
        FROM ipo_pos.push_devices p JOIN ipo_pos.login_sessions s ON s.id=p.session_id
        JOIN ipo_pos.users u ON u.id=p.user_id
        JOIN ipo_pos.business_memberships m ON m.business_id=business AND m.user_id=p.user_id
        JOIN ipo_pos.businesses b ON b.id=business
        WHERE COALESCE(s.refresh_expires_at,s.expires_at)>now() AND u.disabled_at IS NULL
        AND m.disabled_at IS NULL AND b.archived_at IS NULL
        AND (s.business_id IS NULL OR s.business_id=business)
        ON CONFLICT(installation_id,business_id) DO UPDATE SET device_generation=EXCLUDED.device_generation,
        user_id=EXCLUDED.user_id,revision=EXCLUDED.revision,attempts=0,
        next_attempt_at=clock_timestamp(),expires_at=clock_timestamp()+interval '24 hours';
END $$;
REVOKE ALL ON FUNCTION ipo_pos.record_business_data_change(uuid,text,uuid) FROM PUBLIC;

CREATE FUNCTION ipo_pos.business_data_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE business uuid; offering uuid;
BEGIN
    IF TG_TABLE_NAME='transactions' THEN
        PERFORM ipo_pos.record_business_data_change(NEW.business_id,'transaction',NEW.id);
    ELSIF TG_TABLE_NAME='ipo_dashboard_assumption_versions' THEN
        PERFORM ipo_pos.record_business_data_change(NEW.business_id,'assumptions',NEW.ipo_id);
    ELSE
        PERFORM pg_advisory_xact_lock(hashtextextended('business-data-catalogue',0));
        IF TG_TABLE_NAME='ipos' THEN
            IF TG_OP='UPDATE' AND (to_jsonb(NEW)-'updated_at') IS NOT DISTINCT FROM
                (to_jsonb(OLD)-'updated_at') THEN RETURN NULL; END IF;
            offering := CASE WHEN TG_OP='DELETE' THEN OLD.id ELSE NEW.id END;
        ELSE offering := NEW.ipo_id;
        END IF;
        -- shortcut: catalogue fanout is per business; batch it before large multi-tenant imports. Keep lock order consistent.
        FOR business IN SELECT id FROM ipo_pos.businesses ORDER BY id LOOP
            PERFORM ipo_pos.record_business_data_change(business,'ipo',offering);
        END LOOP;
    END IF;
    RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.business_data_changed() FROM PUBLIC;
CREATE TRIGGER transactions_data_push AFTER INSERT OR UPDATE ON ipo_pos.transactions
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.business_data_changed();
CREATE TRIGGER assumptions_data_push AFTER INSERT ON ipo_pos.ipo_dashboard_assumption_versions
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.business_data_changed();
CREATE TRIGGER basis_data_push AFTER INSERT ON ipo_pos.ipo_dashboard_basis_versions
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.business_data_changed();
CREATE TRIGGER ipos_data_push AFTER INSERT OR UPDATE OR DELETE ON ipo_pos.ipos
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.business_data_changed();

CREATE FUNCTION ipo_pos.initial_business_data() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended('business-data-catalogue',0));
    INSERT INTO ipo_pos.business_data_revisions VALUES(NEW.id,1);
    INSERT INTO ipo_pos.business_data_changes
        SELECT NEW.id,'ipo',id,1 FROM ipo_pos.ipos;
    RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.initial_business_data() FROM PUBLIC;
CREATE TRIGGER business_initial_data AFTER INSERT ON ipo_pos.businesses
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.initial_business_data();

-- Baseline existing records without publishing historical notifications.
INSERT INTO ipo_pos.business_data_revisions SELECT id,1 FROM ipo_pos.businesses;
INSERT INTO ipo_pos.business_data_changes
    SELECT business_id,'transaction',id,1 FROM ipo_pos.transactions
    UNION ALL SELECT DISTINCT business_id,'assumptions',ipo_id,1 FROM ipo_pos.ipo_dashboard_assumption_versions
    UNION ALL SELECT b.id,'ipo',i.id,1 FROM ipo_pos.businesses b CROSS JOIN ipo_pos.ipos i;
COMMENT ON TABLE ipo_pos.business_data_changes IS 'Latest change per record; null bodies in the sync feed are tombstones. Financial data is fetched only through authenticated APIs.';
COMMENT ON TABLE ipo_pos.business_push_outbox IS 'Coalesced business-data revision hints; no transaction values, customer data or analysis results.';
COMMIT;
