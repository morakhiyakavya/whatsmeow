-- PostgreSQL 17+. Apply once to a new application database.
-- Independent of whatsmeow's library-managed credential tables.
BEGIN;
CREATE SCHEMA ipo_pos;
-- Reserved for sqlstore. The Go service creates/upgrades its credential tables.
CREATE SCHEMA whatsapp;
SET LOCAL search_path = ipo_pos, pg_catalog;

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (btrim(name) <> ''),
    phone_number text NOT NULL UNIQUE CHECK (phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz
);

CREATE TABLE businesses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (btrim(name) <> ''),
    currency_code text NOT NULL DEFAULT 'INR' CHECK (currency_code ~ '^[A-Z]{3}$'),
    active_rule_version_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

CREATE TABLE business_memberships (
    business_id uuid NOT NULL REFERENCES businesses(id),
    user_id uuid NOT NULL REFERENCES users(id),
    role text NOT NULL CHECK (role IN ('admin', 'cashier')),
    joined_at timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz,
    PRIMARY KEY (business_id, user_id)
);

CREATE TABLE ipos (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code text NOT NULL UNIQUE CHECK (btrim(code) <> ''),
    name text NOT NULL CHECK (btrim(name) <> ''),
    issuer_name text,
    opens_on date,
    closes_on date,
    listing_on date,
    price_low numeric(18,6),
    price_high numeric(18,6),
    metadata jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    CHECK (closes_on >= opens_on),
    CHECK (price_low >= 0 AND price_low <> 'NaN'::numeric),
    CHECK (price_high >= 0 AND price_high <> 'NaN'::numeric),
    CHECK (price_high >= price_low)
);

CREATE TABLE form_types (
    code text PRIMARY KEY,
    name text NOT NULL CHECK (btrim(name) <> '')
);
INSERT INTO form_types (code, name) VALUES
    ('RETAIL', 'Retail'), ('RETAIL_199', 'Retail 199'),
    ('SHNI', 'SHNI'), ('BHNI', 'BHNI'), ('SHARES', 'Shares'),
    ('CE', 'CE'), ('PE', 'PE');

