BEGIN;
ALTER TABLE ipo_pos.whatsapp_accounts ADD COLUMN auth_generation bigint NOT NULL DEFAULT 1 CHECK (auth_generation > 0);
-- Fail safely on conflicting existing bindings; never choose or unlink a user's device implicitly.
CREATE UNIQUE INDEX whatsapp_one_link_per_user ON ipo_pos.whatsapp_accounts(linked_by_user_id) WHERE device_jid IS NOT NULL;
CREATE UNIQUE INDEX whatsapp_one_link_per_number ON ipo_pos.whatsapp_accounts(phone_number) WHERE device_jid IS NOT NULL;

ALTER TABLE ipo_pos.login_sessions
    ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
    ADD COLUMN business_id uuid,
    ADD COLUMN whatsapp_account_id uuid,
    ADD COLUMN auth_generation bigint,
    ADD COLUMN device_name text NOT NULL DEFAULT 'Legacy session' CHECK (btrim(device_name) <> '' AND octet_length(device_name) <= 128),
    ADD COLUMN refresh_hash bytea UNIQUE CHECK (octet_length(refresh_hash) = 32),
    ADD COLUMN refresh_expires_at timestamptz,
    ADD FOREIGN KEY (business_id,whatsapp_account_id) REFERENCES ipo_pos.whatsapp_accounts(business_id,id),
    ADD CHECK ((business_id IS NULL AND whatsapp_account_id IS NULL AND auth_generation IS NULL AND refresh_hash IS NULL AND refresh_expires_at IS NULL)
        OR (business_id IS NOT NULL AND whatsapp_account_id IS NOT NULL AND auth_generation IS NOT NULL AND auth_generation > 0 AND refresh_hash IS NOT NULL AND refresh_expires_at IS NOT NULL AND refresh_expires_at > created_at));
CREATE INDEX login_sessions_user_idx ON ipo_pos.login_sessions(user_id,created_at DESC,id);

CREATE TABLE ipo_pos.login_attempts (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    phone_number text NOT NULL CHECK (phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    user_id uuid REFERENCES ipo_pos.users(id),
    business_id uuid,
    whatsapp_account_id uuid,
    device_name text NOT NULL CHECK (btrim(device_name) <> '' AND octet_length(device_name) <= 128),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','verified')),
    auth_generation bigint,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now()+interval '3 minutes',
    consumed_at timestamptz,
    FOREIGN KEY (business_id,whatsapp_account_id) REFERENCES ipo_pos.whatsapp_accounts(business_id,id),
    CHECK ((user_id IS NULL AND business_id IS NULL AND whatsapp_account_id IS NULL)
        OR (user_id IS NOT NULL AND business_id IS NOT NULL AND whatsapp_account_id IS NOT NULL)),
    CHECK (state <> 'verified' OR (user_id IS NOT NULL AND auth_generation IS NOT NULL))
);
CREATE UNIQUE INDEX login_attempts_account_idx ON ipo_pos.login_attempts(business_id,whatsapp_account_id) WHERE consumed_at IS NULL;
CREATE INDEX login_attempts_phone_idx ON ipo_pos.login_attempts(phone_number,created_at);
CREATE INDEX login_attempts_expiry_idx ON ipo_pos.login_attempts(expires_at);

CREATE TABLE ipo_pos.device_login_tokens (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    source_session_id uuid NOT NULL REFERENCES ipo_pos.login_sessions(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES ipo_pos.users(id),
    business_id uuid NOT NULL,
    whatsapp_account_id uuid NOT NULL,
    auth_generation bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now()+interval '2 minutes',
    FOREIGN KEY (business_id,whatsapp_account_id) REFERENCES ipo_pos.whatsapp_accounts(business_id,id),
    UNIQUE(source_session_id)
);
CREATE INDEX device_login_tokens_expiry_idx ON ipo_pos.device_login_tokens(expires_at);

CREATE FUNCTION ipo_pos.advance_auth_generation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF (OLD.device_jid,OLD.linked_by_user_id,OLD.phone_number) IS DISTINCT FROM (NEW.device_jid,NEW.linked_by_user_id,NEW.phone_number) THEN
        NEW.auth_generation := OLD.auth_generation+1;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER whatsapp_auth_generation BEFORE UPDATE ON ipo_pos.whatsapp_accounts FOR EACH ROW EXECUTE FUNCTION ipo_pos.advance_auth_generation();

CREATE FUNCTION ipo_pos.revoke_linked_sessions() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
BEGIN
    IF OLD.device_jid IS NOT NULL AND OLD.auth_generation <> NEW.auth_generation THEN
        DELETE FROM ipo_pos.login_sessions WHERE user_id=OLD.linked_by_user_id;
        DELETE FROM ipo_pos.login_attempts WHERE business_id=OLD.business_id AND whatsapp_account_id=OLD.id;
        UPDATE ipo_pos.transaction_confirmations SET status='cancelled',error_code='sender_unlinked'
            WHERE business_id=OLD.business_id AND whatsapp_account_id=OLD.id AND status='pending';
    END IF;
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.revoke_linked_sessions() FROM PUBLIC;
CREATE TRIGGER whatsapp_session_revocation AFTER UPDATE ON ipo_pos.whatsapp_accounts FOR EACH ROW EXECUTE FUNCTION ipo_pos.revoke_linked_sessions();
COMMIT;
