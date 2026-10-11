package main

import (
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestSignedTransactionValidation(t *testing.T) {
	for _, test := range []struct {
		quantity, rate, want string
		valid                bool
	}{
		{"2.500000", "250", "2.5", true},
		{"-2.500000", "250", "-2.5", true},
		{"-0.000001", "0", "-0.000001", true},
		{"0", "250", "", false},
		{"-0.000000", "250", "", false},
		{"-1.1234567", "250", "", false},
		{"-1000000000000", "250", "", false},
		{"NaN", "250", "", false},
		{"2.5", "-250", "2.5", true},
		{"-2.5", "-250", "-2.5", true},
		{"2.5", "-0.000000", "2.5", true},
		{"2.5", "-0.000001", "2.5", true},
		{"2.5", "-1.1234567", "", false},
		{"2.5", "-1000000000000", "", false},
		{"2.5", "NaN", "", false},
		{"2.5", "-Infinity", "", false},
	} {
		t.Run(test.quantity+"/"+test.rate, func(t *testing.T) {
			input := transactionFields{
				IPOID: "00000000-0000-0000-0000-000000000001", CustomerID: "00000000-0000-0000-0000-000000000002",
				OccurredAt: time.Now(), FormType: "RETAIL", Quantity: test.quantity, Rate: test.rate,
			}
			w := httptest.NewRecorder()
			if input.validate(w) != test.valid || (test.valid && input.Quantity != test.want) || (!test.valid && w.Code != 400) {
				t.Fatalf("quantity=%q rate=%q: normalized=%q response=%d", test.quantity, test.rate, input.Quantity, w.Code)
			}
		})
	}
}

func testSignedTransactionAPI(t *testing.T, client testClient, base string, request map[string]any) {
	client.t = t
	body := cloneMap(request)
	body["quantity"] = "-2.5"
	status, obj, _ := client.request("POST", base+"/transactions", body, "signed-sell")
	expectStatus(t, 201, status, obj)
	id := field(t, obj, "id")
	if field(t, obj, "quantity") != "-2.500000" || field(t, obj, "total_amount") != "-625.00" {
		t.Fatal("sell creation lost the signed quantity or total")
	}
	body["quantity"] = "-2.500000"
	status, obj, headers := client.request("POST", base+"/transactions", body, "signed-sell")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "id") != id || headers.Get("Idempotency-Replayed") != "true" {
		t.Fatal("equivalent signed retry created another transaction")
	}
	status, obj, _ = client.request("POST", base+"/transactions", request, "signed-sell")
	expectStatus(t, 409, status, obj)
	body["quantity"], body["expected_version"], body["change_reason"] = "-3.25", 1, "Corrected sell quantity"
	status, obj, _ = client.request("PUT", base+"/transactions/"+id, body, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "total_amount") != "-812.50" || obj["version"] != float64(2) {
		t.Fatal("sell edit lost its sign or version")
	}
	status, obj, _ = client.request("GET", base+"/transactions/"+id+"/history", nil, "")
	expectStatus(t, 200, status, obj)
	history := obj["items"].([]any)
	if len(history) != 2 || field(t, history[1].(map[string]any), "total_amount") != "-625.00" {
		t.Fatal("sell edit rewrote its original history")
	}
	status, obj, _ = client.request("POST", base+"/transactions/"+id+"/cancel", map[string]any{"expected_version": 2, "change_reason": "Cancelled sell"}, "")
	expectStatus(t, 200, status, obj)
	if field(t, obj, "status") != "cancelled" || field(t, obj, "total_amount") != "-812.50" {
		t.Fatal("sell cancellation lost the signed amount")
	}
	for i, test := range []struct{ quantity, rate, total string }{
		{"2.5", "-250", "-625.00"},
		{"-2.5", "-250", "625.00"},
		{"-2.5", "0", "0.00"},
	} {
		body := cloneMap(request)
		body["quantity"], body["rate"] = test.quantity, test.rate
		key := "signed-rate-" + strconv.Itoa(i)
		status, obj, _ := client.request("POST", base+"/transactions", body, key)
		expectStatus(t, 201, status, obj)
		id := field(t, obj, "id")
		if field(t, obj, "total_amount") != test.total {
			t.Fatal("signed rate creation calculated the wrong total")
		}
		body["rate"] = test.rate + ".000000"
		status, obj, headers := client.request("POST", base+"/transactions", body, key)
		expectStatus(t, 200, status, obj)
		if field(t, obj, "id") != id || headers.Get("Idempotency-Replayed") != "true" {
			t.Fatal("equivalent signed rate retry created another transaction")
		}
		body["rate"] = "250"
		status, obj, _ = client.request("POST", base+"/transactions", body, key)
		expectStatus(t, 409, status, obj)
		body["rate"] = "-125"
		body["expected_version"], body["change_reason"] = 1, "Corrected rate sign"
		status, obj, _ = client.request("PUT", base+"/transactions/"+id, body, "")
		expectStatus(t, 200, status, obj)
		want := "-312.50"
		if test.quantity == "-2.5" {
			want = "312.50"
		}
		if field(t, obj, "total_amount") != want || obj["version"] != float64(2) {
			t.Fatal("rate correction did not recalculate the signed total")
		}
		status, obj, _ = client.request("GET", base+"/transactions/"+id+"/history", nil, "")
		expectStatus(t, 200, status, obj)
		history := obj["items"].([]any)
		if len(history) != 2 || field(t, history[1].(map[string]any), "total_amount") != test.total {
			t.Fatal("rate correction rewrote its original history")
		}
	}
}
