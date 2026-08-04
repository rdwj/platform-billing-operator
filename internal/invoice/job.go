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

package invoice

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/metrics"
)

// InvoiceStore is the subset of ledger.Store that the invoice job needs.
type InvoiceStore interface {
	FinalizeInvoice(ctx context.Context, tenantID, period string) (*ledger.Invoice, bool, error)
}

// Job runs monthly invoice finalization for all tenants.
type Job struct {
	store InvoiceStore
	k8s   client.Client
	log   *slog.Logger
}

// New creates an invoice finalization Job.
func New(store InvoiceStore, k8s client.Client, log *slog.Logger) *Job {
	return &Job{
		store: store,
		k8s:   k8s,
		log:   log,
	}
}

// Tick runs one invoice-finalization pass. Iterates all TenantAccounts,
// finalizes the previous month's invoice for each. Idempotent: re-running
// for the same period produces no duplicates (the store skips already-finalized invoices).
func (j *Job) Tick(ctx context.Context) error {
	// Finalize the previous month
	now := time.Now().UTC()
	prevMonth := now.AddDate(0, -1, 0)
	period := prevMonth.Format("2006-01")

	var accounts billingv1alpha1.TenantAccountList
	if err := j.k8s.List(ctx, &accounts); err != nil {
		return fmt.Errorf("list TenantAccounts: %w", err)
	}

	for i := range accounts.Items {
		tenantID := accounts.Items[i].Name

		inv, newlyFinalized, err := j.store.FinalizeInvoice(ctx, tenantID, period)
		if err != nil {
			j.log.Error("finalize invoice", "tenant", tenantID, "period", period, "err", err)
			metrics.InvoiceErrors.WithLabelValues(tenantID).Inc()
			continue
		}

		if newlyFinalized {
			metrics.InvoicesFinalized.WithLabelValues(tenantID).Inc()
			j.log.Info("invoice finalized", "tenant", tenantID, "period", period,
				"invoice_id", inv.InvoiceID, "total", inv.Total)
		} else {
			j.log.Info("invoice already finalized", "tenant", tenantID, "period", period,
				"invoice_id", inv.InvoiceID, "status", inv.Status)
		}
	}

	return nil
}
