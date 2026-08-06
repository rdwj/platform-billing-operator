// Package charging implements the reservation charging job: a periodic emitter
// that creates ledger charges for active Guaranteed reservations. Guaranteed
// reservations are billed per period regardless of GPU utilization.
package charging

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/shopspring/decimal"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/metrics"
)

const moneyPlaces = 6

// ChargeStore is the subset of ledger.Store needed by the charging job.
type ChargeStore interface {
	InsertReservationCharge(ctx context.Context, recordID, tenantID, priceBook string,
		priceBookRev int, amount, currency string, components json.RawMessage,
		chargedAt time.Time) error
}

// ChargeComponent describes one line item in the charge breakdown.
type ChargeComponent struct {
	SKU          string `json:"sku"`
	Description  string `json:"description"`
	Count        int32  `json:"count"`
	MonthlyRate  string `json:"monthly_rate"`
	DaysInPeriod int    `json:"days_in_period"`
	DaysInMonth  int    `json:"days_in_month"`
	Amount       string `json:"amount"`
}

// Job emits periodic charges for active Guaranteed reservations.
type Job struct {
	k8s       client.Client
	store     ChargeStore
	log       *slog.Logger
	namespace string
	now       func() time.Time
}

func New(k8s client.Client, store ChargeStore, namespace string, log *slog.Logger) *Job {
	return &Job{
		k8s:       k8s,
		store:     store,
		log:       log,
		namespace: namespace,
	}
}

// SetNow overrides the clock for testing.
func (j *Job) SetNow(fn func() time.Time) { j.now = fn }

func (j *Job) clock() time.Time {
	if j.now != nil {
		return j.now()
	}
	return time.Now().UTC()
}

// Tick processes all active Guaranteed reservations and emits charges for every
// billing period from the reservation's window start through the current month.
// This catch-up design means missed months (operator downtime, late activation)
// are charged on the next tick. Idempotent: duplicate inserts are no-ops.
func (j *Job) Tick(ctx context.Context) (int, error) {
	now := j.clock()

	var resList billingv1alpha1.ReservationList
	if err := j.k8s.List(ctx, &resList, client.InNamespace(j.namespace)); err != nil {
		return 0, fmt.Errorf("list reservations: %w", err)
	}

	charged := 0
	for i := range resList.Items {
		res := &resList.Items[i]

		if !isChargeable(res, now) {
			continue
		}

		n, err := j.chargeAllPeriods(ctx, res, now)
		if err != nil {
			return charged, err
		}
		charged += n
	}

	if charged > 0 {
		metrics.AddCount(metrics.ReservationChargesTotal, charged)
		j.log.Info("reservation charges emitted", "count", charged)
	}
	return charged, nil
}

// chargeAllPeriods iterates every month from the reservation's window start
// through now, emitting one charge per period. Already-charged periods are
// skipped via ON CONFLICT DO NOTHING.
func (j *Job) chargeAllPeriods(ctx context.Context, res *billingv1alpha1.Reservation, now time.Time) (int, error) {
	charged := 0

	for _, period := range billingPeriods(res.Spec.Window.Start.Time, now) {
		amount, comp, err := computePeriodCharge(res, period)
		if err != nil {
			j.log.Error("compute charge failed", "reservation", res.Name, "period", periodKey(period), "err", err)
			continue
		}
		if amount.IsZero() {
			continue
		}

		components, err := json.Marshal([]ChargeComponent{comp})
		if err != nil {
			j.log.Error("marshal components failed", "reservation", res.Name, "err", err)
			continue
		}

		recordID := fmt.Sprintf("resv-%s-%s", res.UID, periodKey(period))

		if err := j.store.InsertReservationCharge(ctx, recordID, res.Spec.TenantRef,
			"reservation", 0, amount.StringFixed(moneyPlaces), "USD",
			components, now); err != nil {
			return charged, fmt.Errorf("insert reservation charge for %s period %s: %w",
				res.Name, periodKey(period), err)
		}

		metrics.AddDecimalAmount(metrics.ReservationChargeAmountTotal,
			[]string{res.Spec.TenantRef, res.Spec.SKU, "USD"},
			amount.StringFixed(moneyPlaces))
		charged++
	}
	return charged, nil
}

