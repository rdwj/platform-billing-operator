/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package fakestripe_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	fakestripe "github.com/rdwj/platform-billing-operator/test/fake-stripe"
)

func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(fakestripe.NewMux())
	t.Cleanup(srv.Close)
	return srv
}

func postForm(t *testing.T, u string, data url.Values) *http.Response {
	t.Helper()
	resp, err := http.Post(u, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		t.Fatalf("POST failed: %v", err)
	}
	return resp
}

func decodeJSON(t *testing.T, body io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}
}

func TestCreatePaymentIntent(t *testing.T) {
	srv := startServer(t)

	resp := postForm(t, srv.URL+"/v1/payment_intents", url.Values{
		"amount":   {"5000"},
		"currency": {"usd"},
		"customer": {"cus_test"},
		"confirm":  {"true"},
	})
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	var pi fakestripe.PaymentIntent
	decodeJSON(t, resp.Body, &pi)

	if pi.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if pi.Object != "payment_intent" {
		t.Errorf("object = %q, want %q", pi.Object, "payment_intent")
	}
	if pi.Amount != 5000 {
		t.Errorf("amount = %d, want 5000", pi.Amount)
	}
	if pi.Currency != "usd" {
		t.Errorf("currency = %q, want %q", pi.Currency, "usd")
	}
	if pi.Status != "succeeded" {
		t.Errorf("status = %q, want %q (confirm=true)", pi.Status, "succeeded")
	}
	if pi.Customer != "cus_test" {
		t.Errorf("customer = %q, want %q", pi.Customer, "cus_test")
	}
	if pi.ClientSecret == "" {
		t.Error("expected non-empty client_secret")
	}
	if pi.Created == 0 {
		t.Error("expected non-zero created timestamp")
	}
}

func TestCreatePaymentIntent_WithoutConfirm(t *testing.T) {
	srv := startServer(t)

	resp := postForm(t, srv.URL+"/v1/payment_intents", url.Values{
		"amount":   {"1000"},
		"currency": {"eur"},
	})
	defer func() { _ = resp.Body.Close() }()

	var pi fakestripe.PaymentIntent
	decodeJSON(t, resp.Body, &pi)

	if pi.Status != "requires_confirmation" {
		t.Errorf("status = %q, want %q (no confirm)", pi.Status, "requires_confirmation")
	}
}

func TestCreatePaymentIntent_MissingAmount(t *testing.T) {
	srv := startServer(t)

	resp := postForm(t, srv.URL+"/v1/payment_intents", url.Values{
		"currency": {"usd"},
	})
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestGetPaymentIntent(t *testing.T) {
	srv := startServer(t)

	// Create an intent first.
	resp := postForm(t, srv.URL+"/v1/payment_intents", url.Values{
		"amount":   {"2500"},
		"currency": {"usd"},
		"confirm":  {"true"},
	})
	var created fakestripe.PaymentIntent
	decodeJSON(t, resp.Body, &created)
	_ = resp.Body.Close()

	// Retrieve it.
	getResp, err := http.Get(srv.URL + "/v1/payment_intents/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = getResp.Body.Close() }()

	if getResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(getResp.Body)
		t.Fatalf("GET status = %d, body = %s", getResp.StatusCode, body)
	}

	var retrieved fakestripe.PaymentIntent
	decodeJSON(t, getResp.Body, &retrieved)

	if retrieved.ID != created.ID {
		t.Errorf("retrieved ID = %q, want %q", retrieved.ID, created.ID)
	}
	if retrieved.Amount != created.Amount {
		t.Errorf("retrieved amount = %d, want %d", retrieved.Amount, created.Amount)
	}
}

func TestGetPaymentIntent_NotFound(t *testing.T) {
	srv := startServer(t)

	resp, err := http.Get(srv.URL + "/v1/payment_intents/pi_nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUniqueIDs(t *testing.T) {
	srv := startServer(t)

	var ids []string
	for i := 0; i < 5; i++ {
		resp := postForm(t, srv.URL+"/v1/payment_intents", url.Values{
			"amount": {"100"},
		})
		var pi fakestripe.PaymentIntent
		decodeJSON(t, resp.Body, &pi)
		_ = resp.Body.Close()
		ids = append(ids, pi.ID)
	}

	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate ID: %q", id)
		}
		seen[id] = true
	}
}
