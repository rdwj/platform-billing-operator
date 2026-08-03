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
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// signWebhookBody computes the hex-encoded HMAC-SHA256 of body using secret.
func signWebhookBody(t *testing.T, body []byte, secret string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------------------------------------------------------------------------
// Contract tests -- WebhookBackend must pass the shared contract suite.
// ---------------------------------------------------------------------------

func TestWebhookBackend_Contract(t *testing.T) {
	for _, tc := range contractTests {
		// WebhookBackend correctly rejects unsigned requests, same as StripeBackend.
		if tc.name == "VerifyWebhookSignature accepts valid request" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, NewWebhookBackend("test-hmac-secret"))
		})
	}
}

// ---------------------------------------------------------------------------
// HMAC signature verification
// ---------------------------------------------------------------------------

func TestWebhookBackend_VerifySignature_Valid(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")

	body := []byte(`{"event":"payment.succeeded","intent_id":"wh-intent-1","amount":"50.000000"}`)
	sig := signWebhookBody(t, body, "test-hmac-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", sig)

	if err := backend.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("VerifyWebhookSignature rejected valid signature: %v", err)
	}
}

func TestWebhookBackend_VerifySignature_Invalid(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")

	body := []byte(`{"event":"payment.succeeded","intent_id":"wh-intent-1","amount":"50.000000"}`)
	// Sign with a different secret.
	sig := signWebhookBody(t, body, "wrong-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", sig)

	err := backend.VerifyWebhookSignature(req)
	if err == nil {
		t.Fatal("VerifyWebhookSignature accepted invalid signature")
	}
}

func TestWebhookBackend_VerifySignature_MissingHeader(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")

	body := []byte(`{"event":"payment.succeeded"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	// No X-Webhook-Signature header set.

	err := backend.VerifyWebhookSignature(req)
	if err == nil {
		t.Fatal("VerifyWebhookSignature accepted request with no signature header")
	}
}

// ---------------------------------------------------------------------------
// Body preservation after verification
// ---------------------------------------------------------------------------

func TestWebhookBackend_BodyPreservedAfterVerify(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")

	payload := WebhookEvent{
		Event:      "payment.succeeded",
		IntentID:   "wh-intent-1",
		Amount:     "75.000000",
		TenantName: "test-tenant",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	sig := signWebhookBody(t, body, "test-hmac-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", sig)

	if err := backend.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("VerifyWebhookSignature: %v", err)
	}

	// After verification the body must still be readable.
	restored, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading body after verify: %v", err)
	}
	if !bytes.Equal(restored, body) {
		t.Fatalf("body after verify = %q, want %q", restored, body)
	}

	// Verify it's valid JSON with expected fields.
	var decoded WebhookEvent
	if err := json.Unmarshal(restored, &decoded); err != nil {
		t.Fatalf("JSON decode after verify: %v", err)
	}
	if decoded.IntentID != "wh-intent-1" {
		t.Errorf("decoded intent_id = %q, want %q", decoded.IntentID, "wh-intent-1")
	}
	if decoded.Amount != "75.000000" {
		t.Errorf("decoded amount = %q, want %q", decoded.Amount, "75.000000")
	}
}

// ---------------------------------------------------------------------------
// Status round-trip: create -> webhook -> query
// ---------------------------------------------------------------------------

func TestWebhookBackend_StatusRoundTrip(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")
	ctx := context.Background()

	// Create an intent; it should start as Pending.
	id, err := backend.CreatePaymentIntent(ctx, testTenant(), "100.000000")
	if err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}
	if id == "" {
		t.Fatal("CreatePaymentIntent returned empty ID")
	}

	status, err := backend.GetPaymentStatus(ctx, id)
	if err != nil {
		t.Fatalf("GetPaymentStatus (before webhook): %v", err)
	}
	if status.State != PaymentStatePending {
		t.Fatalf("initial state = %q, want %q", status.State, PaymentStatePending)
	}

	// Send a signed webhook marking the intent as succeeded.
	payload := WebhookEvent{
		Event:      "payment.succeeded",
		IntentID:   id,
		Amount:     "100.000000",
		TenantName: "test-tenant",
	}
	body, _ := json.Marshal(payload)
	sig := signWebhookBody(t, body, "test-hmac-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", sig)

	if err := backend.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("VerifyWebhookSignature: %v", err)
	}

	// The status should now reflect the webhook update.
	status, err = backend.GetPaymentStatus(ctx, id)
	if err != nil {
		t.Fatalf("GetPaymentStatus (after webhook): %v", err)
	}
	if status.State != PaymentStateSucceeded {
		t.Errorf("state after webhook = %q, want %q", status.State, PaymentStateSucceeded)
	}
	if status.Amount != "100.000000" {
		t.Errorf("amount after webhook = %q, want %q", status.Amount, "100.000000")
	}
}

// ---------------------------------------------------------------------------
// Unknown intent returns error
// ---------------------------------------------------------------------------

func TestWebhookBackend_StatusUnknownIntent(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")

	_, err := backend.GetPaymentStatus(context.Background(), "nonexistent-intent")
	if err == nil {
		t.Fatal("GetPaymentStatus accepted unknown intent ID without error")
	}
}

// ---------------------------------------------------------------------------
// Sequential ID generation
// ---------------------------------------------------------------------------

func TestWebhookBackend_SequentialIDs(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")
	ctx := context.Background()
	tenant := testTenant()

	id1, err := backend.CreatePaymentIntent(ctx, tenant, "10.000000")
	if err != nil {
		t.Fatalf("first CreatePaymentIntent: %v", err)
	}
	id2, err := backend.CreatePaymentIntent(ctx, tenant, "20.000000")
	if err != nil {
		t.Fatalf("second CreatePaymentIntent: %v", err)
	}

	if id1 != "wh-intent-1" {
		t.Errorf("first ID = %q, want %q", id1, "wh-intent-1")
	}
	if id2 != "wh-intent-2" {
		t.Errorf("second ID = %q, want %q", id2, "wh-intent-2")
	}
}

// ---------------------------------------------------------------------------
// Failed payment status via webhook
// ---------------------------------------------------------------------------

func TestWebhookBackend_FailedPaymentStatus(t *testing.T) {
	backend := NewWebhookBackend("test-hmac-secret")
	ctx := context.Background()

	id, err := backend.CreatePaymentIntent(ctx, testTenant(), "50.000000")
	if err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}

	// Send a signed webhook marking the intent as failed.
	payload := WebhookEvent{
		Event:      "payment.failed",
		IntentID:   id,
		Amount:     "50.000000",
		TenantName: "test-tenant",
	}
	body, _ := json.Marshal(payload)
	sig := signWebhookBody(t, body, "test-hmac-secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Webhook-Signature", sig)

	if err := backend.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("VerifyWebhookSignature: %v", err)
	}

	status, err := backend.GetPaymentStatus(ctx, id)
	if err != nil {
		t.Fatalf("GetPaymentStatus: %v", err)
	}
	if status.State != PaymentStateFailed {
		t.Errorf("state = %q, want %q", status.State, PaymentStateFailed)
	}
}
