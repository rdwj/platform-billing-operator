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
	"fmt"
	"net/http"
	"sync"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// PaymentState represents the lifecycle state of a payment intent.
type PaymentState string

const (
	PaymentStateSucceeded PaymentState = "Succeeded"
	PaymentStateFailed    PaymentState = "Failed"
	PaymentStatePending   PaymentState = "Pending"
)

// PaymentStatus holds the outcome of a payment intent query.
// Amount is a decimal string, never a float.
type PaymentStatus struct {
	State    PaymentState
	Amount   string
	Metadata map[string]string
}

// PSPBackend abstracts a payment service provider (Stripe, Adyen, etc.).
// Implementations handle payment intent creation, webhook verification, and
// status queries. The operator code programs against this interface so the
// concrete PSP can be swapped without touching reconciliation logic.
type PSPBackend interface {
	// CreatePaymentIntent initiates a payment for the given tenant account.
	// amount is a decimal string. Returns the provider's intent ID.
	CreatePaymentIntent(ctx context.Context, acct *billingv1alpha1.TenantAccount, amount string) (string, error)

	// VerifyWebhookSignature validates that an incoming webhook request
	// originated from the payment provider.
	VerifyWebhookSignature(req *http.Request) error

	// GetPaymentStatus retrieves the current state of a previously created
	// payment intent.
	GetPaymentStatus(ctx context.Context, intentID string) (PaymentStatus, error)
}

// ---------------------------------------------------------------------------
// RecordingBackend -- test double
// ---------------------------------------------------------------------------

// RecordedIntent captures one CreatePaymentIntent call.
type RecordedIntent struct {
	TenantName string
	Amount     string
	ReturnedID string
}

// RecordedStatusCheck captures one GetPaymentStatus call.
type RecordedStatusCheck struct {
	IntentID string
	Returned PaymentStatus
}

// RecordingBackend is a PSPBackend test double that records every call for
// assertion. Modelled after enforcement.NoopBackend but keeps a full call log.
type RecordingBackend struct {
	mu              sync.Mutex
	Intents         []RecordedIntent
	SignatureChecks int
	StatusChecks    []RecordedStatusCheck

	// Configurable response functions for tests. When nil the backend uses
	// sensible defaults (sequential IDs, Succeeded status, nil error).
	IntentIDFunc func() string
	StatusFunc   func(intentID string) PaymentStatus
	VerifyFunc   func(req *http.Request) error

	// intentCounter provides default sequential IDs ("intent-1", "intent-2", ...).
	intentCounter int
	// lastAmount stores the amount from the most recent CreatePaymentIntent for
	// use in the default StatusFunc path.
	lastAmount string
}

var _ PSPBackend = (*RecordingBackend)(nil)

// CreatePaymentIntent records the call and returns an intent ID from
// IntentIDFunc, or a sequential default ("intent-1", "intent-2", ...).
func (r *RecordingBackend) CreatePaymentIntent(_ context.Context, acct *billingv1alpha1.TenantAccount, amount string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.intentCounter++
	id := fmt.Sprintf("intent-%d", r.intentCounter)
	if r.IntentIDFunc != nil {
		id = r.IntentIDFunc()
	}

	r.lastAmount = amount
	r.Intents = append(r.Intents, RecordedIntent{
		TenantName: acct.Name,
		Amount:     amount,
		ReturnedID: id,
	})
	return id, nil
}

// VerifyWebhookSignature increments the counter and delegates to VerifyFunc
// when set; otherwise returns nil.
func (r *RecordingBackend) VerifyWebhookSignature(req *http.Request) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.SignatureChecks++
	if r.VerifyFunc != nil {
		return r.VerifyFunc(req)
	}
	return nil
}

// GetPaymentStatus delegates to StatusFunc when set; otherwise returns
// Succeeded with the amount from the most recent CreatePaymentIntent call.
func (r *RecordingBackend) GetPaymentStatus(_ context.Context, intentID string) (PaymentStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var status PaymentStatus
	if r.StatusFunc != nil {
		status = r.StatusFunc(intentID)
	} else {
		status = PaymentStatus{
			State:  PaymentStateSucceeded,
			Amount: r.lastAmount,
		}
	}

	r.StatusChecks = append(r.StatusChecks, RecordedStatusCheck{
		IntentID: intentID,
		Returned: status,
	})
	return status, nil
}

// GetIntents returns a copy of the recorded intents, safe for concurrent use.
func (r *RecordingBackend) GetIntents() []RecordedIntent {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]RecordedIntent, len(r.Intents))
	copy(result, r.Intents)
	return result
}
