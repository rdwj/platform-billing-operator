package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const labelTenantID = "tenant_id"

var (
	ChargeAmountTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "billing_charge_amount_total",
			Help: "Cumulative charge amount in billing currency, by tenant and SKU.",
		},
		[]string{labelTenantID, "sku", "currency"},
	)

	EventsRatedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "billing_events_rated_total",
			Help: "Total number of usage events successfully rated.",
		},
	)

	IngestTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "billing_ingest_total",
			Help: "Total ingest attempts by result (inserted, duplicate, conflict_dlq, etc).",
		},
		[]string{"result"},
	)

	DriftExceeded = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "billing_drift_exceeded",
			Help: "1 if drift exceeds tolerance for a tenant, 0 otherwise.",
		},
		[]string{labelTenantID},
	)

	DriftPct = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "billing_drift_pct",
			Help: "Current drift percentage between ledger and Prometheus token counts.",
		},
		[]string{labelTenantID},
	)

	PSPWebhookTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "billing_psp_webhook_total",
			Help: "PSP webhook callbacks by event type.",
		},
		[]string{"event"},
	)

	TenantState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "billing_tenant_state",
			Help: "Current lifecycle state of each tenant (1 for active state, 0 otherwise).",
		},
		[]string{labelTenantID, "state"},
	)

	TenantBalance = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "billing_tenant_balance",
			Help: "Current wallet balance per tenant (observability only, not authoritative).",
		},
		[]string{labelTenantID},
	)

	TenantMTDSpend = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "billing_tenant_mtd_spend",
			Help: "Month-to-date spend per tenant (observability only).",
		},
		[]string{labelTenantID},
	)

	BudgetUtilization = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "billing_budget_utilization",
			Help: "Budget utilization ratio (spend / cap) per tenant and policy.",
		},
		[]string{labelTenantID, "policy"},
	)

	InvoicesFinalized = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "billing",
			Name:      "invoices_finalized_total",
			Help:      "Number of invoices finalized.",
		},
		[]string{"tenant"},
	)

	InvoiceErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "billing",
			Name:      "invoice_errors_total",
			Help:      "Number of invoice finalization errors.",
		},
		[]string{"tenant"},
	)

	ReservationChargesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "billing_reservation_charges_total",
			Help: "Total number of reservation charges emitted.",
		},
	)

	ReservationChargeAmountTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "billing_reservation_charge_amount_total",
			Help: "Cumulative reservation charge amount by tenant, SKU, and currency.",
		},
		[]string{labelTenantID, "sku", "currency"},
	)
)

func init() {
	crmetrics.Registry.MustRegister(
		ChargeAmountTotal,
		EventsRatedTotal,
		IngestTotal,
		DriftExceeded,
		DriftPct,
		PSPWebhookTotal,
		TenantState,
		TenantBalance,
		TenantMTDSpend,
		BudgetUtilization,
		InvoicesFinalized,
		InvoiceErrors,
		ReservationChargesTotal,
		ReservationChargeAmountTotal,
	)
}
