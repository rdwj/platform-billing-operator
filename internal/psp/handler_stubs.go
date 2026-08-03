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
	"sync"
)

// ---------------------------------------------------------------------------
// StubLedgerClient -- test double for LedgerClient
// ---------------------------------------------------------------------------

// CreditRecord captures one CreditWallet call.
type CreditRecord struct {
	Tenant   string
	Amount   string
	IntentID string
}

// StubLedgerClient records CreditWallet calls for assertion.
type StubLedgerClient struct {
	mu      sync.Mutex
	Credits []CreditRecord
	Err     error
}

var _ LedgerClient = (*StubLedgerClient)(nil)

func (s *StubLedgerClient) CreditWallet(_ context.Context, tenant, amount, intentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Credits = append(s.Credits, CreditRecord{
		Tenant:   tenant,
		Amount:   amount,
		IntentID: intentID,
	})
	return s.Err
}

// GetCredits returns a copy of the recorded credits, safe for concurrent use.
func (s *StubLedgerClient) GetCredits() []CreditRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]CreditRecord, len(s.Credits))
	copy(result, s.Credits)
	return result
}

// ---------------------------------------------------------------------------
// RecordingK8sUpdater -- test double for K8sStatusUpdater
// ---------------------------------------------------------------------------

// StatusPatch captures one K8sStatusUpdater call.
type StatusPatch struct {
	Type     string // "succeeded", "failed", "disputed", "corroboration_failed"
	Tenant   string
	IntentID string
}

// RecordingK8sUpdater records status patches for assertion.
type RecordingK8sUpdater struct {
	mu      sync.Mutex
	Patches []StatusPatch
	Err     error
}

var _ K8sStatusUpdater = (*RecordingK8sUpdater)(nil)

func (r *RecordingK8sUpdater) PatchPaymentSucceeded(_ context.Context, tenant string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Patches = append(r.Patches, StatusPatch{Type: "succeeded", Tenant: tenant})
	return r.Err
}

func (r *RecordingK8sUpdater) PatchPaymentFailed(_ context.Context, tenant string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Patches = append(r.Patches, StatusPatch{Type: "failed", Tenant: tenant})
	return r.Err
}

func (r *RecordingK8sUpdater) PatchPaymentDisputed(_ context.Context, tenant, intentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Patches = append(r.Patches, StatusPatch{Type: "disputed", Tenant: tenant, IntentID: intentID})
	return r.Err
}

func (r *RecordingK8sUpdater) PatchCorroborationFailed(_ context.Context, tenant, intentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Patches = append(r.Patches, StatusPatch{Type: "corroboration_failed", Tenant: tenant, IntentID: intentID})
	return r.Err
}

// GetPatches returns a copy of the recorded patches, safe for concurrent use.
func (r *RecordingK8sUpdater) GetPatches() []StatusPatch {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]StatusPatch, len(r.Patches))
	copy(result, r.Patches)
	return result
}
