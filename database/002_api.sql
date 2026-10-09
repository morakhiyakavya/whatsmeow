BEGIN;
SET LOCAL search_path = ipo_pos, pg_catalog;

CREATE TABLE user_credentials (
    user_id uuid PRIMARY KEY REFERENCES users(id),
    password_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE login_sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at)
);
CREATE INDEX login_sessions_expiry_idx ON login_sessions(expires_at);

-- Original request identity survives edits, so replaying creation returns the
-- existing transaction and a different payload with the same key is rejected.
CREATE TABLE manual_transaction_requests (
    business_id uuid NOT NULL,
    idempotency_key text NOT NULL,
    transaction_id uuid NOT NULL,
    user_id uuid NOT NULL,
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    PRIMARY KEY (business_id, idempotency_key),
    FOREIGN KEY (business_id, transaction_id) REFERENCES transactions(business_id, id),
    FOREIGN KEY (business_id, user_id) REFERENCES business_memberships(business_id, user_id)
);
CREATE TRIGGER manual_requests_immutable BEFORE UPDATE OR DELETE ON manual_transaction_requests
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER manual_requests_no_truncate BEFORE TRUNCATE ON manual_transaction_requests
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();

CREATE INDEX customers_created_idx ON customers(business_id, created_at DESC, id DESC);
CREATE INDEX ipos_created_idx ON ipos(created_at DESC, id DESC);
COMMIT;
