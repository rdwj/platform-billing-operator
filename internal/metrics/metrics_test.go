package metrics_test

import (
	"testing"

	"github.com/rdwj/platform-billing-operator/internal/metrics"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

func TestAllMetricsRegistered(t *testing.T) {
	// Touch each metric so Gather returns them (Vec types need at
	// least one label combination observed).
	metrics.ChargeAmountTotal.WithLabelValues("_test", "_test", "_test").Add(0)
	metrics.IngestTotal.WithLabelValues("_test").Inc()
	metrics.DriftExceeded.WithLabelValues("_test").Set(0)
	metrics.DriftPct.WithLabelValues("_test").Set(0)
	metrics.PSPWebhookTotal.WithLabelValues("_test").Inc()
	metrics.TenantState.WithLabelValues("_test", "_test").Set(0)
	metrics.TenantBalance.WithLabelValues("_test").Set(0)
	metrics.TenantMTDSpend.WithLabelValues("_test").Set(0)
	metrics.BudgetUtilization.WithLabelValues("_test", "_test").Set(0)

	want := []string{
		"billing_charge_amount_total",
		"billing_events_rated_total",
		"billing_ingest_total",
		"billing_drift_exceeded",
		"billing_drift_pct",
		"billing_psp_webhook_total",
		"billing_tenant_state",
		"billing_tenant_balance",
		"billing_tenant_mtd_spend",
		"billing_budget_utilization",
	}

	families, err := crmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	registered := make(map[string]bool)
	for _, f := range families {
		registered[f.GetName()] = true
	}

	for _, name := range want {
		if !registered[name] {
			t.Errorf("metric %q not found in controller-runtime registry", name)
		}
	}
}