// billingPeriods returns a time in each month from windowStart through now
// (inclusive of both endpoints' months). Each returned time is the 1st of
// that month at midnight UTC.
func billingPeriods(windowStart, now time.Time) []time.Time {
	ws := windowStart.UTC()
	n := now.UTC()
	start := time.Date(ws.Year(), ws.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)

	var periods []time.Time
	for m := start; !m.After(end); m = m.AddDate(0, 1, 0) {
		periods = append(periods, m)
	}
	return periods
}

// isChargeable returns true if the reservation should be charged.
func isChargeable(res *billingv1alpha1.Reservation, _ time.Time) bool {
	if res.Spec.Class != billingv1alpha1.ReservationClassGuaranteed {
		return false
	}
	switch res.Status.State {
	case billingv1alpha1.ReservationStateConfirmed, billingv1alpha1.ReservationStateActive,
		billingv1alpha1.ReservationStateCancelled:
		// Cancelled reservations are still chargeable so computePeriodCharge
		// can pro-rate the cancellation month via the CancelledAt clamp.
		return true
	default:
		return false
	}
}

// computePeriodCharge calculates the charge for the billing period containing
// periodRef, pro-rating for partial months (first/last month of the reservation
// window).
func computePeriodCharge(res *billingv1alpha1.Reservation, periodRef time.Time) (decimal.Decimal, ChargeComponent, error) {
	monthlyPrice, err := decimal.NewFromString(res.Spec.MonthlyPrice)
	if err != nil {
		return decimal.Zero, ChargeComponent{}, fmt.Errorf("parse monthlyPrice %q: %w", res.Spec.MonthlyPrice, err)
	}
	count := decimal.NewFromInt32(res.Spec.Count)

	year, month, _ := periodRef.Date()
	monthStart := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	monthEnd := monthStart.AddDate(0, 1, 0)
	daysInMonth := calendarDays(monthStart, monthEnd)

	winStart := res.Spec.Window.Start.UTC()
	winEnd := res.Spec.Window.End.UTC()

	periodStart := monthStart
	if winStart.After(monthStart) {
		periodStart = winStart
	}
	periodEnd := monthEnd
	if winEnd.Before(monthEnd) {
		periodEnd = winEnd
	}

	// Pro-rate for cancellation: clamp periodEnd to the cancellation
	// timestamp so we only charge for days before cancellation.
	if res.Status.CancelledAt != nil {
		cancelledAt := res.Status.CancelledAt.UTC()
		if cancelledAt.Before(periodEnd) {
			periodEnd = cancelledAt
		}
	}

	daysInPeriod := calendarDays(periodStart, periodEnd)
	if daysInPeriod <= 0 {
		return decimal.Zero, ChargeComponent{}, nil
	}

	amount := monthlyPrice.Mul(count).
		Mul(decimal.NewFromInt(int64(daysInPeriod))).
		Div(decimal.NewFromInt(int64(daysInMonth))).
		Round(moneyPlaces)

	comp := ChargeComponent{
		SKU:          res.Spec.SKU,
		Description:  fmt.Sprintf("Guaranteed reservation %s: %d × %s GPU(s)", res.Name, res.Spec.Count, res.Spec.SKU),
		Count:        res.Spec.Count,
		MonthlyRate:  monthlyPrice.StringFixed(moneyPlaces),
		DaysInPeriod: daysInPeriod,
		DaysInMonth:  daysInMonth,
		Amount:       amount.StringFixed(moneyPlaces),
	}

	return amount, comp, nil
}

// calendarDays returns the number of calendar days between two times. A partial
// day at the end boundary counts as a full billable day. Uses date subtraction
// (no floating-point) to avoid rounding artifacts.
func calendarDays(start, end time.Time) int {
	startDate := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	days := int(endDate.Sub(startDate) / (24 * time.Hour))
	// If the end time extends past midnight of endDate, it's a partial day → round up.
	if end.After(endDate) {
		days++
	}
	return days
}

// periodKey returns the billing period key for idempotency (e.g. "2026-08").
func periodKey(t time.Time) string {
	return t.Format("2006-01")
}
