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
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rdwj/platform-billing-operator/internal/metrics"
	"github.com/shopspring/decimal"
)

const (
	EventPaymentSucceeded = "payment.succeeded"
	EventPaymentFailed    = "payment.failed"
	EventPaymentDisputed  = "payment.disputed"
)

// WebhookEvent is the normalized event payload from a PSP webhook.
type WebhookEvent struct {
	Event      string `json:"event"`
	IntentID   string `json:"intent_id"`
	Amount     string `json:"amount"`
	TenantName string `json:"tenant_name"`
}

// LedgerClient abstracts wallet credit operations against the ledger API.
type LedgerClient interface {
	CreditWallet(ctx context.Context, tenant, amount, intentID string) error
}

// K8sStatusUpdater abstracts TenantAccount status patches.
type K8sStatusUpdater interface {
	PatchPaymentSucceeded(ctx context.Context, tenant string) error
	PatchPaymentFailed(ctx context.Context, tenant string) error
	PatchPaymentDisputed(ctx context.Context, tenant, intentID string) error
	PatchCorroborationFailed(ctx context.Context, tenant, intentID string) error
}

// WebhookHandler processes incoming PSP webhook events. It verifies signatures,
// corroborates payment amounts, and delegates to the ledger and K8s updater.
type WebhookHandler struct {
	Backend PSPBackend
	Ledger  LedgerClient
	K8s     K8sStatusUpdater
}

var _ http.Handler = (*WebhookHandler)(nil)

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.Backend.VerifyWebhookSignature(r); err != nil {
		slog.Warn("webhook signature verification failed", "error", err)
		http.Error(w, "signature verification failed", http.StatusUnauthorized)
		return
	}

	var event WebhookEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	if event.Event == "" || event.IntentID == "" || event.TenantName == "" {
		http.Error(w, "event, intent_id, and tenant_name are required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	metrics.PSPWebhookTotal.WithLabelValues(event.Event).Inc()
	switch event.Event {
	case EventPaymentSucceeded:
		if event.Amount == "" {
			http.Error(w, "amount is required for payment.succeeded", http.StatusBadRequest)
			return
		}
		h.handleSucceeded(ctx, w, event)
	case EventPaymentFailed:
		h.handleFailed(ctx, w, event)
	case EventPaymentDisputed:
		h.handleDisputed(ctx, w, event)
	default:
		http.Error(w, fmt.Sprintf("unknown event type: %s", event.Event), http.StatusBadRequest)
	}
}

func (h *WebhookHandler) handleSucceeded(ctx context.Context, w http.ResponseWriter, event WebhookEvent) {
	log := slog.With("tenant", event.TenantName, "intent_id", event.IntentID, "event", event.Event)

	status, err := h.Backend.GetPaymentStatus(ctx, event.IntentID)
	if err != nil {
		log.Error("failed to get payment status for corroboration", "error", err)
		http.Error(w, "corroboration lookup failed", http.StatusInternalServerError)
		return
	}

	claimedAmount, err := decimal.NewFromString(event.Amount)
	if err != nil {
		log.Error("invalid claimed amount in webhook", "amount", event.Amount, "error", err)
		http.Error(w, "invalid amount format", http.StatusBadRequest)
		return
	}

	actualAmount, err := decimal.NewFromString(status.Amount)
	if err != nil {
		log.Error("invalid amount from PSP status", "amount", status.Amount, "error", err)
		http.Error(w, "corroboration failed: invalid PSP amount", http.StatusInternalServerError)
		return
	}

	if !claimedAmount.Equal(actualAmount) {
		log.Warn("payment corroboration failed: amount mismatch",
			"claimed", claimedAmount.String(), "actual", actualAmount.String())
		if patchErr := h.K8s.PatchCorroborationFailed(ctx, event.TenantName, event.IntentID); patchErr != nil {
			log.Error("failed to patch corroboration-failed condition", "error", patchErr)
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	if err := h.Ledger.CreditWallet(ctx, event.TenantName, status.Amount, event.IntentID); err != nil {
		log.Error("failed to credit wallet", "amount", status.Amount, "error", err)
		http.Error(w, "wallet credit failed", http.StatusInternalServerError)
		return
	}

	if err := h.K8s.PatchPaymentSucceeded(ctx, event.TenantName); err != nil {
		log.Error("failed to patch payment-succeeded status (wallet already credited)", "error", err)
	}

	log.Info("payment succeeded: wallet credited", "amount", status.Amount)
	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) handleFailed(ctx context.Context, w http.ResponseWriter, event WebhookEvent) {
	log := slog.With("tenant", event.TenantName, "intent_id", event.IntentID, "event", event.Event)

	if err := h.K8s.PatchPaymentFailed(ctx, event.TenantName); err != nil {
		log.Error("failed to patch payment-failed status", "error", err)
		http.Error(w, "status patch failed", http.StatusInternalServerError)
		return
	}

	log.Info("payment failed: status updated")
	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) handleDisputed(ctx context.Context, w http.ResponseWriter, event WebhookEvent) {
	log := slog.With("tenant", event.TenantName, "intent_id", event.IntentID, "event", event.Event)

	if err := h.K8s.PatchPaymentDisputed(ctx, event.TenantName, event.IntentID); err != nil {
		log.Error("failed to patch payment-disputed condition", "error", err)
		http.Error(w, "status patch failed", http.StatusInternalServerError)
		return
	}

	log.Info("payment disputed: condition set")
	w.WriteHeader(http.StatusOK)
}
