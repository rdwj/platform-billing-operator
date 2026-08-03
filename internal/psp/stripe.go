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
	"fmt"
	"io"
	"net/http"

	"github.com/shopspring/decimal"
	stripe "github.com/stripe/stripe-go/v82"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// hundred is the multiplier/divisor for dollar <-> cents conversion.
// Kept as a package-level constant to avoid repeated allocation.
var hundred = decimal.NewFromInt(100)

// StripeBackend implements PSPBackend against the Stripe API using the
// stripe-go SDK. It avoids global state by constructing a per-instance
// stripe.Client with custom backends, making it safe for parallel tests.
type StripeBackend struct {
	apiKey        string
	webhookSecret string
	httpClient    *http.Client
	backendURL    string // override URL for testing (empty = Stripe default)
}

var _ PSPBackend = (*StripeBackend)(nil)

// NewStripeBackend creates a StripeBackend with the given API key and webhook
// signing secret. Use functional options (WithHTTPClient, WithBackendURL) to
// configure test overrides.
func NewStripeBackend(apiKey, webhookSecret string, opts ...func(*StripeBackend)) *StripeBackend {
	s := &StripeBackend{
		apiKey:        apiKey,
		webhookSecret: webhookSecret,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// WithHTTPClient overrides the HTTP client used for Stripe API calls.
func WithHTTPClient(c *http.Client) func(*StripeBackend) {
	return func(s *StripeBackend) { s.httpClient = c }
}

// WithBackendURL overrides the Stripe API base URL (for pointing at fake-stripe).
func WithBackendURL(url string) func(*StripeBackend) {
	return func(s *StripeBackend) { s.backendURL = url }
}

// client returns a stripe.Client configured for this backend instance.
// When backendURL is set, it points at the fake server instead of Stripe.
func (s *StripeBackend) client() *stripe.Client {
	if s.backendURL != "" {
		cfg := &stripe.BackendConfig{
			URL: stripe.String(s.backendURL),
		}
		if s.httpClient != nil {
			cfg.HTTPClient = s.httpClient
		}
		backends := stripe.NewBackendsWithConfig(cfg)
		return stripe.NewClient(s.apiKey, stripe.WithBackends(backends))
	}
	return stripe.NewClient(s.apiKey)
}

// dollarsToCents converts a decimal dollar string (e.g. "50.000000") to cents
// (e.g. 5000) using shopspring/decimal. Never uses float.
func dollarsToCents(amount string) (int64, error) {
	d, err := decimal.NewFromString(amount)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q: %w", amount, err)
	}
	return d.Mul(hundred).IntPart(), nil
}

// centsToDollars converts cents (int64) to a 6-decimal-place dollar string.
func centsToDollars(cents int64) string {
	return decimal.NewFromInt(cents).Div(hundred).StringFixed(6)
}

// CreatePaymentIntent initiates a confirmed payment for the given tenant
// account. The amount parameter is a decimal dollar string (e.g. "50.000000").
// Returns the Stripe PaymentIntent ID.
//
// For Phase 4 the tenant's Name is used as the customer reference; the
// BillingConnector controller (a later phase) will resolve the real customer
// ID from PSPCustomerSecretRef.
func (s *StripeBackend) CreatePaymentIntent(ctx context.Context, acct *billingv1alpha1.TenantAccount, amount string) (string, error) {
	cents, err := dollarsToCents(amount)
	if err != nil {
		return "", fmt.Errorf("CreatePaymentIntent: %w", err)
	}

	params := &stripe.PaymentIntentCreateParams{
		Amount:   stripe.Int64(cents),
		Currency: stripe.String("usd"),
		Customer: stripe.String(acct.Name),
		Confirm:  stripe.Bool(true),
		// Off-session server-initiated payment (no redirect).
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
	}
	params.Context = ctx
	params.SetIdempotencyKey(fmt.Sprintf("%s-%s", acct.Name, amount))

	pi, err := s.client().V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("CreatePaymentIntent: stripe: %w", err)
	}

	return pi.ID, nil
}

// VerifyWebhookSignature validates that an incoming webhook request was signed
// by Stripe using the configured webhook secret. It reads the full body,
// verifies the signature, then restores the body so downstream handlers can
// still decode JSON from it.
func (s *StripeBackend) VerifyWebhookSignature(req *http.Request) error {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("VerifyWebhookSignature: read body: %w", err)
	}
	// Restore the body for downstream consumption.
	req.Body = io.NopCloser(bytes.NewReader(body))

	sigHeader := req.Header.Get("Stripe-Signature")
	if err := stripe.ValidatePayload(body, sigHeader, s.webhookSecret, stripe.WithIgnoreTolerance()); err != nil {
		return fmt.Errorf("VerifyWebhookSignature: %w", err)
	}

	return nil
}

// GetPaymentStatus retrieves the current state of a previously created
// payment intent. Maps Stripe statuses to the PSP-neutral PaymentState enum.
func (s *StripeBackend) GetPaymentStatus(ctx context.Context, intentID string) (PaymentStatus, error) {
	pi, err := s.client().V1PaymentIntents.Retrieve(ctx, intentID, &stripe.PaymentIntentRetrieveParams{})
	if err != nil {
		return PaymentStatus{}, fmt.Errorf("GetPaymentStatus: stripe: %w", err)
	}

	state := mapStripeStatus(pi.Status)
	return PaymentStatus{
		State:  state,
		Amount: centsToDollars(pi.Amount),
	}, nil
}

// mapStripeStatus converts a Stripe PaymentIntent status string to the
// PSP-neutral PaymentState enum.
func mapStripeStatus(s stripe.PaymentIntentStatus) PaymentState {
	switch s {
	case stripe.PaymentIntentStatusSucceeded:
		return PaymentStateSucceeded
	case stripe.PaymentIntentStatusRequiresPaymentMethod, stripe.PaymentIntentStatusCanceled:
		return PaymentStateFailed
	default:
		// processing, requires_action, requires_confirmation, requires_capture
		return PaymentStatePending
	}
}
