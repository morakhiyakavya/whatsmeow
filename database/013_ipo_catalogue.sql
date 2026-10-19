BEGIN;
SET LOCAL search_path = ipo_pos, pg_catalog;

ALTER TABLE ipos
    ADD COLUMN catalogue_source text CHECK (catalogue_source = 'nse'),
    ADD COLUMN source_issue_id text,
    ADD COLUMN symbol text,
    ADD COLUMN exchange text CHECK (exchange IN ('NSE','BSE')),
    ADD COLUMN board text CHECK (board IN ('Mainboard','SME')),
    ADD COLUMN source_status text CHECK (source_status IN ('active','postponed','withdrawn')),
    ADD COLUMN lot_size bigint CHECK (lot_size > 0),
    ADD COLUMN registrar text,
    ADD COLUMN issue_size_text text,
    ADD COLUMN issue_size_shares numeric(24,0) CHECK (issue_size_shares >= 0 AND issue_size_shares <> 'NaN'::numeric),
    ADD COLUMN issue_size_amount numeric(24,2) CHECK (issue_size_amount >= 0 AND issue_size_amount <> 'NaN'::numeric),
    ADD COLUMN source_url text,
    ADD COLUMN last_seen_at timestamptz,
    ADD COLUMN terms_observed_at timestamptz,
    ADD COLUMN timeline jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(timeline)='object'),
    ADD CONSTRAINT ipos_source_identity_check CHECK ((catalogue_source IS NULL) = (source_issue_id IS NULL)),
    ADD CONSTRAINT ipos_source_identity_unique UNIQUE (catalogue_source,source_issue_id);

CREATE TABLE ipo_subscriptions (
    ipo_id uuid NOT NULL REFERENCES ipos(id),
    category text NOT NULL CHECK (btrim(category)<>''),
    label text NOT NULL CHECK (btrim(label)<>''),
    source text NOT NULL CHECK (source='nse'),
    coverage text NOT NULL DEFAULT 'NSE only' CHECK (coverage='NSE only'),
    offered_shares numeric(24,0) CHECK (offered_shares>=0 AND offered_shares<>'NaN'::numeric),
    bid_shares numeric(24,0) CHECK (bid_shares>=0 AND bid_shares<>'NaN'::numeric),
    applications numeric(24,0) CHECK (applications>=0 AND applications<>'NaN'::numeric),
    multiple numeric(24,6) CHECK (multiple>=0 AND multiple<>'NaN'::numeric),
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (ipo_id,category)
);

CREATE TABLE ipo_reservations (
    ipo_id uuid NOT NULL REFERENCES ipos(id),
    category text NOT NULL CHECK (btrim(category)<>''),
    reserved_shares numeric(24,0) CHECK (reserved_shares>=0 AND reserved_shares<>'NaN'::numeric),
    percentage numeric(9,6) CHECK (percentage BETWEEN 0 AND 100),
    basis text NOT NULL,
    source_url text NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (ipo_id,category),
    CHECK (reserved_shares IS NOT NULL OR percentage IS NOT NULL)
);

CREATE TABLE ipo_promoter_shareholdings (
    ipo_id uuid NOT NULL REFERENCES ipos(id),
    holder text NOT NULL CHECK (btrim(holder)<>''),
    pre_percentage numeric(9,6) CHECK (pre_percentage BETWEEN 0 AND 100),
    post_percentage numeric(9,6) CHECK (post_percentage BETWEEN 0 AND 100),
    source_url text NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (ipo_id,holder),
    CHECK (pre_percentage IS NOT NULL OR post_percentage IS NOT NULL)
);

CREATE TABLE ipo_anchor_lockins (
    ipo_id uuid NOT NULL REFERENCES ipos(id),
    label text NOT NULL CHECK (btrim(label)<>''),
    expires_on date NOT NULL,
    source_url text NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (ipo_id,label)
);

CREATE TABLE ipo_import_runs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    scheduled_for timestamptz NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','succeeded','partial','failed')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    source_results jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(source_results)='object'),
    imported_count integer NOT NULL DEFAULT 0 CHECK (imported_count>=0)
);
CREATE INDEX ipo_import_due_idx ON ipo_import_runs(next_attempt_at) WHERE status IN ('pending','running','partial','failed') AND attempts<3;
CREATE INDEX ipo_import_finished_idx ON ipo_import_runs(finished_at DESC) WHERE status IN ('succeeded','partial');
COMMIT;
