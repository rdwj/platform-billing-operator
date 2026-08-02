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

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

var ErrNotImplemented = errors.New("stripe backend not yet implemented")

// StripeBackend implements PSPBackend against the Stripe API.
// Phase 1 stub — all methods return ErrNotImplemented. The real
// implementation arrives in Phase 4 when the stripe-go SDK is added.
type StripeBackend struct{}

var _ PSPBackend = (*StripeBackend)(nil)

func (s *StripeBackend) CreatePaymentIntent(_ context.Context, _ *billingv1alpha1.TenantAccount, _ string) (string, error) {
	return "", ErrNotImplemented
}

func (s *StripeBackend) VerifyWebhookSignature(_ *http.Request) error {
	return ErrNotImplemented
}

func (s *StripeBackend) GetPaymentStatus(_ context.Context, _ string) (PaymentStatus, error) {
	return PaymentStatus{}, ErrNotImplemented
}
