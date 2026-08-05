package charging

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"log/slog"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

type chargeRecord struct {
	RecordID     string
	TenantID     string
	PriceBook    string
	PriceBookRev int
	Amount       string
	Currency     string
	Components   json.RawMessage
	ChargedAt    time.Time
}

type fakeStore struct {
	charges []chargeRecord
	failOn  string
}

func (f *fakeStore) InsertReservationCharge(_ context.Context, recordID, tenantID, priceBook string,
	priceBookRev int, amount, currency string, components json.RawMessage,
	chargedAt time.Time) error {
	if f.failOn == recordID {
		return fmt.Errorf("simulated DB error")
	}
	for _, c := range f.charges {
		if c.RecordID == recordID {
			return nil // ON CONFLICT DO NOTHING
		}
	}
	f.charges = append(f.charges, chargeRecord{
		RecordID: recordID, TenantID: tenantID, PriceBook: priceBook,
		PriceBookRev: priceBookRev, Amount: amount, Currency: currency,
		Components: components, ChargedAt: chargedAt,
	})
	return nil
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func makeReservation(name, tenant, sku string, count int32, class, state string,
	start, end time.Time, monthlyPrice string) *billingv1alpha1.Reservation {
	return &billingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "platform-billing-operator-system",
			UID:       types.UID(name + "-uid"),
		},
		Spec: billingv1alpha1.ReservationSpec{
			TenantRef:    tenant,
			SKU:          sku,
			Count:        count,
			Geo:          "us-east",
			Class:        class,
			MonthlyPrice: monthlyPrice,
			Window: billingv1alpha1.ReservationWindow{
				Start: metav1.Time{Time: start},
				End:   metav1.Time{Time: end},
			},
		},
		Status: billingv1alpha1.ReservationStatus{
			State: state,
		},
	}
}

func TestTick_ActiveGuaranteed_EmitsCharge(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	res := makeReservation("res1", "acme", "h200", 4, "Guaranteed", "Active",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"12000.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 charge, got %d", count)
	}
	if len(store.charges) != 1 {
		t.Fatalf("expected 1 stored charge, got %d", len(store.charges))
	}

	c := store.charges[0]
	if c.TenantID != "acme" {
		t.Errorf("tenant = %q, want acme", c.TenantID)
	}
	if c.RecordID != "resv-res1-uid-2026-08" {
		t.Errorf("recordID = %q, want resv-res1-uid-2026-08", c.RecordID)
	}
	// Full month: 4 × $12,000 × 31/31 = $48,000
	if c.Amount != "48000.000000" {
		t.Errorf("amount = %q, want 48000.000000", c.Amount)
	}
}

func TestTick_ConfirmedGuaranteed_EmitsCharge(t *testing.T) {
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	res := makeReservation("res-confirmed", "beta", "h200", 2, "Guaranteed", "Confirmed",
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC),
		"6000.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatalf("Tick() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 charge, got %d", count)
	}
}

func TestTick_Idempotent(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	res := makeReservation("res-idem", "acme", "h200", 4, "Guaranteed", "Active",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"12000.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	if _, err := job.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second tick in same period — no new charges due to ON CONFLICT DO NOTHING
	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The job still "counts" it because it doesn't know if the insert was a no-op.
	// But the store should have only 1 record.
	_ = count
	if len(store.charges) != 1 {
		t.Errorf("expected 1 stored charge after 2 ticks, got %d", len(store.charges))
	}
}

func TestTick_PreemptibleSkipped(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	res := makeReservation("res-preempt", "acme", "h200", 2, "Preemptible", "Active",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"2400.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 charges for Preemptible, got %d", count)
	}
}

func TestTick_ExpiredSkipped(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	res := makeReservation("res-expired", "acme", "h200", 4, "Guaranteed", "Expired",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"12000.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 charges for Expired, got %d", count)
	}
}

func TestTick_PartialMonth_ProRata(t *testing.T) {
	// Reservation starts Aug 16, month has 31 days.
	// Aug 16 through Aug 31 = 16 billable days.
	now := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)
	res := makeReservation("res-partial", "acme", "h200", 1, "Guaranteed", "Active",
		time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 30, 23, 59, 59, 0, time.UTC),
		"3100.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected 1 charge, got %d", count)
	}

	// Pro-rata: 3100 × 1 × 16/31 = 1600.000000
	c := store.charges[0]
	if c.Amount != "1600.000000" {
		t.Errorf("amount = %q, want 1600.000000", c.Amount)
	}
}

func TestTick_PartialMonth_EndEarly(t *testing.T) {
	// Reservation started Jul 1, ends Aug 11 00:00:00 (exclusive boundary).
	// Catch-up emits charges for both July (full) and August (partial: 10 days).
	now := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	res := makeReservation("res-end-early", "acme", "h200", 2, "Guaranteed", "Active",
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
		"3100.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 charges (Jul + Aug), got %d", count)
	}

	// July: full month, 3100 × 2 × 31/31 = 6200.000000
	if store.charges[0].Amount != "6200.000000" {
		t.Errorf("Jul amount = %q, want 6200.000000", store.charges[0].Amount)
	}
	// August: partial, 3100 × 2 × 10/31 = 2000.000000
	if store.charges[1].Amount != "2000.000000" {
		t.Errorf("Aug amount = %q, want 2000.000000", store.charges[1].Amount)
	}
}

