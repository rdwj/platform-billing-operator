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
	"fmt"
	"io"
	"net/http"
	"sync"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

var _ PSPBackend = (*WebhookBackend)(nil)

// WebhookBackend implements PSPBackend for a generic "bring your own biller"
// path using HMAC-SHA256 webhook verification.
type WebhookBackend struct {
	hmacSecret []byte
	mu         sync.RWMutex
	intents    map[string]PaymentStatus
	counter    int64
}

// NewWebhookBackend creates a WebhookBackend with the given HMAC secret.
func NewWebhookBackend(hmacSecret string) *WebhookBackend {
	return &WebhookBackend{
		hmacSecret: []byte(hmacSecret),
		intents:    make(map[string]PaymentStatus),
	}
}

// CreatePaymentIntent generates a local intent ID and stores it as Pending.
func (w *WebhookBackend) CreatePaymentIntent(_ context.Context, _ *billingv1alpha1.TenantAccount, amount string) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.counter++
	id := fmt.Sprintf("wh-intent-%d", w.counter)
	w.intents[id] = PaymentStatus{
		State:  PaymentStatePending,
		Amount: amount,
	}
	return id, nil
}

// VerifyWebhookSignature validates an incoming webhook request using
// HMAC-SHA256 over the raw body. On success it parses the JSON payload and
// updates the stored intent status.
func (w *WebhookBackend) VerifyWebhookSignature(req *http.Request) error {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("VerifyWebhookSignature: read body: %w", err)
	}
	// Restore the body for downstream consumption.
	req.Body = io.NopCloser(bytes.NewReader(body))

	sig := req.Header.Get("X-Webhook-Signature")
	if sig == "" {
		return fmt.Errorf("VerifyWebhookSignature: missing X-Webhook-Signature header")
	}

	mac := hmac.New(sha256.New, w.hmacSecret)
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return fmt.Errorf("VerifyWebhookSignature: signature mismatch")
	}

	var payload struct {
		Event    string `json:"event"`
		IntentID string `json:"intent_id"`
		Amount   string `json:"amount"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("VerifyWebhookSignature: decode payload: %w", err)
	}

	state := mapWebhookEvent(payload.Event)

	if payload.IntentID != "" {
		w.mu.Lock()
		w.intents[payload.IntentID] = PaymentStatus{
			State:  state,
			Amount: payload.Amount,
		}
		w.mu.Unlock()
	}

	return nil
}

// GetPaymentStatus retrieves the stored state of a previously created intent.
func (w *WebhookBackend) GetPaymentStatus(_ context.Context, intentID string) (PaymentStatus, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	status, ok := w.intents[intentID]
	if !ok {
		return PaymentStatus{}, fmt.Errorf("GetPaymentStatus: unknown intent %q", intentID)
	}
	return status, nil
}

// mapWebhookEvent converts a webhook event string to PaymentState.
func mapWebhookEvent(event string) PaymentState {
	switch event {
	case "payment.succeeded":
		return PaymentStateSucceeded
	case "payment.failed":
		return PaymentStateFailed
	default:
		return PaymentStatePending
	}
}
