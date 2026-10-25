-- psql variables: database_name and api_role. Apply after all application migrations.
GRANT CONNECT ON DATABASE :"database_name" TO :"api_role";
GRANT USAGE ON SCHEMA ipo_pos TO :"api_role";
GRANT SELECT ON ALL TABLES IN SCHEMA ipo_pos TO :"api_role";
GRANT INSERT ON ipo_pos.users, ipo_pos.businesses, ipo_pos.business_memberships,
    ipo_pos.user_credentials, ipo_pos.manual_transaction_requests,
    ipo_pos.transaction_versions TO :"api_role";
REVOKE INSERT ON ipo_pos.ipos FROM :"api_role";
GRANT INSERT (code,name,issuer_name,opens_on,closes_on,listing_on,price_low,price_high,metadata)
    ON ipo_pos.ipos TO :"api_role";
GRANT INSERT (scheduled_for) ON ipo_pos.ipo_import_runs TO :"api_role";
GRANT INSERT, UPDATE ON ipo_pos.customers, ipo_pos.transactions TO :"api_role";
GRANT INSERT, UPDATE, DELETE ON ipo_pos.contact_names TO :"api_role";
GRANT INSERT ON ipo_pos.transaction_confirmations TO :"api_role";
GRANT INSERT ON ipo_pos.ipo_dashboard_assumption_versions TO :"api_role";
GRANT INSERT, UPDATE, DELETE ON ipo_pos.login_sessions TO :"api_role";
GRANT INSERT, UPDATE, DELETE ON ipo_pos.device_login_tokens TO :"api_role";
GRANT INSERT, UPDATE, DELETE ON ipo_pos.push_devices TO :"api_role";
GRANT INSERT (user_id,session_id,reason,release) ON ipo_pos.auth_events TO :"api_role";
GRANT UPDATE (consumed_at) ON ipo_pos.login_attempts TO :"api_role";
-- Only the trusted WhatsApp worker may supply bindings and verification results.
GRANT INSERT (business_id, label, linked_by_user_id) ON ipo_pos.whatsapp_accounts TO :"api_role";
-- Row-locking guards need UPDATE permission on at least one identity column.
GRANT UPDATE (id) ON ipo_pos.businesses, ipo_pos.users, ipo_pos.whatsapp_accounts TO :"api_role";
GRANT UPDATE (user_id) ON ipo_pos.business_memberships TO :"api_role";
