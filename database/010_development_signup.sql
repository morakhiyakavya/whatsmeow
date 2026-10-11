BEGIN;
-- Reserve identities without creating users, memberships or businesses before approval.
ALTER TABLE ipo_pos.login_attempts
    ADD COLUMN signup_user_id uuid,
    ADD COLUMN signup_business_id uuid,
    ADD COLUMN signup_account_id uuid UNIQUE,
    ADD CONSTRAINT login_attempts_signup_identity_check CHECK (
        (signup_user_id IS NULL AND signup_business_id IS NULL AND signup_account_id IS NULL)
        OR (signup_user_id IS NOT NULL AND signup_business_id IS NOT NULL AND signup_account_id IS NOT NULL
            AND ((state='pending' AND user_id IS NULL AND business_id IS NULL AND whatsapp_account_id IS NULL)
                OR (state='verified' AND user_id=signup_user_id AND business_id=signup_business_id
                    AND whatsapp_account_id=signup_account_id)))
    );
COMMIT;
