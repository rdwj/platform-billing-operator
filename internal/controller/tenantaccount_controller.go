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

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/enforcement"
)

const (
	requeueBalance      = 30 * time.Second
	minPatchInterval    = 30 * time.Second
	defaultMaxStaleness = 10 * time.Minute
)

// BalanceQuerier abstracts the ledger queries the balance controller needs.
type BalanceQuerier interface {
	QueryTenantBalance(ctx context.Context, tenantID string) (string, time.Time, error)
	QueryMTDSpend(ctx context.Context, tenantID string) (string, error)
	UpdateWallet(ctx context.Context, tenantID, balance string, asOf time.Time) error
}

// TenantAccountReconciler reconciles TenantAccount CRs by querying the ledger
// for the current balance and patching status. For ShowbackOnly mode, usage is
// rated and visible but no wallet/PSP enforcement runs.
type TenantAccountReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Ledger           BalanceQuerier
	Enforcement      enforcement.EnforcementBackend
	RequeueInterval  time.Duration
	MinPatchInterval time.Duration
	lastPatch        sync.Map
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=tenantaccounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=tenantaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=tenantaccounts/finalizers,verbs=update
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=pricebooks,verbs=get;list;watch

func (r *TenantAccountReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var acct billingv1alpha1.TenantAccount
	if err := r.Get(ctx, req.NamespacedName, &acct); err != nil {
		if apierrors.IsNotFound(err) {
			r.lastPatch.Delete(req.String())
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !acct.DeletionTimestamp.IsZero() {
		r.lastPatch.Delete(req.String())
		return ctrl.Result{}, nil
	}

	if r.rateLimited(req) {
		return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
	}

	balance, asOf, err := r.Ledger.QueryTenantBalance(ctx, acct.Name)
	if err != nil {
		logger.Error(err, "querying tenant balance")
		return r.handleLedgerUnavailable(ctx, &acct, err)
	}

	mtdSpend, err := r.Ledger.QueryMTDSpend(ctx, acct.Name)
	if err != nil {
		logger.Error(err, "querying mtd spend")
		return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
	}

	now := metav1.Now()
	if asOf.IsZero() {
		asOf = now.Time
	}
	observedAt := metav1.NewTime(asOf)

	prevState := acct.Status.State
	state := r.computeState(&acct, balance)

	// Enforcement actions on state transitions
	if r.Enforcement != nil {
		switch {
		case state == billingv1alpha1.StateFrozen && prevState != billingv1alpha1.StateFrozen:
			if err := r.Enforcement.Freeze(ctx, &acct); err != nil {
				logger.Error(err, "enforcement freeze failed")
				return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
			}
		case prevState == billingv1alpha1.StateFrozen && state != billingv1alpha1.StateFrozen:
			if err := r.Enforcement.Thaw(ctx, &acct); err != nil {
				logger.Error(err, "enforcement thaw failed")
				return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
			}
		}
	}

	acct.Status.Balance = balance
	acct.Status.BalanceObservedAt = &observedAt
	acct.Status.MtdSpend = mtdSpend
	acct.Status.State = state
	acct.Status.ObservedGeneration = acct.Generation

	apimeta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionBalanceHealthy,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: acct.Generation,
		Reason:             "BalanceComputed",
		Message:            "Balance mirrored from ledger",
	})

	// Clear stale condition on successful ledger query
	apimeta.RemoveStatusCondition(&acct.Status.Conditions, billingv1alpha1.ConditionBalanceStale)

	if err := r.Ledger.UpdateWallet(ctx, acct.Name, balance, asOf); err != nil {
		logger.Error(err, "updating wallet cache")
	}

	if err := r.Status().Update(ctx, &acct); err != nil {
		return ctrl.Result{}, err
	}
	r.lastPatch.Store(req.String(), time.Now())

	logger.Info("TenantAccount reconciled", "state", state, "balance", balance)
	return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
}

