package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func TestCheckRegisteredNumber(t *testing.T) {
	const phone = "+919000001010"
	for _, test := range []struct {
		name    string
		results []types.IsOnWhatsAppResponse
		err     error
		want    bool
		failed  bool
	}{
		{"registered", []types.IsOnWhatsAppResponse{{Query: phone, IsIn: true}}, nil, true, false},
		{"unregistered", []types.IsOnWhatsAppResponse{{Query: phone, IsIn: false}}, nil, false, false},
		{"without plus", []types.IsOnWhatsAppResponse{{Query: phone[1:], IsIn: true}}, nil, true, false},
		{"missing", nil, nil, false, true},
		{"wrong number", []types.IsOnWhatsAppResponse{{Query: "+919000001011"}}, nil, false, true},
		{"missing query", []types.IsOnWhatsAppResponse{{IsIn: true}}, nil, false, true},
		{"duplicate", []types.IsOnWhatsAppResponse{{Query: phone}, {Query: phone}}, nil, false, true},
		{"partial with error", []types.IsOnWhatsAppResponse{{Query: phone}}, errors.New("upstream failure"), false, true},
		{"timeout", nil, context.DeadlineExceeded, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &fakePairingClient{numberCheck: func(_ context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
				if len(phones) != 1 || phones[0] != phone {
					t.Fatalf("wrong query: %v", phones)
				}
				return test.results, test.err
			}}
			got, err := checkRegisteredNumber(context.Background(), client, phone)
			if got != test.want || (err != nil) != test.failed {
				t.Fatalf("registered=%v error=%v", got, err)
			}
		})
	}
}

