-- Synthetic fixtures only. Run after 001_initial.sql; all fixtures roll back.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL search_path = ipo_pos, pg_catalog;

CREATE FUNCTION pg_temp.assert_true(ok boolean, message text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    IF NOT coalesce(ok, false) THEN RAISE EXCEPTION 'assertion failed: %', message; END IF;
END;
$$;
CREATE FUNCTION pg_temp.expect_failure(statement text, expected_state text) RETURNS void LANGUAGE plpgsql AS $$
BEGIN
    BEGIN
        EXECUTE statement;
    EXCEPTION WHEN OTHERS THEN
        IF SQLSTATE = expected_state THEN RETURN; END IF;
        RAISE EXCEPTION 'expected SQLSTATE %, got %: %', expected_state, SQLSTATE, SQLERRM;
    END;
    RAISE EXCEPTION 'statement unexpectedly succeeded: %', statement;
END;
$$;

DO $$
DECLARE
    staff uuid;
    shop uuid;
    other_shop uuid;
    customer uuid;
    other_customer uuid;
    offering uuid;
    account uuid;
    chat uuid;
    other_chat uuid;
    message uuid;
    rules uuid;
    run uuid;
    rejected_run uuid;
    sale uuid;
    auto_sale uuid;
    base_insert text;
    auto_insert text;
BEGIN
    INSERT INTO users(name, phone_number) VALUES ('Test cashier', '+919000000001') RETURNING id INTO staff;
    INSERT INTO businesses(name) VALUES ('Test business') RETURNING id INTO shop;
    INSERT INTO businesses(name) VALUES ('Other business') RETURNING id INTO other_shop;
    INSERT INTO business_memberships VALUES (shop, staff, 'cashier', now(), NULL), (other_shop, staff, 'admin', now(), NULL);
    PERFORM pg_temp.expect_failure(format('INSERT INTO business_memberships VALUES (%L,%L,''manager'',now(),NULL)', shop, gen_random_uuid()), '23514');
    INSERT INTO customers(business_id,name) VALUES (shop,'Test customer') RETURNING id INTO customer;
    INSERT INTO customers(business_id,name) VALUES (other_shop,'Other customer') RETURNING id INTO other_customer;
    PERFORM pg_temp.expect_failure(format('UPDATE customers SET referred_by_customer_id=%L WHERE business_id=%L AND id=%L', other_customer, shop, customer), '23503');
    PERFORM pg_temp.expect_failure(format('UPDATE customers SET referred_by_customer_id=id WHERE business_id=%L AND id=%L', shop, customer), '23514');
    INSERT INTO ipos(code,name) VALUES ('TEST-IPO','Synthetic IPO') RETURNING id INTO offering;
    INSERT INTO whatsapp_accounts(business_id,label,linked_by_user_id) VALUES (shop,'Test account',staff) RETURNING id INTO account;
    INSERT INTO whatsapp_number_checks(business_id,phone_number,registered,whatsapp_account_id)
        VALUES (shop,'+919000000002',false,account);
    PERFORM pg_temp.assert_true((SELECT registered=false AND checked_at IS NOT NULL FROM whatsapp_number_checks
        WHERE business_id=shop AND phone_number='+919000000002'), 'registration is a stored boolean with a check timestamp');
    PERFORM pg_temp.expect_failure(format('INSERT INTO whatsapp_number_checks(business_id,phone_number,registered,whatsapp_account_id) VALUES(%L,''+919000000002'',true,%L)',shop,account), '23505');
    PERFORM pg_temp.expect_failure(format('INSERT INTO whatsapp_number_checks(business_id,phone_number,registered,whatsapp_account_id) VALUES(%L,''123'',false,%L)',shop,account), '23514');
    PERFORM pg_temp.expect_failure(format('INSERT INTO whatsapp_number_checks(business_id,phone_number,registered,whatsapp_account_id) VALUES(%L,''+919000000003'',NULL,%L)',shop,account), '23502');
    PERFORM pg_temp.expect_failure(format('INSERT INTO whatsapp_number_checks(business_id,phone_number,registered,whatsapp_account_id) VALUES(%L,''+919000000002'',true,%L)',other_shop,account), '23503');
    INSERT INTO conversations(business_id,whatsapp_account_id,customer_id) VALUES (shop,account,customer) RETURNING id INTO chat;
    INSERT INTO conversations(business_id,whatsapp_account_id) VALUES (shop,account) RETURNING id INTO other_chat;
    INSERT INTO conversation_aliases VALUES (shop,account,'919000000002@s.whatsapp.net',chat), (shop,account,'123456@lid',chat);
    PERFORM pg_temp.expect_failure(format('INSERT INTO conversation_aliases VALUES (%L,%L,''123@g.us'',%L)', shop,account,chat), '23514');
    INSERT INTO messages(business_id,conversation_id,wa_message_id,direction,sender_jid,body,sent_at,first_seen_via)
        VALUES (shop,chat,'test-message','incoming','123456@lid','Buy 2.5 CE forms at 250',now(),'live') RETURNING id INTO message;
    PERFORM pg_temp.expect_failure(format('INSERT INTO messages(business_id,conversation_id,wa_message_id,direction,sender_jid,body,sent_at,first_seen_via) VALUES (%L,%L,''test-message'',''incoming'',''123456@lid'',''Duplicate'',now(),''history'')', shop,chat), '23505');

    -- No multiplier: CE value and category never affect 250 * 2.5 = 625.
    base_insert := format('INSERT INTO transactions(business_id,idempotency_key,origin,created_by_user_id,ipo_id,customer_id,occurred_at,form_type_code,category,ce_pe_value,quantity,rate,changed_by_user_id,change_reason) VALUES (%L,''manual-1'',''manual'',%L,%L,%L,now(),''CE'',''subject_1'',20,2.5,250,%L,''Created'')', shop,staff,offering,customer,staff);
    EXECUTE base_insert;
    SELECT id INTO sale FROM transactions WHERE business_id=shop AND idempotency_key='manual-1';
    PERFORM pg_temp.assert_true((SELECT total_amount=625 AND currency_code='INR' FROM transactions WHERE business_id=shop AND id=sale), 'decimal amount and business currency');
    PERFORM pg_temp.assert_true((SELECT count(*)=1 FROM transaction_versions WHERE business_id=shop AND id=sale), 'initial version captured');
    PERFORM pg_temp.expect_failure(base_insert, '23505');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET ce_pe_value=NULL,version=2 WHERE business_id=%L AND id=%L', shop,sale), '23514');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET form_type_code=''RETAIL'',version=2 WHERE business_id=%L AND id=%L', shop,sale), '23514');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET quantity=0,version=2 WHERE business_id=%L AND id=%L', shop,sale), '23514');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET quantity=''NaN'',version=2 WHERE business_id=%L AND id=%L', shop,sale), '23514');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET rate=-1,version=2 WHERE business_id=%L AND id=%L', shop,sale), '23514');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET customer_id=%L,version=2 WHERE business_id=%L AND id=%L', other_customer,shop,sale), '23503');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET quantity=3 WHERE business_id=%L AND id=%L', shop,sale), 'P0001');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET idempotency_key=''changed'',version=2 WHERE business_id=%L AND id=%L', shop,sale), 'P0001');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET currency_code=''USD'',version=2 WHERE business_id=%L AND id=%L', shop,sale), 'P0001');

    UPDATE transactions SET quantity=3.25,ce_pe_value=40.5,category='subject_2',version=2,change_reason='Corrected quantity and CE value'
        WHERE business_id=shop AND id=sale AND version=1;
    PERFORM pg_temp.assert_true((SELECT total_amount=812.50 AND ce_pe_value=40.5 FROM transactions WHERE business_id=shop AND id=sale), 'updated fractional quantity/value');
    PERFORM pg_temp.assert_true((SELECT quantity=2.5 AND ce_pe_value=20 AND category='subject_1' AND total_amount=625 FROM transaction_versions WHERE business_id=shop AND id=sale AND version=1), 'old fields unchanged');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET quantity=4,version=2 WHERE business_id=%L AND id=%L', shop,sale), 'P0001');
    UPDATE transactions SET form_type_code='RETAIL',ce_pe_value=NULL,category='none',version=3,change_reason='Changed form type' WHERE business_id=shop AND id=sale;
    UPDATE transactions SET form_type_code='PE',ce_pe_value=12.5,version=4,change_reason='Changed to PE' WHERE business_id=shop AND id=sale;
    UPDATE transactions SET status='cancelled',version=5,change_reason='Cancelled' WHERE business_id=shop AND id=sale;
    PERFORM pg_temp.assert_true((SELECT count(*)=5 FROM transaction_versions WHERE business_id=shop AND id=sale), 'one snapshot per successful revision');
    PERFORM pg_temp.expect_failure(format('UPDATE transaction_versions SET quantity=99 WHERE business_id=%L AND id=%L', shop,sale), 'P0001');
    PERFORM pg_temp.expect_failure(format('DELETE FROM transaction_versions WHERE business_id=%L AND id=%L', shop,sale), 'P0001');
    PERFORM pg_temp.expect_failure(format('INSERT INTO transaction_versions SELECT * FROM transactions WHERE business_id=%L AND id=%L', shop,sale), 'P0001');
    PERFORM pg_temp.expect_failure(format('DELETE FROM transactions WHERE business_id=%L AND id=%L', shop,sale), 'P0001');
    PERFORM pg_temp.expect_failure('TRUNCATE transaction_versions CASCADE', 'P0001');

    INSERT INTO automation_rule_versions(business_id,version,configuration,created_by_user_id) VALUES (shop,1,'{"allow_automatic":true}',staff) RETURNING id INTO rules;
    UPDATE businesses SET active_rule_version_id=rules WHERE id=shop;
    PERFORM pg_temp.expect_failure(format('UPDATE businesses SET active_rule_version_id=%L WHERE id=%L', rules,other_shop), '23503');
    INSERT INTO automation_runs(business_id,conversation_id,trigger_message_id,trigger_revision,rule_version_id,model_version,prompt_version,run_key,order_key,input_text,input_messages,extracted_data,decision,reason,rule_results)
        VALUES (shop,chat,message,1,rules,'test-model','v1','run-1','order-1','Exact synthetic conversation text','[{"body":"Buy 2.5 CE forms at 250","revision":1}]','{"quantity":2.5,"rate":250}','accepted','All configured rules passed','[{"rule":"test","passed":true}]') RETURNING id INTO run;
    INSERT INTO automation_runs(business_id,conversation_id,trigger_message_id,trigger_revision,rule_version_id,model_version,prompt_version,run_key,input_text,input_messages,extracted_data,decision,reason,rule_results)
        VALUES (shop,chat,message,1,rules,'test-model','v1','run-rejected','Exact text','[]','{}','rejected','Missing acceptance','[]') RETURNING id INTO rejected_run;
    auto_insert := format('INSERT INTO transactions(business_id,idempotency_key,origin,ipo_id,customer_id,occurred_at,form_type_code,category,ce_pe_value,quantity,rate,conversation_id,automation_run_id,change_reason) VALUES (%L,''order-1'',''automatic'',%L,%L,now(),''CE'',''none'',20,2.5,250,%L,%L,''AI rule acceptance'')', shop,offering,customer,chat,run);
    PERFORM pg_temp.expect_failure(replace(auto_insert,run::text,rejected_run::text), 'P0001');
    PERFORM pg_temp.expect_failure(replace(auto_insert,chat::text,other_chat::text), 'P0001');
    UPDATE businesses SET active_rule_version_id=NULL WHERE id=shop;
    PERFORM pg_temp.expect_failure(auto_insert, 'P0001');
    UPDATE businesses SET active_rule_version_id=rules WHERE id=shop;
    UPDATE messages SET body_revision=2,body='Edited text' WHERE business_id=shop AND id=message;
    PERFORM pg_temp.expect_failure(auto_insert, 'P0001');
    UPDATE messages SET body_revision=1,revoked_at=now() WHERE business_id=shop AND id=message;
    PERFORM pg_temp.expect_failure(auto_insert, 'P0001');
    UPDATE messages SET revoked_at=NULL WHERE business_id=shop AND id=message;
    EXECUTE auto_insert;
    SELECT id INTO auto_sale FROM transactions WHERE business_id=shop AND idempotency_key='order-1';
    PERFORM pg_temp.assert_true((SELECT automation_run_id=run AND total_amount=625 FROM transaction_versions WHERE business_id=shop AND id=auto_sale AND version=1), 'automatic version links exact decision');
    PERFORM pg_temp.expect_failure(auto_insert, '23505');
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET version=2 WHERE business_id=%L AND id=%L', shop,auto_sale), '23505');
    UPDATE transactions SET changed_by_user_id=staff,automation_run_id=NULL,quantity=3,version=2,change_reason='Cashier corrected automatic transaction'
        WHERE business_id=shop AND id=auto_sale;
    PERFORM pg_temp.assert_true((SELECT origin='automatic' AND changed_by_user_id=staff AND total_amount=750 FROM transactions WHERE business_id=shop AND id=auto_sale), 'manual correction preserves original automatic origin');
    PERFORM pg_temp.expect_failure(format('UPDATE automation_runs SET reason=''Rewritten'' WHERE business_id=%L AND id=%L', shop,run), 'P0001');
    PERFORM pg_temp.expect_failure(format('DELETE FROM automation_rule_versions WHERE business_id=%L AND id=%L', shop,rules), 'P0001');
    UPDATE business_memberships SET disabled_at=now() WHERE business_id=shop AND user_id=staff;
    PERFORM pg_temp.expect_failure(format('UPDATE transactions SET version=3 WHERE business_id=%L AND id=%L', shop,auto_sale), 'P0001');
    RAISE NOTICE 'All schema assertions passed';
END;
$$;
ROLLBACK;