func (r *TenantAccountReconciler) handleLedgerUnavailable(ctx context.Context, acct *billingv1alpha1.TenantAccount, ledgerErr error) (ctrl.Result, error) {
	now := time.Now()

	// If we have a recent enough observation, hold current state silently.
	if acct.Status.BalanceObservedAt != nil {
		staleDuration := now.Sub(acct.Status.BalanceObservedAt.Time)
		if staleDuration < defaultMaxStaleness {
			return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
		}
	}

	// Beyond maxStaleness (or never observed): set BalanceStale condition.
	// Critical invariant: never remove enforcement state. Hold what we have.
	apimeta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionBalanceStale,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: acct.Generation,
		Reason:             "LedgerUnavailable",
		Message:            fmt.Sprintf("Ledger unreachable for >%s: %v", defaultMaxStaleness, ledgerErr),
	})

	// Also mark BalanceHealthy as False.
	apimeta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionBalanceHealthy,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: acct.Generation,
		Reason:             "LedgerUnavailable",
		Message:            "Balance data is stale; holding last known enforcement state",
	})

	if err := r.Status().Update(ctx, acct); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
}

func (r *TenantAccountReconciler) computeState(acct *billingv1alpha1.TenantAccount, balance string) string {
	if acct.Spec.BillingMode == billingv1alpha1.BillingModeShowbackOnly {
		return billingv1alpha1.StateActive
	}

	bal, err := decimal.NewFromString(balance)
	if err != nil {
		if acct.Status.State == "" {
			return billingv1alpha1.StatePending
		}
		return acct.Status.State
	}

	zero := decimal.Zero

	graceAmt := zero
	if acct.Spec.GracePolicy != nil && acct.Spec.GracePolicy.GraceAmount != "" {
		if parsed, err := decimal.NewFromString(acct.Spec.GracePolicy.GraceAmount); err == nil {
			graceAmt = parsed
		}
	}

	var warnThreshold decimal.Decimal
	hasWarnThreshold := false
	if acct.Spec.GracePolicy != nil && acct.Spec.GracePolicy.WarningThreshold != "" {
		if parsed, err := decimal.NewFromString(acct.Spec.GracePolicy.WarningThreshold); err == nil {
			warnThreshold = parsed
			hasWarnThreshold = true
		}
	}

	// Frozen → Active on top-up (or Warning if below threshold)
	if acct.Status.State == billingv1alpha1.StateFrozen && bal.GreaterThan(zero) {
		if hasWarnThreshold && bal.LessThan(warnThreshold) {
			return billingv1alpha1.StateWarning
		}
		return billingv1alpha1.StateActive
	}

	// Never-activated tenants: can't be frozen/graced, only activated or held Pending.
	if acct.Status.State == "" || acct.Status.State == billingv1alpha1.StatePending {
		if bal.GreaterThan(zero) {
			if hasWarnThreshold && bal.LessThan(warnThreshold) {
				return billingv1alpha1.StateWarning
			}
			return billingv1alpha1.StateActive
		}
		return billingv1alpha1.StatePending
	}

	// Below -graceAmount → Frozen
	if bal.LessThan(graceAmt.Neg()) {
		return billingv1alpha1.StateFrozen
	}

	// At or below zero
	if bal.LessThanOrEqual(zero) {
		if graceAmt.GreaterThan(zero) {
			return billingv1alpha1.StateGrace
		}
		return billingv1alpha1.StateFrozen
	}

	// Balance > 0: check warning threshold
	if hasWarnThreshold && bal.LessThan(warnThreshold) {
		return billingv1alpha1.StateWarning
	}

	return billingv1alpha1.StateActive
}

func (r *TenantAccountReconciler) requeueAfter() time.Duration {
	if r.RequeueInterval > 0 {
		return r.RequeueInterval
	}
	return requeueBalance
}

func (r *TenantAccountReconciler) rateLimited(req ctrl.Request) bool {
	interval := r.MinPatchInterval
	if interval == 0 {
		interval = minPatchInterval
	}
	val, ok := r.lastPatch.Load(req.String())
	if !ok {
		return false
	}
	last := val.(time.Time)
	return time.Since(last) < interval
}

func (r *TenantAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.TenantAccount{}).
		Named("tenantaccount-balance").
		Complete(r)
}