func testWhatsAppNumbers(t *testing.T, db *sql.DB, owner, other testClient, s *whatsAppService, client *fakePairingClient, path string) {
	t.Helper()
	const phone = "+919000001010"
	path += "/check-number"
	input := map[string]string{"phone_number": phone}
	parts := strings.Split(path, "/")
	businessID, id := parts[3], parts[5]
	contactPath := "/api/businesses/" + businessID + "/contacts?phone_number=" + url.QueryEscape(phone)
	status, object, _ := owner.request("GET", contactPath, nil, "")
	expectStatus(t, 200, status, object)
	if object["whatsapp_registered"] != nil || object["whatsapp_checked_at"] != nil {
		t.Fatal("unchecked number was reported as registered/unregistered")
	}
	public := owner
	public.Token = ""
	status, object, _ = public.request("POST", path, input, "")
	expectStatus(t, 401, status, object)
	status, object, _ = other.request("POST", path, input, "")
	expectStatus(t, 403, status, object)
	status, object, _ = owner.request("POST", strings.Replace(path, "/whatsapp-accounts/", "/whatsapp-accounts/bad-", 1), input, "")
	expectStatus(t, 400, status, object)
	for _, invalid := range []map[string]string{{}, {"phone_number": "919000001010"}, {"phone_number": "+0919000001010"}, {"phone_number": phone, "registered": "true"}} {
		status, object, _ = owner.request("POST", path, invalid, "")
		expectStatus(t, 400, status, object)
	}
	var savedTime time.Time
	for _, registered := range []bool{true, false} {
		client.numberCheck = func(_ context.Context, phones []string) ([]types.IsOnWhatsAppResponse, error) {
			if len(phones) != 1 || phones[0] != phone {
				t.Error("endpoint changed the queried number")
			}
			return []types.IsOnWhatsAppResponse{{Query: phone, IsIn: registered}}, nil
		}
		status, object, _ = owner.request("POST", path, input, "")
		expectStatus(t, 200, status, object)
		if object["registered"] != registered || object["phone_number"] != phone {
			t.Fatalf("unexpected registration response: %v", object)
		}
		checkedAt, err := time.Parse(time.RFC3339Nano, field(t, object, "checked_at"))
		if err != nil {
			t.Fatal(err)
		}
		var saved bool
		var source string
		if err = db.QueryRow(`SELECT registered,checked_at,whatsapp_account_id FROM ipo_pos.whatsapp_number_checks
			WHERE business_id=$1 AND phone_number=$2`, businessID, phone).Scan(&saved, &savedTime, &source); err != nil {
			t.Fatal(err)
		}
		if saved != registered || !savedTime.Equal(checkedAt) || !strings.EqualFold(source, id) {
			t.Fatal("successful result and timestamp were not saved with the querying account")
		}
		status, object, _ = owner.request("GET", contactPath, nil, "")
		expectStatus(t, 200, status, object)
		if object["whatsapp_registered"] != registered || object["whatsapp_checked_at"] == nil {
			t.Fatal("contact lookup did not expose the stored number status")
		}
	}
	status, object, _ = other.request("GET", "/api/me", nil, "")
	expectStatus(t, 200, status, object)
	otherBusiness := field(t, object["businesses"].([]any)[0].(map[string]any), "id")
	status, object, _ = other.request("GET", "/api/businesses/"+otherBusiness+"/contacts?phone_number="+url.QueryEscape(phone), nil, "")
	expectStatus(t, 200, status, object)
	if object["whatsapp_registered"] != nil || object["whatsapp_checked_at"] != nil {
		t.Fatal("registration status leaked into another business")
	}
	assertSaved := func() {
		t.Helper()
		var saved bool
		var checkedAt time.Time
		if err := db.QueryRow(`SELECT registered,checked_at FROM ipo_pos.whatsapp_number_checks
			WHERE business_id=$1 AND phone_number=$2`, businessID, phone).Scan(&saved, &checkedAt); err != nil || saved || !checkedAt.Equal(savedTime) {
			t.Fatalf("failed check changed the previous result: %v", err)
		}
	}
	for _, upstreamErr := range []error{nil, errors.New("private upstream details"), context.DeadlineExceeded} {
		client.numberCheck = func(context.Context, []string) ([]types.IsOnWhatsAppResponse, error) { return nil, upstreamErr }
		status, object, _ := owner.request("POST", path, input, "")
		want := 502
		if upstreamErr == context.DeadlineExceeded {
			want = 504
		}
		expectStatus(t, want, status, object)
		raw, _ := json.Marshal(object)
		if _, exists := object["registered"]; exists || strings.Contains(string(raw), "private upstream details") {
			t.Fatal("lookup failure exposed upstream details or reported an unregistered number")
		}
		assertSaved()
	}
	// Request-role code cannot forge a successful WhatsApp registration check.
	if _, err := db.Exec(`UPDATE ipo_pos.whatsapp_number_checks SET registered=true WHERE business_id=$1 AND phone_number=$2`, businessID, phone); err == nil {
		t.Fatal("runtime role can forge registration results")
	}
	if _, err := db.Exec(`INSERT INTO ipo_pos.whatsapp_number_checks(business_id,phone_number,registered,whatsapp_account_id) VALUES($1,$2,true,$3)`, businessID, "+919000001011", id); err == nil {
		t.Fatal("runtime role can insert registration results")
	}
	client.numberCheck = func(context.Context, []string) ([]types.IsOnWhatsAppResponse, error) {
		return []types.IsOnWhatsAppResponse{{Query: phone, IsIn: true}}, nil
	}
	if _, err := s.db.Exec(`CREATE FUNCTION ipo_pos.reject_number_check_test() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'private storage details'; END $$;
		CREATE TRIGGER reject_number_check_test BEFORE INSERT OR UPDATE ON ipo_pos.whatsapp_number_checks
		FOR EACH ROW EXECUTE FUNCTION ipo_pos.reject_number_check_test()`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = s.db.Exec(`DROP FUNCTION IF EXISTS ipo_pos.reject_number_check_test() CASCADE`) }()
	status, object, _ = owner.request("POST", path, input, "")
	expectStatus(t, 503, status, object)
	if object["registered"] != nil || field(t, object["error"].(map[string]any), "code") != "whatsapp_check_storage_failed" {
		t.Fatal("unsaved result was reported as successful")
	}
	assertSaved()
	if _, err := s.db.Exec(`DROP FUNCTION ipo_pos.reject_number_check_test() CASCADE`); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	client.numberCheck = func(ctx context.Context, _ []string) ([]types.IsOnWhatsAppResponse, error) {
		defer close(finished)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if result := s.checkNumber(ctx, businessID, id, phone); !errors.Is(result.err, context.DeadlineExceeded) {
		t.Fatalf("lookup did not honour request deadline: %v", result.err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled lookup held the worker")
	}
	assertSaved()
	client.numberCheck = nil
	s.mu.Lock()
	c := s.accounts[strings.ToLower(businessID+"/"+id)]
	s.mu.Unlock()
	c.update("reconnecting", "", time.Time{})
	status, object, _ = owner.request("POST", path, input, "")
	expectStatus(t, 409, status, object)
	c.update("connected", "", time.Time{})
	// The next request must reach the worker after cancellation; the default fake fails immediately.
	status, object, _ = owner.request("POST", path, input, "")
	expectStatus(t, 502, status, object)
}
