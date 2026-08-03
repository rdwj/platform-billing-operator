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

package psp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHandler(backend *RecordingBackend, ledger *StubLedgerClient, k8s *RecordingK8sUpdater) *WebhookHandler {
	return &WebhookHandler{
		Backend: backend,
		Ledger:  ledger,
		K8s:     k8s,
	}
}

func postWebhook(handler http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestWebhookHandler_MethodNotAllowed(t *testing.T) {
	h := newTestHandler(&RecordingBackend{}, &StubLedgerClient{}, &RecordingK8sUpdater{})
	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestWebhookHandler_SignatureRejection(t *testing.T) {
	backend := &RecordingBackend{
		VerifyFunc: func(_ *http.Request) error { return errors.New("forged") },
	}
	h := newTestHandler(backend, &StubLedgerClient{}, &RecordingK8sUpdater{})
	w := postWebhook(h, `{"event":"payment.succeeded","intent_id":"pi_1","amount":"50.000000","tenant_name":"t1"}`)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestWebhookHandler_InvalidJSON(t *testing.T) {
	h := newTestHandler(&RecordingBackend{}, &StubLedgerClient{}, &RecordingK8sUpdater{})
	w := postWebhook(h, `not json`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWebhookHandler_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing event", `{"intent_id":"pi_1","tenant_name":"t1"}`},
		{"missing intent_id", `{"event":"payment.succeeded","tenant_name":"t1"}`},
		{"missing tenant_name", `{"event":"payment.succeeded","intent_id":"pi_1"}`},
		{"missing amount for succeeded", `{"event":"payment.succeeded","intent_id":"pi_1","tenant_name":"t1"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHandler(&RecordingBackend{}, &StubLedgerClient{}, &RecordingK8sUpdater{})
			w := postWebhook(h, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestWebhookHandler_UnknownEvent(t *testing.T) {
	h := newTestHandler(&RecordingBackend{}, &StubLedgerClient{}, &RecordingK8sUpdater{})
	w := postWebhook(h, `{"event":"payment.refunded","intent_id":"pi_1","tenant_name":"t1"}`)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestWebhookHandler_PaymentSucceeded(t *testing.T) {
	backend := &RecordingBackend{}
	backend.CreatePaymentIntent(nil, testTenant(), "50.000000")
	ledger := &StubLedgerClient{}
	k8s := &RecordingK8sUpdater{}
	h := newTestHandler(backend, ledger, k8s)

	w := postWebhook(h, `{"event":"payment.succeeded","intent_id":"intent-1","amount":"50.000000","tenant_name":"t1"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	credits := ledger.GetCredits()
	if len(credits) != 1 {
		t.Fatalf("credits = %d, want 1", len(credits))
	}
	if credits[0].Tenant != "t1" {
		t.Errorf("credit tenant = %q, want %q", credits[0].Tenant, "t1")
	}
	if credits[0].Amount != "50.000000" {
		t.Errorf("credit amount = %q, want %q", credits[0].Amount, "50.000000")
	}
	if credits[0].IntentID != "intent-1" {
		t.Errorf("credit intentID = %q, want %q", credits[0].IntentID, "intent-1")
	}

	patches := k8s.GetPatches()
	if len(patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(patches))
	}
	if patches[0].Type != "succeeded" {
		t.Errorf("patch type = %q, want %q", patches[0].Type, "succeeded")
	}
}

func TestWebhookHandler_CorroborationMismatch(t *testing.T) {
	backend := &RecordingBackend{
		StatusFunc: func(_ string) PaymentStatus {
			return PaymentStatus{State: PaymentStateSucceeded, Amount: "75.000000"}
		},
	}
	ledger := &StubLedgerClient{}
	k8s := &RecordingK8sUpdater{}
	h := newTestHandler(backend, ledger, k8s)

	w := postWebhook(h, `{"event":"payment.succeeded","intent_id":"pi_1","amount":"50.000000","tenant_name":"t1"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (acknowledge receipt even on mismatch)", w.Code, http.StatusOK)
	}

	credits := ledger.GetCredits()
	if len(credits) != 0 {
		t.Fatalf("wallet should NOT be credited on mismatch, got %d credits", len(credits))
	}

	patches := k8s.GetPatches()
	if len(patches) != 1 {
		t.Fatalf("patches = %d, want 1 (corroboration_failed)", len(patches))
	}
	if patches[0].Type != "corroboration_failed" {
		t.Errorf("patch type = %q, want %q", patches[0].Type, "corroboration_failed")
	}
	if patches[0].Tenant != "t1" {
		t.Errorf("patch tenant = %q, want %q", patches[0].Tenant, "t1")
	}
	if patches[0].IntentID != "pi_1" {
		t.Errorf("patch intentID = %q, want %q", patches[0].IntentID, "pi_1")
	}
}

func TestWebhookHandler_PaymentFailed(t *testing.T) {
	k8s := &RecordingK8sUpdater{}
	h := newTestHandler(&RecordingBackend{}, &StubLedgerClient{}, k8s)

	w := postWebhook(h, `{"event":"payment.failed","intent_id":"pi_1","tenant_name":"t1"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	patches := k8s.GetPatches()
	if len(patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(patches))
	}
	if patches[0].Type != "failed" {
		t.Errorf("patch type = %q, want %q", patches[0].Type, "failed")
	}
	if patches[0].Tenant != "t1" {
		t.Errorf("patch tenant = %q, want %q", patches[0].Tenant, "t1")
	}
}

func TestWebhookHandler_PaymentDisputed(t *testing.T) {
	k8s := &RecordingK8sUpdater{}
	h := newTestHandler(&RecordingBackend{}, &StubLedgerClient{}, k8s)

	w := postWebhook(h, `{"event":"payment.disputed","intent_id":"pi_1","tenant_name":"t1"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	patches := k8s.GetPatches()
	if len(patches) != 1 {
		t.Fatalf("patches = %d, want 1", len(patches))
	}
	if patches[0].Type != "disputed" {
		t.Errorf("patch type = %q, want %q", patches[0].Type, "disputed")
	}
	if patches[0].IntentID != "pi_1" {
		t.Errorf("patch intentID = %q, want %q", patches[0].IntentID, "pi_1")
	}
}

func TestWebhookHandler_IdempotentReplay(t *testing.T) {
	backend := &RecordingBackend{}
	backend.CreatePaymentIntent(nil, testTenant(), "50.000000")
	ledger := &StubLedgerClient{}
	k8s := &RecordingK8sUpdater{}
	h := newTestHandler(backend, ledger, k8s)

	body := `{"event":"payment.succeeded","intent_id":"intent-1","amount":"50.000000","tenant_name":"t1"}`
	w1 := postWebhook(h, body)
	w2 := postWebhook(h, body)

	if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
		t.Fatalf("statuses = %d, %d; want both %d", w1.Code, w2.Code, http.StatusOK)
	}

	// Both calls go through — idempotency is enforced by the ledger's
	// idempotency_key on the adjustment, not by the handler. The handler
	// calls CreditWallet both times with the same intentID, and the ledger
	// deduplicates.
	credits := ledger.GetCredits()
	if len(credits) != 2 {
		t.Fatalf("credits = %d, want 2 (handler does not deduplicate; ledger does)", len(credits))
	}
	if credits[0].IntentID != credits[1].IntentID {
		t.Errorf("both credits should use the same intentID for ledger dedup: %q vs %q",
			credits[0].IntentID, credits[1].IntentID)
	}
}

func TestWebhookHandler_LedgerError(t *testing.T) {
	backend := &RecordingBackend{}
	backend.CreatePaymentIntent(nil, testTenant(), "50.000000")
	ledger := &StubLedgerClient{Err: errors.New("connection refused")}
	h := newTestHandler(backend, ledger, &RecordingK8sUpdater{})

	w := postWebhook(h, `{"event":"payment.succeeded","intent_id":"intent-1","amount":"50.000000","tenant_name":"t1"}`)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
}
