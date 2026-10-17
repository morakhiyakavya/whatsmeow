BEGIN;

ALTER TABLE ipo_pos.login_attempts ADD COLUMN recovery_hash bytea UNIQUE
    CHECK (octet_length(recovery_hash)=32);
DROP INDEX ipo_pos.login_attempts_account_idx;
-- Pairing remains exclusive; recovery attempts have independent ownership challenges.
CREATE UNIQUE INDEX login_attempts_account_idx ON ipo_pos.login_attempts(business_id,whatsapp_account_id)
    WHERE consumed_at IS NULL AND recovery_hash IS NULL;

CREATE TABLE ipo_pos.auth_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    occurred_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    user_id uuid,
    session_id uuid,
    reason text NOT NULL CHECK (reason IN ('session_deleted','logout','logout_all','device_removed',
        'session_expired','whatsapp_link_changed','recovery_replaced','refresh_rotated',
        'unknown_refresh','refresh_expired','user_disabled','membership_disabled','business_archived','link_changed')),
    release text NOT NULL DEFAULT '' CHECK (octet_length(release)<=256)
);
CREATE INDEX auth_events_time_idx ON ipo_pos.auth_events(occurred_at);
CREATE INDEX auth_events_user_idx ON ipo_pos.auth_events(user_id,occurred_at);

CREATE FUNCTION ipo_pos.audit_session_deletion() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    INSERT INTO ipo_pos.auth_events(user_id,session_id,reason,release)
    VALUES(OLD.user_id,OLD.id,COALESCE(NULLIF(current_setting('venweb.auth_reason',true),''),'session_deleted'),
        COALESCE(current_setting('venweb.auth_release',true),''));
    RETURN OLD;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.audit_session_deletion() FROM PUBLIC;
CREATE TRIGGER session_deletion_audit AFTER DELETE ON ipo_pos.login_sessions
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.audit_session_deletion();

CREATE OR REPLACE FUNCTION ipo_pos.revoke_linked_sessions() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE previous_reason text := current_setting('venweb.auth_reason',true);
BEGIN
    IF OLD.device_jid IS NOT NULL AND OLD.auth_generation <> NEW.auth_generation THEN
        PERFORM set_config('venweb.auth_reason','whatsapp_link_changed',true);
        DELETE FROM ipo_pos.login_sessions WHERE user_id=OLD.linked_by_user_id;
        PERFORM set_config('venweb.auth_reason',COALESCE(previous_reason,''),true);
        DELETE FROM ipo_pos.login_attempts WHERE business_id=OLD.business_id AND whatsapp_account_id=OLD.id;
        UPDATE ipo_pos.transaction_confirmations SET status='cancelled',error_code='sender_unlinked'
            WHERE business_id=OLD.business_id AND whatsapp_account_id=OLD.id AND status='pending';
    END IF;
    RETURN NEW;
END $$;

COMMENT ON TABLE ipo_pos.auth_events IS 'Private session diagnostics: no credentials, hashes, phone numbers or WhatsApp content. Retain 30 days via operator maintenance.';
COMMIT;
