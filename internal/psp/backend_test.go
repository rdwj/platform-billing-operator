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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
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
// Contract test suite — any PSPBackend implementation must pass these.
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
// RecordingBackend — must pass all contract tests
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
// StripeBackend (stub) — must compile and satisfy the interface, but all
// methods return ErrNotImplemented.
// ---------------------------------------------------------------------------

func TestStripeBackend_Compiles(t *testing.T) {
	var _ PSPBackend = (*StripeBackend)(nil)
}

func TestStripeBackend_ReturnsNotImplemented(t *testing.T) {
	b := &StripeBackend{}

	_, err := b.CreatePaymentIntent(context.Background(), testTenant(), "10.000000")
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("CreatePaymentIntent: expected ErrNotImplemented, got %v", err)
	}

	err = b.VerifyWebhookSignature(httptest.NewRequest(http.MethodPost, "/", nil))
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("VerifyWebhookSignature: expected ErrNotImplemented, got %v", err)
	}

	_, err = b.GetPaymentStatus(context.Background(), "intent-1")
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("GetPaymentStatus: expected ErrNotImplemented, got %v", err)
	}
}
