BEGIN;

CREATE FUNCTION ipo_pos.confirmation_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP='INSERT' THEN
        PERFORM pg_notify('ipo_worker_wake',NEW.business_id::text||'/'||NEW.whatsapp_account_id::text);
    ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM ipo_pos.record_business_data_change(NEW.business_id,'transaction',NEW.transaction_id);
    END IF;
    RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.confirmation_changed() FROM PUBLIC;
CREATE TRIGGER confirmation_wake AFTER INSERT OR UPDATE OF status ON ipo_pos.transaction_confirmations
    FOR EACH ROW EXECUTE FUNCTION ipo_pos.confirmation_changed();

CREATE FUNCTION ipo_pos.push_work_changed() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
BEGIN
    PERFORM pg_notify('ipo_worker_wake','push');
    RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION ipo_pos.push_work_changed() FROM PUBLIC;
CREATE TRIGGER profile_push_wake AFTER INSERT OR UPDATE OF profile_revision ON ipo_pos.profile_push_outbox
    FOR EACH STATEMENT EXECUTE FUNCTION ipo_pos.push_work_changed();
CREATE TRIGGER directory_push_wake AFTER INSERT OR UPDATE OF revision ON ipo_pos.directory_push_outbox
    FOR EACH STATEMENT EXECUTE FUNCTION ipo_pos.push_work_changed();
CREATE TRIGGER business_push_wake AFTER INSERT OR UPDATE OF revision ON ipo_pos.business_push_outbox
    FOR EACH STATEMENT EXECUTE FUNCTION ipo_pos.push_work_changed();

COMMIT;
