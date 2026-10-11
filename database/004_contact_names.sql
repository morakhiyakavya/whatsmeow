BEGIN;
-- Older customer names have no recorded author; do not invent their provenance.
ALTER TABLE ipo_pos.customers ADD COLUMN name_supplied_by_user_id uuid;
ALTER TABLE ipo_pos.customers ADD FOREIGN KEY (business_id,name_supplied_by_user_id)
    REFERENCES ipo_pos.business_memberships(business_id,user_id);

CREATE TABLE ipo_pos.contact_names (
    business_id uuid NOT NULL,
    phone_number text NOT NULL CHECK (phone_number ~ '^\+[1-9][0-9]{6,14}$'),
    supplied_by_user_id uuid NOT NULL,
    name text NOT NULL CHECK (btrim(name) <> '' AND octet_length(name) <= 256),
    source text NOT NULL DEFAULT 'mobile_contacts' CHECK (source = 'mobile_contacts'),
    received_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (business_id,phone_number,supplied_by_user_id),
    FOREIGN KEY (business_id,supplied_by_user_id)
        REFERENCES ipo_pos.business_memberships(business_id,user_id)
);
COMMIT;
