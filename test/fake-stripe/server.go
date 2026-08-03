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

// Package fakestripe implements a minimal Stripe API test double for
// platform-billing-operator tests. It handles PaymentIntent create and
// retrieve operations with enough fidelity to satisfy the stripe-go SDK's
// form-encoded request format and JSON response expectations.
//
// The server stores intents in a sync.Map so tests can pre-populate or
// inspect state. All IDs are generated as "pi_fake_<counter>".
package fakestripe

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var counter atomic.Int64

// PaymentIntent mirrors the subset of Stripe's PaymentIntent object that
// the billing operator cares about.
type PaymentIntent struct {
	ID           string `json:"id"`
	Object       string `json:"object"`
	Amount       int64  `json:"amount"`
	Currency     string `json:"currency"`
	Status       string `json:"status"`
	Customer     string `json:"customer,omitempty"`
	ClientSecret string `json:"client_secret"`
	Created      int64  `json:"created"`
}

// Server holds the mux and state for a fake Stripe API server.
type Server struct {
	intents sync.Map // id -> PaymentIntent
}

// NewServer creates a new Server instance.
func NewServer() *Server {
	return &Server{}
}

// NewMux creates a new http.ServeMux wired to a fresh Server. This is the
// primary entry point for test code.
func NewMux() *http.ServeMux {
	return NewServer().Mux()
}

// Mux returns an http.ServeMux with routes registered for this server.
func (s *Server) Mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/payment_intents", s.handleCreatePaymentIntent)
	mux.HandleFunc("GET /v1/payment_intents/{id}", s.handleGetPaymentIntent)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	return mux
}

// StoreIntent pre-populates an intent for testing (e.g., for GetPaymentStatus tests).
func (s *Server) StoreIntent(pi PaymentIntent) {
	s.intents.Store(pi.ID, pi)
}

func (s *Server) handleCreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
	// stripe-go sends application/x-www-form-urlencoded
	if err := r.ParseForm(); err != nil {
		writeError(w,"invalid form body")
		return
	}

	amountStr := r.FormValue("amount")
	if amountStr == "" {
		writeError(w,"missing required param: amount")
		return
	}
	amount, err := strconv.ParseInt(amountStr, 10, 64)
	if err != nil {
		writeError(w,"invalid amount: "+amountStr)
		return
	}

	currency := r.FormValue("currency")
	if currency == "" {
		currency = "usd"
	}

	customer := r.FormValue("customer")

	id := fmt.Sprintf("pi_fake_%d", counter.Add(1))
	now := time.Now().Unix()

	status := "requires_confirmation"
	if r.FormValue("confirm") == "true" {
		status = "succeeded"
	}

	pi := PaymentIntent{
		ID:           id,
		Object:       "payment_intent",
		Amount:       amount,
		Currency:     currency,
		Status:       status,
		Customer:     customer,
		ClientSecret: id + "_secret_fake",
		Created:      now,
	}

	s.intents.Store(id, pi)
	writeJSON(w, http.StatusOK, pi)
}

func (s *Server) handleGetPaymentIntent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w,"missing payment intent ID")
		return
	}

	val, ok := s.intents.Load(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": fmt.Sprintf("No such payment_intent: '%s'", id),
			},
		})
		return
	}

	writeJSON(w, http.StatusOK, val)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": map[string]any{
			"type":    "invalid_request_error",
			"message": msg,
		},
	})
}
