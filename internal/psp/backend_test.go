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
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	stripe "github.com/stripe/stripe-go/v82"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	fakestripe "github.com/rdwj/platform-billing-operator/test/fake-stripe"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testTenant() *billingv1alpha1.TenantAccount {
	return &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "test-tenant", Namespace: "default"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Test Tenant",
			BillingMode: billingv1alpha1.BillingModePrepaid,
		},
	}
}

// ---------------------------------------------------------------------------
// Contract test suite -- any PSPBackend implementation must pass these.
// ---------------------------------------------------------------------------

type contractTestCase struct {
	name string
	fn   func(t *testing.T, b PSPBackend)
}

var contractTests = []contractTestCase{
	{"CreatePaymentIntent returns valid ID", testCreateIntentReturnsID},
	{"CreatePaymentIntent returns non-empty ID", testCreateIntentNonEmptyID},
	{"GetPaymentStatus returns consistent state", testGetStatusConsistent},
	{"VerifyWebhookSignature accepts valid request", testVerifyAcceptsValid},
}

func testCreateIntentReturnsID(t *testing.T, b PSPBackend) {
	t.Helper()
	id, err := b.CreatePaymentIntent(context.Background(), testTenant(), "50.000000")
	if err != nil {
		t.Fatalf("CreatePaymentIntent returned error: %v", err)
	}
	if id == "" {
		t.Fatal("CreatePaymentIntent returned empty ID")
	}
}

func testCreateIntentNonEmptyID(t *testing.T, b PSPBackend) {
	t.Helper()
	id1, err := b.CreatePaymentIntent(context.Background(), testTenant(), "25.000000")
	if err != nil {
		t.Fatalf("first CreatePaymentIntent: %v", err)
	}
	id2, err := b.CreatePaymentIntent(context.Background(), testTenant(), "75.000000")
	if err != nil {
		t.Fatalf("second CreatePaymentIntent: %v", err)
	}
	if id1 == id2 {
		t.Fatalf("two calls returned the same ID: %q", id1)
	}
}

func testGetStatusConsistent(t *testing.T, b PSPBackend) {
	t.Helper()
	id, err := b.CreatePaymentIntent(context.Background(), testTenant(), "100.000000")
	if err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}
	status, err := b.GetPaymentStatus(context.Background(), id)
	if err != nil {
		t.Fatalf("GetPaymentStatus: %v", err)
	}
	switch status.State {
	case PaymentStateSucceeded, PaymentStateFailed, PaymentStatePending:
		// valid
	default:
		t.Fatalf("unknown payment state: %q", status.State)
	}
}

func testVerifyAcceptsValid(t *testing.T, b PSPBackend) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	err := b.VerifyWebhookSignature(req)
	if err != nil {
		t.Fatalf("VerifyWebhookSignature rejected valid request: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RecordingBackend -- must pass all contract tests
// ---------------------------------------------------------------------------

func TestRecordingBackend_Contract(t *testing.T) {
	for _, tc := range contractTests {
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, &RecordingBackend{})
		})
	}
}

func TestRecordingBackend_RecordsIntents(t *testing.T) {
	b := &RecordingBackend{}
	tenant := testTenant()

	_, _ = b.CreatePaymentIntent(context.Background(), tenant, "50.000000")
	_, _ = b.CreatePaymentIntent(context.Background(), tenant, "75.000000")

	if len(b.Intents) != 2 {
		t.Fatalf("expected 2 recorded intents, got %d", len(b.Intents))
	}
	if b.Intents[0].Amount != "50.000000" {
		t.Errorf("intent[0] amount = %q, want %q", b.Intents[0].Amount, "50.000000")
	}
	if b.Intents[1].Amount != "75.000000" {
		t.Errorf("intent[1] amount = %q, want %q", b.Intents[1].Amount, "75.000000")
	}
	if b.Intents[0].TenantName != "test-tenant" {
		t.Errorf("intent[0] tenant = %q, want %q", b.Intents[0].TenantName, "test-tenant")
	}
}

func TestRecordingBackend_SequentialIDs(t *testing.T) {
	b := &RecordingBackend{}
	tenant := testTenant()

	id1, _ := b.CreatePaymentIntent(context.Background(), tenant, "10.000000")
	id2, _ := b.CreatePaymentIntent(context.Background(), tenant, "20.000000")

	if id1 != "intent-1" {
		t.Errorf("first ID = %q, want %q", id1, "intent-1")
	}
	if id2 != "intent-2" {
		t.Errorf("second ID = %q, want %q", id2, "intent-2")
	}
}

