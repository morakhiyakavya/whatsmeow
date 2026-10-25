package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func TestDirectoryPushIntegration(t *testing.T) {
	dsn := os.Getenv("IPO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires disposable schema_test database")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var role string
	if err = db.QueryRow(`SELECT current_user`).Scan(&role); err != nil || role != "schema_test" {
		t.Fatal("requires disposable schema_test database")
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var business, user, session string
	if err = db.QueryRow(`INSERT INTO ipo_pos.businesses(name) VALUES('Directory push fixture') RETURNING id`).Scan(&business); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`INSERT INTO ipo_pos.users(name,phone_number) VALUES('Directory owner','+919000009111') RETURNING id`).Scan(&user); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO ipo_pos.business_memberships(business_id,user_id,role) VALUES($1,$2,'admin')`, business, user)
	if err = db.QueryRow(`INSERT INTO ipo_pos.login_sessions(user_id,token_hash,expires_at) VALUES($1,decode(repeat('ab',32),'hex'),now()+interval '1 hour') RETURNING id`, user).Scan(&session); err != nil {
		t.Fatal(err)
	}
	const installation = "30000000-0000-4000-8000-000000000001"
	exec(`INSERT INTO ipo_pos.push_devices(installation_id,user_id,session_id,fcm_token) VALUES($1,$2,$3,'directory-token')`, installation, user, session)
	exec(`WITH expired AS (INSERT INTO ipo_pos.login_sessions(user_id,token_hash,created_at,expires_at)
		VALUES($1,decode(repeat('ac',32),'hex'),now()-interval '2 hours',now()-interval '1 hour') RETURNING id)
		INSERT INTO ipo_pos.push_devices(installation_id,user_id,session_id,fcm_token)
		SELECT '30000000-0000-4000-8000-000000000002',$1,id,'expired-directory-token' FROM expired`, user)
	var otherBusiness string
	if err = db.QueryRow(`INSERT INTO ipo_pos.businesses(name) VALUES('Unrelated directory') RETURNING id`).Scan(&otherBusiness); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO ipo_pos.customers(business_id,name) VALUES($1,'Other customer')`, otherBusiness)
	var leaked int
	if err = db.QueryRow(`SELECT count(*) FROM ipo_pos.directory_push_outbox WHERE business_id=$1`, otherBusiness).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("cross-business directory notification", err)
	}
	ctx := context.Background()
	count := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM ipo_pos.directory_push_outbox WHERE business_id=$1`, business).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	change := func(name string) {
		t.Helper()
		exec(`INSERT INTO ipo_pos.contact_names(business_id,phone_number,supplied_by_user_id,name) VALUES($1,'+919000009112',$2,$3)
 ON CONFLICT(business_id,phone_number,supplied_by_user_id) DO UPDATE SET name=EXCLUDED.name`, business, user, name)
	}
	// A rollback must not leave a notification or consume a committed revision.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO ipo_pos.contact_names(business_id,phone_number,supplied_by_user_id,name) VALUES($1,'+919000009113',$2,'Rolled back')`, business, user); err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	if count() != 0 {
		t.Fatal("rollback left push work")
	}
	change("First")
	change("Renamed")
	change("Renamed")
	if count() != 1 {
		t.Fatal("directory changes did not coalesce")
	}
	var revision int
	if err = db.QueryRow(`SELECT revision FROM ipo_pos.contact_directory_revisions WHERE business_id=$1`, business).Scan(&revision); err != nil || revision != 2 {
		t.Fatal("unchanged name advanced revision", revision, err)
	}
	sent := 0
	sender := pushSenderFunc(func(_ context.Context, token string, data map[string]string) pushOutcome {
		sent++
		if token != "directory-token" || len(data) != 4 || data["type"] != "contacts_changed" || data["user_id"] != user || data["business_id"] != business || data["directory_revision"] != "2" {
			t.Fatalf("wrong directory payload: %v", data)
		}
		change("Changed during send")
		return pushOutcome{}
	})
	if worked, err := deliverDirectoryPush(ctx, db, sender); !worked || err != nil {
		t.Fatal(worked, err)
	}
	if sent != 1 || count() != 1 {
		t.Fatal("completion deleted a newer invalidation")
	}
	// Retry/backoff and generation checks share the existing profile delivery implementation.
	retry := pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome {
		return pushOutcome{failed: true, retry: true, after: 2 * time.Hour}
	})
	if _, err = deliverDirectoryPush(ctx, db, retry); err != nil {
		t.Fatal(err)
	}
	var delayed bool
	if err = db.QueryRow(`SELECT attempts=1 AND next_attempt_at>now()+interval '119 minutes' FROM ipo_pos.directory_push_outbox WHERE business_id=$1`, business).Scan(&delayed); err != nil || !delayed {
		t.Fatal("retry-after lost", err)
	}
	change("Token changed")
	exec(`UPDATE ipo_pos.push_devices SET generation=gen_random_uuid(),fcm_token='replacement' WHERE installation_id=$1`, installation)
	if _, err = deliverDirectoryPush(ctx, db, pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome {
		t.Fatal("sent to stale generation")
		return pushOutcome{}
	})); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("stale generation retained")
	}
	change("Remove membership")
	exec(`DELETE FROM ipo_pos.contact_names WHERE business_id=$1 AND supplied_by_user_id=$2`, business, user)
	if count() != 1 {
		t.Fatal("withdrawal did not queue an invalidation")
	}
	var customer, account string
	if err = db.QueryRow(`INSERT INTO ipo_pos.customers(business_id,name,phone_number) VALUES($1,'Customer','+919000009114') RETURNING id`, business).Scan(&customer); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE ipo_pos.customers SET name='Renamed customer' WHERE id=$1`, customer)
	exec(`UPDATE ipo_pos.customers SET archived_at=now() WHERE id=$1`, customer)
	if err = db.QueryRow(`INSERT INTO ipo_pos.whatsapp_accounts(business_id,label,linked_by_user_id) VALUES($1,'Directory account',$2) RETURNING id`, business, user).Scan(&account); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO ipo_pos.whatsapp_number_checks(business_id,whatsapp_account_id,phone_number,registered) VALUES($1,$2,'+919000009114',true)`, business, account)
	exec(`UPDATE ipo_pos.whatsapp_number_checks SET registered=false WHERE business_id=$1`, business)
	if err = db.QueryRow(`SELECT revision FROM ipo_pos.contact_directory_revisions WHERE business_id=$1`, business).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE ipo_pos.whatsapp_number_checks SET checked_at=clock_timestamp() WHERE business_id=$1`, business)
	var after int
	if err = db.QueryRow(`SELECT revision FROM ipo_pos.contact_directory_revisions WHERE business_id=$1`, business).Scan(&after); err != nil || after != revision {
		t.Fatal("unchanged registration queued work", err)
	}
	// Rechecking membership at send time also covers removals that do not revoke sessions.
	exec(`UPDATE ipo_pos.businesses SET archived_at=now() WHERE id=$1`, business)
	if _, err = deliverDirectoryPush(ctx, db, pushSenderFunc(func(context.Context, string, map[string]string) pushOutcome {
		t.Fatal("sent for archived business")
		return pushOutcome{}
	})); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("archived business work retained")
	}
	exec(`UPDATE ipo_pos.businesses SET archived_at=NULL WHERE id=$1`, business)
	change("Disable member")
	exec(`UPDATE ipo_pos.business_memberships SET disabled_at=now() WHERE business_id=$1 AND user_id=$2`, business, user)
	if count() != 0 {
		t.Fatal("disablement retained queued contact data")
	}
}
