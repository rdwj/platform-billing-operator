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

package drift

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/shopspring/decimal"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

// LedgerQuerier is the subset of ledger.Store that the drift reconciler needs.
type LedgerQuerier interface {
	QueryTenantTokens(ctx context.Context, tenantID string, from, to time.Time) (int64, error)
	InsertDriftReport(ctx context.Context, r ledger.DriftReport) error
}

// DriftJob compares token counts between the billing ledger and Prometheus,
// flags TenantAccounts whose drift exceeds the configured tolerance, and
// persists a DriftReport for each tenant.
type DriftJob struct {
	ledger       LedgerQuerier
	prom         PromClient
	k8s          client.Client
	log          *slog.Logger
	tolerancePct decimal.Decimal
	window       time.Duration
}

// New creates a DriftJob. tolerancePct is parsed as a decimal percentage
// (default 5); window is how far back each tick looks (default 24h).
func New(lq LedgerQuerier, prom PromClient, k8s client.Client, log *slog.Logger, tolerancePct string, window time.Duration) *DriftJob {
	tol, err := decimal.NewFromString(tolerancePct)
	if err != nil {
		tol = decimal.NewFromInt(5)
	}
	if window == 0 {
		window = 24 * time.Hour
	}
	return &DriftJob{
		ledger:       lq,
		prom:         prom,
		k8s:          k8s,
		log:          log,
		tolerancePct: tol,
		window:       window,
	}
}

// Tick runs one drift-check pass across all TenantAccounts. Individual
// per-tenant errors are logged and do not stop the sweep.
func (j *DriftJob) Tick(ctx context.Context) error {
	windowEnd := time.Now().UTC()
	windowStart := windowEnd.Add(-j.window)

	var accounts billingv1alpha1.TenantAccountList
	if err := j.k8s.List(ctx, &accounts); err != nil {
		return fmt.Errorf("list TenantAccounts: %w", err)
	}

	for i := range accounts.Items {
		acct := &accounts.Items[i]
		tenantID := acct.Name

		// --- ledger side ---
		ledgerTokens, err := j.ledger.QueryTenantTokens(ctx, tenantID, windowStart, windowEnd)
		if err != nil {
			j.log.Error("query ledger tokens", "tenant", tenantID, "err", err)
			continue
		}

		// --- prometheus side ---
		promQL := fmt.Sprintf(
			"sum(increase(vllm:prompt_tokens_total{tenant_id=\"%s\"}[%ds]))",
			tenantID, int(j.window.Seconds()),
		)
		samples, err := j.prom.Query(ctx, promQL, windowEnd)
		if err != nil {
			j.log.Error("query prometheus", "tenant", tenantID, "err", err)
			continue
		}
		var totalValue float64
		for _, s := range samples {
			totalValue += s.Value
		}
		promTokens := int64(math.Round(totalValue))

		// --- drift computation ---
		driftPct := ComputeDrift(ledgerTokens, promTokens)
		exceeded := driftPct.GreaterThan(j.tolerancePct)

		// --- persist report ---
		sig := Sign(tenantID, windowStart, windowEnd, ledgerTokens, promTokens, driftPct.String())
		if err := j.ledger.InsertDriftReport(ctx, ledger.DriftReport{
			TenantID:     tenantID,
			WindowStart:  windowStart,
			WindowEnd:    windowEnd,
			LedgerTokens: ledgerTokens,
			PromTokens:   promTokens,
			DriftPct:     driftPct.String(),
			TolerancePct: j.tolerancePct.String(),
			Exceeded:     exceeded,
			Signature:    sig,
		}); err != nil {
			j.log.Error("insert drift report", "tenant", tenantID, "err", err)
		}

		// --- update TenantAccount condition ---
		if exceeded {
			apimeta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
				Type:               billingv1alpha1.ConditionReconcileDrift,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: acct.Generation,
				Reason:             "DriftExceeded",
				Message:            fmt.Sprintf("Drift %.2f%% exceeds tolerance %s%%", driftPct.InexactFloat64(), j.tolerancePct.String()),
			})
		} else {
			apimeta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
				Type:               billingv1alpha1.ConditionReconcileDrift,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: acct.Generation,
				Reason:             "WithinTolerance",
				Message:            fmt.Sprintf("Drift %.2f%% within tolerance %s%%", driftPct.InexactFloat64(), j.tolerancePct.String()),
			})
		}
		if err := j.k8s.Status().Update(ctx, acct); err != nil {
			j.log.Error("update TenantAccount status", "tenant", tenantID, "err", err)
		}

		j.log.Info("drift check", "tenant", tenantID, "ledger", ledgerTokens,
			"prom", promTokens, "drift_pct", driftPct.String(), "exceeded", exceeded)
	}

	return nil
}

// ComputeDrift returns the drift percentage between ledger and prometheus
// token counts. Exported for testability.
func ComputeDrift(ledgerTokens, promTokens int64) decimal.Decimal {
	ledgerDec := decimal.NewFromInt(ledgerTokens)
	promDec := decimal.NewFromInt(promTokens)
	if ledgerDec.IsZero() && promDec.IsZero() {
		return decimal.Zero
	}
	maxVal := decimal.Max(ledgerDec, promDec)
	diff := ledgerDec.Sub(promDec).Abs()
	return diff.Div(maxVal).Mul(decimal.NewFromInt(100))
}

// Sign produces a SHA-256 signature for a drift report, used for
// deduplication and audit.
func Sign(tenantID string, windowStart, windowEnd time.Time, ledgerTokens, promTokens int64, driftPct string) string {
	data := fmt.Sprintf("%s|%d|%d|%d|%d|%s", tenantID, windowStart.Unix(), windowEnd.Unix(), ledgerTokens, promTokens, driftPct)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
}