func TestRecordingBackend_CustomIntentIDFunc(t *testing.T) {
	b := &RecordingBackend{
		IntentIDFunc: func() string { return "custom-id" },
	}
	id, _ := b.CreatePaymentIntent(context.Background(), testTenant(), "10.000000")
	if id != "custom-id" {
		t.Errorf("ID = %q, want %q", id, "custom-id")
	}
}

func TestRecordingBackend_VerifyRejectsWithFunc(t *testing.T) {
	wantErr := errors.New("bad signature")
	b := &RecordingBackend{
		VerifyFunc: func(_ *http.Request) error { return wantErr },
	}
	req := httptest.NewRequest(http.MethodPost, "/webhook", nil)
	err := b.VerifyWebhookSignature(req)
	if !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if b.SignatureChecks != 1 {
		t.Errorf("signature checks = %d, want 1", b.SignatureChecks)
	}
}

func TestRecordingBackend_StatusDefaultsToLastAmount(t *testing.T) {
	b := &RecordingBackend{}
	tenant := testTenant()

	_, _ = b.CreatePaymentIntent(context.Background(), tenant, "42.500000")
	status, err := b.GetPaymentStatus(context.Background(), "intent-1")
	if err != nil {
		t.Fatalf("GetPaymentStatus: %v", err)
	}
	if status.State != PaymentStateSucceeded {
		t.Errorf("state = %q, want %q", status.State, PaymentStateSucceeded)
	}
	if status.Amount != "42.500000" {
		t.Errorf("amount = %q, want %q", status.Amount, "42.500000")
	}
	if len(b.StatusChecks) != 1 {
		t.Errorf("status checks = %d, want 1", len(b.StatusChecks))
	}
}

func TestRecordingBackend_CustomStatusFunc(t *testing.T) {
	b := &RecordingBackend{
		StatusFunc: func(_ string) PaymentStatus {
			return PaymentStatus{State: PaymentStateFailed, Amount: "0.000000"}
		},
	}
	tenant := testTenant()
	_, _ = b.CreatePaymentIntent(context.Background(), tenant, "10.000000")

	status, _ := b.GetPaymentStatus(context.Background(), "intent-1")
	if status.State != PaymentStateFailed {
		t.Errorf("state = %q, want %q", status.State, PaymentStateFailed)
	}
}

// ---------------------------------------------------------------------------
// StripeBackend -- contract tests (skipping bare-request webhook verify)
// ---------------------------------------------------------------------------

func newTestStripeBackend(t *testing.T) (*StripeBackend, *httptest.Server) {
	t.Helper()
	mux := fakestripe.NewMux()
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	backend := NewStripeBackend("sk_test_fake", "whsec_test_fake",
		WithBackendURL(ts.URL),
	)
	return backend, ts
}

func TestStripeBackend_Contract(t *testing.T) {
	backend, _ := newTestStripeBackend(t)

	for _, tc := range contractTests {
		// The bare-request webhook verify test sends an unsigned request;
		// StripeBackend correctly rejects that. Stripe-specific signature
		// tests are below.
		if tc.name == "VerifyWebhookSignature accepts valid request" {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			tc.fn(t, backend)
		})
	}
}

// ---------------------------------------------------------------------------
// Stripe-specific tests: cents conversion, webhook signatures, body restore
// ---------------------------------------------------------------------------

func TestStripeBackend_CentsConversion(t *testing.T) {
	cases := []struct {
		dollars   string
		wantCents int64
		wantBack  string
	}{
		{"19.99", 1999, "19.990000"},
		{"0.01", 1, "0.010000"},
		{"100.00", 10000, "100.000000"},
		{"0.50", 50, "0.500000"},
		{"999.999999", 99999, "999.990000"}, // truncation at sub-cent
	}
	for _, tc := range cases {
		t.Run(tc.dollars, func(t *testing.T) {
			cents, err := dollarsToCents(tc.dollars)
			if err != nil {
				t.Fatalf("dollarsToCents(%q): %v", tc.dollars, err)
			}
			if cents != tc.wantCents {
				t.Errorf("dollarsToCents(%q) = %d, want %d", tc.dollars, cents, tc.wantCents)
			}
			back := centsToDollars(cents)
			if back != tc.wantBack {
				t.Errorf("centsToDollars(%d) = %q, want %q", cents, back, tc.wantBack)
			}
		})
	}
}

func TestStripeBackend_RoundTrip(t *testing.T) {
	backend, _ := newTestStripeBackend(t)
	tenant := testTenant()

	// Create a $19.99 payment.
	id, err := backend.CreatePaymentIntent(context.Background(), tenant, "19.990000")
	if err != nil {
		t.Fatalf("CreatePaymentIntent: %v", err)
	}

	// Verify the amount round-trips through the fake server.
	status, err := backend.GetPaymentStatus(context.Background(), id)
	if err != nil {
		t.Fatalf("GetPaymentStatus: %v", err)
	}
	if status.Amount != "19.990000" {
		t.Errorf("round-trip amount = %q, want %q", status.Amount, "19.990000")
	}
	if status.State != PaymentStateSucceeded {
		t.Errorf("state = %q, want Succeeded", status.State)
	}
}

