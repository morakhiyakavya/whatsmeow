package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestSignedQuantityValidation(t *testing.T) {
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
		{"-2.5", "-250", "", false},
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
}
