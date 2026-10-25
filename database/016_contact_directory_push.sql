BEGIN;

CREATE TABLE ipo_pos.contact_directory_revisions (
    business_id uuid PRIMARY KEY REFERENCES ipo_pos.businesses(id),
    revision bigint NOT NULL CHECK (revision > 0)
);
CREATE TABLE ipo_pos.directory_push_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id uuid NOT NULL REFERENCES ipo_pos.push_devices(installation_id) ON DELETE CASCADE,
    device_generation uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES ipo_pos.users(id),
    business_id uuid NOT NULL REFERENCES ipo_pos.businesses(id),
    revision bigint NOT NULL CHECK (revision > 0),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '24 hours',
    UNIQUE (installation_id,business_id)
);
CREATE INDEX directory_push_due_idx ON ipo_pos.directory_push_outbox(next_attempt_at,id) WHERE attempts < 5;
CREATE INDEX directory_push_expiry_idx ON ipo_pos.directory_push_outbox(expires_at);
CREATE INDEX directory_push_exhausted_idx ON ipo_pos.directory_push_outbox(next_attempt_at) WHERE attempts >= 5;

-- Queue in the writer's transaction, including withdrawals and worker-supplied registration changes.
CREATE FUNCTION ipo_pos.contact_directory_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE
    business uuid;
    next_revision bigint;
BEGIN
    IF TG_OP='UPDATE' THEN
        CASE TG_TABLE_NAME
            WHEN 'contact_names' THEN
                IF NEW.name IS NOT DISTINCT FROM OLD.name THEN RETURN NULL; END IF;
            WHEN 'customers' THEN
                IF (NEW.name,NEW.phone_number,NEW.archived_at) IS NOT DISTINCT FROM
                    (OLD.name,OLD.phone_number,OLD.archived_at) THEN RETURN NULL; END IF;
            WHEN 'whatsapp_number_checks' THEN
                IF NEW.registered IS NOT DISTINCT FROM OLD.registered THEN RETURN NULL; END IF;
        END CASE;
    END IF;
    business := CASE WHEN TG_OP='DELETE' THEN OLD.business_id ELSE NEW.business_id END;
    INSERT INTO ipo_pos.contact_directory_revisions AS r(business_id,revision) VALUES(business,1)
        ON CONFLICT(business_id) DO UPDATE SET revision=r.revision+1 RETURNING revision INTO next_revision;
    INSERT INTO ipo_pos.directory_push_outbox AS q
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
    RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.contact_directory_changed() FROM PUBLIC;
CREATE TRIGGER contact_names_push AFTER INSERT OR UPDATE OR DELETE ON ipo_pos.contact_names
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.contact_directory_changed();
CREATE TRIGGER customers_push AFTER INSERT OR UPDATE ON ipo_pos.customers
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.contact_directory_changed();
CREATE TRIGGER number_checks_push AFTER INSERT OR UPDATE ON ipo_pos.whatsapp_number_checks
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.contact_directory_changed();

COMMENT ON TABLE ipo_pos.directory_push_outbox IS 'Coalesced contact-directory invalidations without contact names or numbers; refresh through authorised APIs.';
COMMIT;
