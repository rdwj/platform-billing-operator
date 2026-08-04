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
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/prometheus/client_golang/prometheus/testutil"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/metrics"
)

// ---------------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------------

type stubLedger struct {
	tokens  map[string]int64
	reports []ledger.DriftReport
}

func (s *stubLedger) QueryTenantTokens(_ context.Context, tenantID string, _, _ time.Time) (int64, error) {
	return s.tokens[tenantID], nil
}

func (s *stubLedger) InsertDriftReport(_ context.Context, r ledger.DriftReport) error {
	s.reports = append(s.reports, r)
	return nil
}

type stubProm struct {
	values map[string]float64 // tenant → value
}

func (s *stubProm) Query(_ context.Context, query string, _ time.Time) ([]Sample, error) {
	for tenant, val := range s.values {
		if strings.Contains(query, tenant) {
			return []Sample{{Value: val}}, nil
		}
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := billingv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("add billing scheme: %v", err)
	}
	return s
}

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(newScheme(t)).
		WithObjects(objs...).
		WithStatusSubresource(&billingv1alpha1.TenantAccount{}).
		Build()
}

func mustDecimal(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := decimal.NewFromString(s)
	if err != nil {
		t.Fatalf("parse decimal %q: %v", s, err)
	}
	return d
}

// ---------------------------------------------------------------------------
// TestComputeDrift
// ---------------------------------------------------------------------------

func TestComputeDrift(t *testing.T) {
	tests := []struct {
		name         string
		ledgerTokens int64
		promTokens   int64
		want         string
	}{
		{"both_equal", 100, 100, "0"},
		{"both_zero", 0, 0, "0"},
		{"prom_higher_10pct", 100, 110, "9.09090909090909"},
		{"ledger_higher_10pct", 110, 100, "9.09090909090909"},
		{"ten_percent", 1000, 900, "10"},
		{"prom_zero", 100, 0, "100"},
		{"ledger_zero", 0, 100, "100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeDrift(tt.ledgerTokens, tt.promTokens)
			want := mustDecimal(t, tt.want)
			if !got.Equal(want) {
				t.Errorf("ComputeDrift(%d, %d) = %s, want %s", tt.ledgerTokens, tt.promTokens, got, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestSign
// ---------------------------------------------------------------------------

func TestSign(t *testing.T) {
	now := time.Now().UTC()
	windowStart := now.Add(-24 * time.Hour)
	windowEnd := now

	t.Run("deterministic", func(t *testing.T) {
		sig1 := Sign("tenant-a", windowStart, windowEnd, 100, 110, "9.09")
		sig2 := Sign("tenant-a", windowStart, windowEnd, 100, 110, "9.09")
		if sig1 != sig2 {
			t.Errorf("same inputs produced different signatures: %s vs %s", sig1, sig2)
		}
	})

	t.Run("different_inputs_different_sig", func(t *testing.T) {
		sig1 := Sign("tenant-a", windowStart, windowEnd, 100, 110, "9.09")
		sig2 := Sign("tenant-b", windowStart, windowEnd, 100, 110, "9.09")
		if sig1 == sig2 {
			t.Error("different inputs produced the same signature")
		}
	})
}

// ---------------------------------------------------------------------------
// TestTick — drift exceeds threshold
// ---------------------------------------------------------------------------

func TestTick_DriftExceedsThreshold(t *testing.T) {
	ctx := context.Background()

	acct := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-a", Namespace: "default"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Tenant A",
			BillingMode: billingv1alpha1.BillingModePrepaid,
		},
	}

	k8s := newFakeClient(t, acct)

	sl := &stubLedger{tokens: map[string]int64{"tenant-a": 100}}
	sp := &stubProm{values: map[string]float64{"tenant-a": 110}}

	job := New(sl, sp, k8s, slog.Default(), "5", time.Hour)
	if err := job.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Check drift report was persisted.
	if len(sl.reports) != 1 {
		t.Fatalf("expected 1 drift report, got %d", len(sl.reports))
	}
	rpt := sl.reports[0]
	if !rpt.Exceeded {
		t.Error("expected Exceeded=true")
	}
	if rpt.TenantID != "tenant-a" {
		t.Errorf("report TenantID = %q, want tenant-a", rpt.TenantID)
	}

	// Check TenantAccount condition.
	var updated billingv1alpha1.TenantAccount
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(acct), &updated); err != nil {
		t.Fatalf("get TenantAccount: %v", err)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, billingv1alpha1.ConditionReconcileDrift)
	if cond == nil {
		t.Fatal("ReconcileDrift condition not found")
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("condition status = %s, want True", cond.Status)
	}
	if cond.Reason != "DriftExceeded" {
		t.Errorf("condition reason = %q, want DriftExceeded", cond.Reason)
	}

	exceeded := testutil.ToFloat64(metrics.DriftExceeded.WithLabelValues("tenant-a"))
	if exceeded != 1 {
		t.Errorf("DriftExceeded gauge = %f, want 1", exceeded)
	}
	pct := testutil.ToFloat64(metrics.DriftPct.WithLabelValues("tenant-a"))
	if pct < 9 || pct > 10 {
		t.Errorf("DriftPct gauge = %f, want ~9.09", pct)
	}
}

// ---------------------------------------------------------------------------
// TestTick — drift within threshold
// ---------------------------------------------------------------------------

func TestTick_DriftWithinThreshold(t *testing.T) {
	ctx := context.Background()

	acct := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-b", Namespace: "default"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Tenant B",
			BillingMode: billingv1alpha1.BillingModePrepaid,
		},
	}

	k8s := newFakeClient(t, acct)

	sl := &stubLedger{tokens: map[string]int64{"tenant-b": 100}}
	sp := &stubProm{values: map[string]float64{"tenant-b": 102}}

	job := New(sl, sp, k8s, slog.Default(), "5", time.Hour)
	if err := job.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(sl.reports) != 1 {
		t.Fatalf("expected 1 drift report, got %d", len(sl.reports))
	}
	if sl.reports[0].Exceeded {
		t.Error("expected Exceeded=false for 2% drift under 5% tolerance")
	}

	var updated billingv1alpha1.TenantAccount
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(acct), &updated); err != nil {
		t.Fatalf("get TenantAccount: %v", err)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, billingv1alpha1.ConditionReconcileDrift)
	if cond == nil {
		t.Fatal("ReconcileDrift condition not found")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("condition status = %s, want False", cond.Status)
	}
	if cond.Reason != "WithinTolerance" {
		t.Errorf("condition reason = %q, want WithinTolerance", cond.Reason)
	}
}