CREATE TABLE customers (
    business_id uuid NOT NULL REFERENCES businesses(id),
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    name text NOT NULL CHECK (btrim(name) <> ''),
    phone_number text CHECK (phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    referred_by_customer_id uuid,
    referral_note text,
    created_at timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    PRIMARY KEY (business_id, id),
    FOREIGN KEY (business_id, referred_by_customer_id) REFERENCES customers(business_id, id),
    CHECK (referred_by_customer_id <> id)
);
CREATE INDEX customers_phone_idx ON customers (business_id, phone_number);

CREATE TABLE whatsapp_accounts (
    business_id uuid NOT NULL REFERENCES businesses(id),
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    label text NOT NULL CHECK (btrim(label) <> ''),
    phone_number text CHECK (phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    device_jid text UNIQUE,
    linked_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    unlinked_at timestamptz,
    PRIMARY KEY (business_id, id),
    FOREIGN KEY (business_id, linked_by_user_id) REFERENCES business_memberships(business_id, user_id)
);

CREATE TABLE conversations (
    business_id uuid NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    whatsapp_account_id uuid NOT NULL,
    customer_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (business_id, id),
    UNIQUE (business_id, whatsapp_account_id, id),
    FOREIGN KEY (business_id, whatsapp_account_id) REFERENCES whatsapp_accounts(business_id, id),
    FOREIGN KEY (business_id, customer_id) REFERENCES customers(business_id, id)
);

-- Known phone-number and LID aliases resolve to one private conversation.
CREATE TABLE conversation_aliases (
    business_id uuid NOT NULL,
    whatsapp_account_id uuid NOT NULL,
    peer_jid text NOT NULL CHECK (peer_jid ~ '^[^@]+@(s\.whatsapp\.net|lid)$'),
    conversation_id uuid NOT NULL,
    PRIMARY KEY (business_id, whatsapp_account_id, peer_jid),
    FOREIGN KEY (business_id, whatsapp_account_id, conversation_id)
        REFERENCES conversations(business_id, whatsapp_account_id, id)
);

CREATE TABLE messages (
    business_id uuid NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL,
    wa_message_id text NOT NULL CHECK (btrim(wa_message_id) <> ''),
    direction text NOT NULL CHECK (direction IN ('incoming', 'outgoing')),
    sender_jid text NOT NULL,
    sender_name text,
    sender_phone text,
    body text NOT NULL CHECK (body <> ''),
    body_revision integer NOT NULL DEFAULT 1 CHECK (body_revision > 0),
    sent_at timestamptz NOT NULL,
    ingested_at timestamptz NOT NULL DEFAULT now(),
    first_seen_via text NOT NULL CHECK (first_seen_via IN ('live', 'history')),
    edited_at timestamptz,
    revoked_at timestamptz,
    PRIMARY KEY (business_id, id),
    UNIQUE (business_id, conversation_id, id),
    UNIQUE (business_id, conversation_id, wa_message_id),
    FOREIGN KEY (business_id, conversation_id) REFERENCES conversations(business_id, id)
);
CREATE INDEX messages_context_idx ON messages (business_id, conversation_id, sent_at DESC, id DESC);

CREATE TABLE automation_rule_versions (
    business_id uuid NOT NULL REFERENCES businesses(id),
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    version integer NOT NULL CHECK (version > 0),
    configuration jsonb NOT NULL CHECK (jsonb_typeof(configuration) = 'object'),
    configuration_schema_version integer NOT NULL DEFAULT 1 CHECK (configuration_schema_version > 0),
    created_by_user_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (business_id, id),
    UNIQUE (business_id, version),
    FOREIGN KEY (business_id, created_by_user_id) REFERENCES business_memberships(business_id, user_id)
);
ALTER TABLE businesses ADD FOREIGN KEY (id, active_rule_version_id)
    REFERENCES automation_rule_versions(business_id, id);

-- One completed evaluation per candidate/job. AI runs before this row is written.
-- Store the exact text plus structured message snapshots, not just mutable message pointers.
CREATE TABLE automation_runs (
    business_id uuid NOT NULL,
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    conversation_id uuid NOT NULL,
    trigger_message_id uuid NOT NULL,
    trigger_revision integer NOT NULL CHECK (trigger_revision > 0),
    rule_version_id uuid NOT NULL,
    model_version text NOT NULL CHECK (btrim(model_version) <> ''),
    prompt_version text NOT NULL CHECK (btrim(prompt_version) <> ''),
    run_key text NOT NULL CHECK (btrim(run_key) <> ''),
    order_key text,
    input_text text NOT NULL CHECK (btrim(input_text) <> ''),
    input_messages jsonb NOT NULL CHECK (jsonb_typeof(input_messages) = 'array'),
    extracted_data jsonb NOT NULL CHECK (jsonb_typeof(extracted_data) = 'object'),
    decision text NOT NULL CHECK (decision IN ('accepted', 'rejected', 'needs_information', 'failed', 'stale')),
    reason text NOT NULL CHECK (btrim(reason) <> ''),
    rule_results jsonb NOT NULL CHECK (jsonb_typeof(rule_results) = 'array'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (business_id, id),
    UNIQUE (business_id, run_key),
    FOREIGN KEY (business_id, conversation_id, trigger_message_id)
        REFERENCES messages(business_id, conversation_id, id),
    FOREIGN KEY (business_id, rule_version_id) REFERENCES automation_rule_versions(business_id, id),
    CHECK (decision <> 'accepted' OR (order_key IS NOT NULL AND btrim(order_key) <> ''))
);
CREATE INDEX automation_runs_conversation_idx ON automation_runs (business_id, conversation_id, created_at DESC, id DESC);

CREATE TABLE transactions (
    business_id uuid NOT NULL REFERENCES businesses(id),
    id uuid NOT NULL DEFAULT gen_random_uuid(),
    idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
    origin text NOT NULL CHECK (origin IN ('manual', 'automatic')),
    created_by_user_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL DEFAULT 1 CHECK (version > 0),
    ipo_id uuid NOT NULL REFERENCES ipos(id),
    customer_id uuid NOT NULL,
    occurred_at timestamptz NOT NULL,
    form_type_code text NOT NULL REFERENCES form_types(code),
    category text NOT NULL CHECK (category IN ('none', 'subject_1', 'subject_2')),
    ce_pe_value numeric(18,6) CHECK (ce_pe_value <> 'NaN'::numeric),
    quantity numeric(18,6) NOT NULL CHECK (quantity > 0 AND quantity <> 'NaN'::numeric),
    rate numeric(18,6) NOT NULL CHECK (rate >= 0 AND rate <> 'NaN'::numeric),
    total_amount numeric(30,2) GENERATED ALWAYS AS (round(rate * quantity, 2)) STORED,
    currency_code text NOT NULL CHECK (currency_code ~ '^[A-Z]{3}$'),
    salesperson_user_id uuid,
    conversation_id uuid,
    status text NOT NULL DEFAULT 'confirmed' CHECK (status IN ('confirmed', 'cancelled')),
    changed_by_user_id uuid,
    automation_run_id uuid,
    change_reason text NOT NULL CHECK (btrim(change_reason) <> ''),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (business_id, id),
    UNIQUE (business_id, idempotency_key),
    FOREIGN KEY (business_id, customer_id) REFERENCES customers(business_id, id),
    FOREIGN KEY (business_id, created_by_user_id) REFERENCES business_memberships(business_id, user_id),
    FOREIGN KEY (business_id, salesperson_user_id) REFERENCES business_memberships(business_id, user_id),
    FOREIGN KEY (business_id, changed_by_user_id) REFERENCES business_memberships(business_id, user_id),
    FOREIGN KEY (business_id, conversation_id) REFERENCES conversations(business_id, id),
    FOREIGN KEY (business_id, automation_run_id) REFERENCES automation_runs(business_id, id),
    CHECK ((form_type_code IN ('CE', 'PE') AND ce_pe_value IS NOT NULL)
        OR (form_type_code NOT IN ('CE', 'PE') AND ce_pe_value IS NULL)),
    CHECK ((origin = 'manual' AND created_by_user_id IS NOT NULL)
        OR (origin = 'automatic' AND created_by_user_id IS NULL)),
    CHECK ((changed_by_user_id IS NOT NULL) <> (automation_run_id IS NOT NULL))
);
CREATE INDEX transactions_ipo_idx ON transactions (business_id, ipo_id, occurred_at DESC, id DESC);
CREATE INDEX transactions_customer_idx ON transactions (business_id, customer_id, occurred_at DESC, id DESC);
CREATE INDEX transactions_date_idx ON transactions (business_id, occurred_at DESC, id DESC);

-- Same snapshot fields, including CE/PE value. No copied defaults or generated
-- expression: the trigger copies the already-calculated amount exactly.
CREATE TABLE transaction_versions (LIKE transactions INCLUDING CONSTRAINTS);
ALTER TABLE transaction_versions
    ADD PRIMARY KEY (business_id, id, version),
    ADD UNIQUE (business_id, automation_run_id),
    ADD FOREIGN KEY (business_id, id) REFERENCES transactions(business_id, id),
    ADD FOREIGN KEY (ipo_id) REFERENCES ipos(id),
    ADD FOREIGN KEY (form_type_code) REFERENCES form_types(code),
    ADD FOREIGN KEY (business_id, customer_id) REFERENCES customers(business_id, id),
    ADD FOREIGN KEY (business_id, created_by_user_id) REFERENCES business_memberships(business_id, user_id),
    ADD FOREIGN KEY (business_id, salesperson_user_id) REFERENCES business_memberships(business_id, user_id),
    ADD FOREIGN KEY (business_id, changed_by_user_id) REFERENCES business_memberships(business_id, user_id),
    ADD FOREIGN KEY (business_id, conversation_id) REFERENCES conversations(business_id, id),
    ADD FOREIGN KEY (business_id, automation_run_id) REFERENCES automation_runs(business_id, id),
    ADD CHECK (total_amount = round(rate * quantity, 2));

CREATE FUNCTION reject_mutation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only; % is forbidden', TG_TABLE_NAME, TG_OP;
END;
$$;

CREATE FUNCTION guard_history_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF pg_trigger_depth() < 2 THEN
        RAISE EXCEPTION 'transaction history is written by the transaction trigger';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION guard_transaction() RETURNS trigger LANGUAGE plpgsql
SET search_path = ipo_pos, pg_catalog AS $$
DECLARE
    business_currency text;
    active_rules uuid;
    run_row automation_runs%ROWTYPE;
    current_message_revision integer;
    current_message_revoked timestamptz;
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.version <> 1 THEN
            RAISE EXCEPTION 'a transaction starts at version 1';
        END IF;
        IF (NEW.origin = 'manual' AND NEW.changed_by_user_id IS DISTINCT FROM NEW.created_by_user_id)
            OR (NEW.origin = 'automatic' AND NEW.automation_run_id IS NULL) THEN
            RAISE EXCEPTION 'creation actor does not match origin';
        END IF;
        NEW.created_at := transaction_timestamp();
    ELSE
        IF ROW(NEW.business_id, NEW.id, NEW.idempotency_key, NEW.origin, NEW.created_by_user_id, NEW.created_at)
            IS DISTINCT FROM ROW(OLD.business_id, OLD.id, OLD.idempotency_key, OLD.origin, OLD.created_by_user_id, OLD.created_at) THEN
            RAISE EXCEPTION 'transaction identity and original creator are immutable';
        END IF;
        IF NEW.version <> OLD.version + 1 THEN
            RAISE EXCEPTION 'supply the expected next version; current version is %', OLD.version;
        END IF;
    END IF;

    SELECT currency_code, active_rule_version_id INTO business_currency, active_rules
        FROM businesses WHERE id = NEW.business_id AND archived_at IS NULL FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'business is missing or archived';
    END IF;
    NEW.currency_code := coalesce(NEW.currency_code, business_currency);
    IF NEW.currency_code <> business_currency THEN
        RAISE EXCEPTION 'transaction currency must match the business currency';
    END IF;

    IF NEW.changed_by_user_id IS NOT NULL THEN
        PERFORM 1 FROM business_memberships bm JOIN users u ON u.id = bm.user_id
            WHERE bm.business_id = NEW.business_id AND bm.user_id = NEW.changed_by_user_id
                AND bm.disabled_at IS NULL AND u.disabled_at IS NULL FOR SHARE OF bm, u;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'change actor must be an active member';
        END IF;
    END IF;

    IF NEW.automation_run_id IS NOT NULL THEN
        SELECT * INTO run_row FROM automation_runs
            WHERE business_id = NEW.business_id AND id = NEW.automation_run_id;
        IF NOT FOUND OR run_row.decision <> 'accepted' THEN
            RAISE EXCEPTION 'automatic changes require an accepted AI decision';
        END IF;
        IF active_rules IS NULL OR active_rules <> run_row.rule_version_id
            OR NEW.conversation_id IS DISTINCT FROM run_row.conversation_id
            OR NEW.idempotency_key <> run_row.order_key THEN
            RAISE EXCEPTION 'automatic change has inactive rules, wrong conversation, or wrong order key';
        END IF;
        SELECT body_revision, revoked_at INTO current_message_revision, current_message_revoked
            FROM messages WHERE business_id = NEW.business_id AND id = run_row.trigger_message_id FOR SHARE;
        IF current_message_revision <> run_row.trigger_revision OR current_message_revoked IS NOT NULL THEN
            RAISE EXCEPTION 'automatic change uses edited or revoked trigger evidence';
        END IF;
    END IF;
    NEW.updated_at := transaction_timestamp();
    RETURN NEW;
END;
$$;

CREATE FUNCTION snapshot_transaction() RETURNS trigger LANGUAGE plpgsql
SET search_path = ipo_pos, pg_catalog AS $$
BEGIN
    INSERT INTO transaction_versions SELECT NEW.*;
    RETURN NEW;
END;
$$;

CREATE TRIGGER transactions_guard BEFORE INSERT OR UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION guard_transaction();
CREATE TRIGGER transactions_snapshot AFTER INSERT OR UPDATE ON transactions
    FOR EACH ROW EXECUTE FUNCTION snapshot_transaction();
CREATE TRIGGER transactions_no_delete BEFORE DELETE ON transactions
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER transactions_no_truncate BEFORE TRUNCATE ON transactions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER versions_internal_insert BEFORE INSERT ON transaction_versions
    FOR EACH ROW EXECUTE FUNCTION guard_history_insert();
CREATE TRIGGER versions_immutable BEFORE UPDATE OR DELETE ON transaction_versions
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER versions_no_truncate BEFORE TRUNCATE ON transaction_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER rules_immutable BEFORE UPDATE OR DELETE ON automation_rule_versions
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER rules_no_truncate BEFORE TRUNCATE ON automation_rule_versions
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER runs_immutable BEFORE UPDATE OR DELETE ON automation_runs
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER runs_no_truncate BEFORE TRUNCATE ON automation_runs
    FOR EACH STATEMENT EXECUTE FUNCTION reject_mutation();

COMMENT ON TABLE transactions IS 'Current IPO/form transaction state. Total is rate times quantity; categories affect later analytics only.';
COMMENT ON TABLE transaction_versions IS 'Complete immutable snapshots, including initial version, generated automatically on every insert/update.';
COMMENT ON COLUMN transactions.ce_pe_value IS 'Transaction-specific CE/PE number, mandatory for CE/PE; never an amount multiplier.';
COMMENT ON COLUMN transactions.idempotency_key IS 'Stable business order identity shared across manual/AI reconciliation; not a new key for every message.';

COMMIT;
