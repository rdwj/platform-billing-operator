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
	"encoding/json"
	"fmt"
	"net/http"
)

// HTTPLedgerClient implements LedgerClient by POSTing adjustments to the
// ledger-svc REST API. It never converts amounts to floats.
type HTTPLedgerClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

var _ LedgerClient = (*HTTPLedgerClient)(nil)

func (c *HTTPLedgerClient) CreditWallet(ctx context.Context, tenant, amount, intentID string) error {
	body := struct {
		Tenant         string `json:"tenant"`
		Amount         string `json:"amount"`
		Kind           string `json:"kind"`
		Reason         string `json:"reason"`
		IdempotencyKey string `json:"idempotency_key"`
	}{
		Tenant:         tenant,
		Amount:         amount,
		Kind:           "payment_credit",
		Reason:         fmt.Sprintf("PSP payment %s", intentID),
		IdempotencyKey: fmt.Sprintf("psp-credit-%s", intentID),
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal adjustment: %w", err)
	}

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/adjustments", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST /v1/adjustments: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusConflict {
		// Idempotency replay -- credit already applied, not an error.
		return nil
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("POST /v1/adjustments: unexpected status %d", resp.StatusCode)
	}
	return nil
}