// ---------------------------------------------------------------------------
// TestTick — both sources return zero
// ---------------------------------------------------------------------------

func TestTick_BothZero(t *testing.T) {
	ctx := context.Background()

	acct := &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-c", Namespace: "default"},
		Spec: billingv1alpha1.TenantAccountSpec{
			DisplayName: "Tenant C",
			BillingMode: billingv1alpha1.BillingModeShowbackOnly,
		},
	}

	k8s := newFakeClient(t, acct)

	sl := &stubLedger{tokens: map[string]int64{"tenant-c": 0}}
	sp := &stubProm{values: map[string]float64{"tenant-c": 0}}

	job := New(sl, sp, k8s, slog.Default(), "5", time.Hour)
	if err := job.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(sl.reports) != 1 {
		t.Fatalf("expected 1 drift report, got %d", len(sl.reports))
	}
	rpt := sl.reports[0]
	if rpt.DriftPct != "0" {
		t.Errorf("DriftPct = %q, want 0", rpt.DriftPct)
	}
	if rpt.Exceeded {
		t.Error("expected Exceeded=false when both sources are zero")
	}

	var updated billingv1alpha1.TenantAccount
	if err := k8s.Get(ctx, client.ObjectKeyFromObject(acct), &updated); err != nil {
		t.Fatalf("get TenantAccount: %v", err)
	}
	cond := apimeta.FindStatusCondition(updated.Status.Conditions, billingv1alpha1.ConditionReconcileDrift)
	if cond == nil {
		t.Fatal("ReconcileDrift condition not found")
	}
	if cond.Status != metav1.ConditionFalse {
		t.Errorf("condition status = %s, want False", cond.Status)
	}
	if cond.Reason != "WithinTolerance" {
		t.Errorf("condition reason = %q, want WithinTolerance", cond.Reason)
	}
}