func TestTick_MultipleReservations(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	res1 := makeReservation("res-a", "acme", "h200", 4, "Guaranteed", "Active",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"12000.000000")
	res2 := makeReservation("res-b", "beta", "h200", 2, "Guaranteed", "Active",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"6000.000000")
	// Preemptible — should be skipped
	res3 := makeReservation("res-c", "acme", "h200", 1, "Preemptible", "Active",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"2400.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).
		WithObjects(res1, res2, res3).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 charges (2 Guaranteed, 1 Preemptible skipped), got %d", count)
	}
}

func TestTick_DBError_StopsBatch(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	res := makeReservation("res-fail", "acme", "h200", 4, "Guaranteed", "Active",
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC),
		"12000.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{failOn: "resv-res-fail-uid-2026-08"}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	_, err := job.Tick(context.Background())
	if err == nil {
		t.Fatal("expected error from DB failure")
	}
}

func TestTick_NoReservations(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}
}

func TestTick_CatchUp_MissedMonths(t *testing.T) {
	// Reservation started Jun 1, now is Aug 15. Three months of charges
	// should be emitted: Jun, Jul, Aug.
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	res := makeReservation("res-catchup", "acme", "h200", 1, "Guaranteed", "Active",
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		"3000.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	count, err := job.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 charges (Jun/Jul/Aug), got %d", count)
	}

	// Verify distinct period keys
	periods := map[string]bool{}
	for _, c := range store.charges {
		periods[c.RecordID] = true
	}
	for _, p := range []string{"resv-res-catchup-uid-2026-06", "resv-res-catchup-uid-2026-07", "resv-res-catchup-uid-2026-08"} {
		if !periods[p] {
			t.Errorf("missing charge for period %s", p)
		}
	}

	// Jun: 30 days, Jul: 31 days, Aug: 31 days — all full months at $3000
	if store.charges[0].Amount != "3000.000000" {
		t.Errorf("Jun amount = %q, want 3000.000000", store.charges[0].Amount)
	}
}

func TestTick_CatchUp_Idempotent(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	res := makeReservation("res-catchup2", "acme", "h200", 1, "Guaranteed", "Active",
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC),
		"3000.000000")

	k8s := fakeclient.NewClientBuilder().WithScheme(newScheme(t)).WithObjects(res).Build()
	store := &fakeStore{}
	job := New(k8s, store, "platform-billing-operator-system", slog.Default())
	job.SetNow(func() time.Time { return now })

	if _, err := job.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.charges) != 2 {
		t.Fatalf("expected 2 charges after first tick, got %d", len(store.charges))
	}

	// Second tick — all periods already charged, no new rows
	if _, err := job.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.charges) != 2 {
		t.Errorf("expected 2 charges after second tick, got %d", len(store.charges))
	}
}

func TestBillingPeriods(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		now   time.Time
		want  int
	}{
		{"same month", time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC), 1},
		{"two months", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), 2},
		{"six months", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), 6},
		{"cross year", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2027, 2, 15, 0, 0, 0, 0, time.UTC), 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			periods := billingPeriods(tt.start, tt.now)
			if len(periods) != tt.want {
				t.Errorf("billingPeriods() returned %d periods, want %d", len(periods), tt.want)
			}
		})
	}
}

func TestCalendarDays(t *testing.T) {
	tests := []struct {
		name  string
		start time.Time
		end   time.Time
		want  int
	}{
		{"full day", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC), 1},
		{"31 days", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), 31},
		{"partial day rounds up", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 31, 23, 59, 59, 0, time.UTC), 31},
		{"same day midnight", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), 0},
		{"sub-day", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := calendarDays(tt.start, tt.end); got != tt.want {
				t.Errorf("calendarDays() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestComputePeriodCharge_FebruaryLeapYear(t *testing.T) {
	// Feb 2028 (leap year) has 29 days. Full-month reservation.
	now := time.Date(2028, 2, 15, 0, 0, 0, 0, time.UTC)
	res := makeReservation("res-feb", "acme", "h200", 1, "Guaranteed", "Active",
		time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2028, 3, 31, 23, 59, 59, 0, time.UTC),
		"2900.000000")

	amount, comp, err := computePeriodCharge(res, now)
	if err != nil {
		t.Fatal(err)
	}
	// Full Feb: 2900 × 1 × 29/29 = 2900
	if amount.StringFixed(moneyPlaces) != "2900.000000" {
		t.Errorf("amount = %s, want 2900.000000", amount.StringFixed(moneyPlaces))
	}
	if comp.DaysInMonth != 29 {
		t.Errorf("daysInMonth = %d, want 29", comp.DaysInMonth)
	}
}

func TestPeriodKey(t *testing.T) {
	tests := []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), "2026-08"},
		{time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), "2026-12"},
		{time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), "2027-01"},
	}
	for _, tt := range tests {
		if got := periodKey(tt.t); got != tt.want {
			t.Errorf("periodKey(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}
}
