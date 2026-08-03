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

package psp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rdwj/platform-billing-operator/internal/psp"
)

func TestHTTPLedgerClient_CreditWallet_Success(t *testing.T) {
	var receivedBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/adjustments", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		data, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &receivedBody))

		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := &psp.HTTPLedgerClient{BaseURL: srv.URL}
	err := client.CreditWallet(context.Background(), "acme", "50.000000", "pi_abc123")

	require.NoError(t, err)
	assert.Equal(t, "acme", receivedBody["tenant"])
	assert.Equal(t, "50.000000", receivedBody["amount"])
	assert.Equal(t, "payment_credit", receivedBody["kind"])
	assert.Equal(t, "PSP payment pi_abc123", receivedBody["reason"])
	assert.Equal(t, "psp-credit-pi_abc123", receivedBody["idempotency_key"])
}

func TestHTTPLedgerClient_CreditWallet_IdempotencyReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	client := &psp.HTTPLedgerClient{BaseURL: srv.URL}
	err := client.CreditWallet(context.Background(), "acme", "50.000000", "pi_abc123")

	assert.NoError(t, err, "409 Conflict should be treated as success (idempotent replay)")
}

func TestHTTPLedgerClient_CreditWallet_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &psp.HTTPLedgerClient{BaseURL: srv.URL}
	err := client.CreditWallet(context.Background(), "acme", "50.000000", "pi_abc123")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestHTTPLedgerClient_CreditWallet_AmountPassedAsString(t *testing.T) {
	var receivedBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &receivedBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := &psp.HTTPLedgerClient{BaseURL: srv.URL}
	_ = client.CreditWallet(context.Background(), "acme", "123.456789", "pi_xyz")

	// JSON encodes strings with quotes; verify the raw value is a string, not a number.
	amount, ok := receivedBody["amount"].(string)
	assert.True(t, ok, "amount should be a JSON string, not a number")
	assert.Equal(t, "123.456789", amount)
}