// signWebhookPayload produces a valid Stripe-Signature header for the given
// body using the stripe-go library's test helper.
func signWebhookPayload(t *testing.T, body []byte, secret string) string {
	t.Helper()
	signed := stripe.GenerateTestSignedPayload(&stripe.UnsignedPayload{
		Payload: body,
		Secret:  secret,
	})
	return signed.Header
}

func TestStripeBackend_VerifySignature_Valid(t *testing.T) {
	backend := NewStripeBackend("sk_test_fake", "whsec_test_secret")

	body := []byte(`{"id":"evt_test","type":"payment_intent.succeeded"}`)
	header := signWebhookPayload(t, body, "whsec_test_secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)

	if err := backend.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("VerifyWebhookSignature rejected valid signature: %v", err)
	}
}

func TestStripeBackend_VerifySignature_Invalid(t *testing.T) {
	backend := NewStripeBackend("sk_test_fake", "whsec_test_secret")

	body := []byte(`{"id":"evt_test","type":"payment_intent.succeeded"}`)
	// Sign with a different secret.
	header := signWebhookPayload(t, body, "whsec_wrong_secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)

	err := backend.VerifyWebhookSignature(req)
	if err == nil {
		t.Fatal("VerifyWebhookSignature accepted invalid signature")
	}
}

func TestStripeBackend_VerifySignature_MissingHeader(t *testing.T) {
	backend := NewStripeBackend("sk_test_fake", "whsec_test_secret")

	body := []byte(`{"id":"evt_test"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	// No Stripe-Signature header set.

	err := backend.VerifyWebhookSignature(req)
	if err == nil {
		t.Fatal("VerifyWebhookSignature accepted request with no signature header")
	}
}

func TestStripeBackend_BodyPreservedAfterVerify(t *testing.T) {
	backend := NewStripeBackend("sk_test_fake", "whsec_test_secret")

	payload := map[string]any{
		"id":   "evt_test",
		"type": "payment_intent.succeeded",
		"data": map[string]any{"amount": 5000},
	}
	body, _ := json.Marshal(payload)
	header := signWebhookPayload(t, body, "whsec_test_secret")

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)

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

	// Verify it's valid JSON that can be decoded.
	var decoded map[string]any
	if err := json.Unmarshal(restored, &decoded); err != nil {
		t.Fatalf("JSON decode after verify: %v", err)
	}
	if decoded["id"] != "evt_test" {
		t.Errorf("decoded id = %v, want %q", decoded["id"], "evt_test")
	}
}

func TestStripeBackend_VerifySignature_ManualHMAC(t *testing.T) {
	// Verify the signature mechanism directly (no stripe-go helper on the
	// signing side) to confirm our understanding of the protocol.
	secret := "whsec_manual_test"
	backend := NewStripeBackend("sk_test_fake", secret)

	body := []byte(`{"id":"evt_manual"}`)
	now := time.Now()

	sig := stripe.ComputeSignature(now, body, secret)
	header := fmt.Sprintf("t=%d,v1=%s", now.Unix(), hex.EncodeToString(sig))

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("Stripe-Signature", header)

	if err := backend.VerifyWebhookSignature(req); err != nil {
		t.Fatalf("manually signed payload rejected: %v", err)
	}
}

func TestStripeBackend_StatusMapping(t *testing.T) {
	cases := []struct {
		stripe stripe.PaymentIntentStatus
		want   PaymentState
	}{
		{stripe.PaymentIntentStatusSucceeded, PaymentStateSucceeded},
		{stripe.PaymentIntentStatusCanceled, PaymentStateFailed},
		{stripe.PaymentIntentStatusRequiresPaymentMethod, PaymentStateFailed},
		{stripe.PaymentIntentStatusProcessing, PaymentStatePending},
		{stripe.PaymentIntentStatusRequiresAction, PaymentStatePending},
		{stripe.PaymentIntentStatusRequiresConfirmation, PaymentStatePending},
		{stripe.PaymentIntentStatusRequiresCapture, PaymentStatePending},
	}
	for _, tc := range cases {
		t.Run(string(tc.stripe), func(t *testing.T) {
			got := mapStripeStatus(tc.stripe)
			if got != tc.want {
				t.Errorf("mapStripeStatus(%q) = %q, want %q", tc.stripe, got, tc.want)
			}
		})
	}
}
