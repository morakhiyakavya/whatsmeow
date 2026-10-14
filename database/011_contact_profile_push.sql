BEGIN;

ALTER TABLE ipo_pos.login_sessions ADD CONSTRAINT login_sessions_id_user_unique UNIQUE (id,user_id);
CREATE TABLE ipo_pos.push_devices (
    installation_id uuid PRIMARY KEY,
    fcm_token text NOT NULL UNIQUE CHECK (octet_length(fcm_token) BETWEEN 1 AND 4096 AND fcm_token !~ '[[:space:][:cntrl:]]'),
    user_id uuid NOT NULL,
    session_id uuid NOT NULL,
    generation uuid NOT NULL DEFAULT gen_random_uuid(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (session_id,user_id) REFERENCES ipo_pos.login_sessions(id,user_id) ON DELETE CASCADE
);
CREATE INDEX push_devices_session_idx ON ipo_pos.push_devices(session_id);
CREATE INDEX push_devices_user_idx ON ipo_pos.push_devices(user_id);

CREATE TABLE ipo_pos.contact_profile_revisions (
    business_id uuid NOT NULL,
    whatsapp_account_id uuid NOT NULL,
    phone_number text NOT NULL CHECK (phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    profile_revision bigint NOT NULL DEFAULT 1 CHECK (profile_revision > 0),
    picture_state text NOT NULL CHECK (picture_state IN ('available','hidden','not_set','changed')),
    picture_id text NOT NULL DEFAULT '',
    event_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (business_id,whatsapp_account_id,phone_number),
    FOREIGN KEY (business_id,whatsapp_account_id) REFERENCES ipo_pos.whatsapp_accounts(business_id,id)
);

-- One pending invalidation per installation/contact; newer revisions replace older ones.
CREATE TABLE ipo_pos.profile_push_outbox (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id uuid NOT NULL REFERENCES ipo_pos.push_devices(installation_id) ON DELETE CASCADE,
    device_generation uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES ipo_pos.users(id),
    business_id uuid NOT NULL,
    whatsapp_account_id uuid NOT NULL,
    auth_generation bigint NOT NULL,
    phone_number text NOT NULL,
    profile_revision bigint NOT NULL CHECK (profile_revision > 0),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 5),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '24 hours',
    UNIQUE (installation_id,business_id,whatsapp_account_id,phone_number),
    FOREIGN KEY (business_id,whatsapp_account_id,phone_number)
        REFERENCES ipo_pos.contact_profile_revisions(business_id,whatsapp_account_id,phone_number)
);
CREATE INDEX profile_push_due_idx ON ipo_pos.profile_push_outbox(next_attempt_at,id) WHERE attempts < 5;
CREATE INDEX profile_push_expiry_idx ON ipo_pos.profile_push_outbox(expires_at);
CREATE INDEX profile_push_exhausted_idx ON ipo_pos.profile_push_outbox(next_attempt_at) WHERE attempts >= 5;

-- Disablement permanently removes subscriptions; re-enabling requires device registration.
CREATE FUNCTION ipo_pos.revoke_disabled_push_devices() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    IF NEW.disabled_at IS NOT NULL THEN
        IF TG_TABLE_NAME='users' THEN
            DELETE FROM ipo_pos.push_devices WHERE user_id=NEW.id;
        ELSE
            DELETE FROM ipo_pos.push_devices p USING ipo_pos.login_sessions s
                WHERE p.session_id=s.id AND p.user_id=NEW.user_id
                AND (s.business_id=NEW.business_id OR s.business_id IS NULL);
        END IF;
    END IF;
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.revoke_disabled_push_devices() FROM PUBLIC;
CREATE TRIGGER user_push_revocation AFTER UPDATE OF disabled_at ON ipo_pos.users
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.revoke_disabled_push_devices();
CREATE TRIGGER membership_push_revocation AFTER UPDATE OF disabled_at ON ipo_pos.business_memberships
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.revoke_disabled_push_devices();

COMMENT ON TABLE ipo_pos.push_devices IS 'Private FCM tokens bound to stable POS sessions; never expose through list APIs.';
COMMENT ON TABLE ipo_pos.contact_profile_revisions IS 'Photo state and monotonic invalidation revisions, without image bytes or URLs.';
COMMENT ON TABLE ipo_pos.profile_push_outbox IS 'Trusted-worker invalidations; acceptance by FCM does not prove device delivery.';
COMMIT;
