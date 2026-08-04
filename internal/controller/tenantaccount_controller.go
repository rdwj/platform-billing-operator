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
	"strconv"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/enforcement"
	"github.com/rdwj/platform-billing-operator/internal/metrics"
)

const (
	requeueBalance       = 30 * time.Second
	minPatchInterval     = 30 * time.Second
	defaultMaxStaleness  = 10 * time.Minute
	priceBookStateActive = "Active"
	unitPer1MTokens      = "per-1M-tokens"
	unitPer1KTokens      = "per-1K-tokens"
)

// BalanceQuerier abstracts the ledger queries the balance controller needs.
type BalanceQuerier interface {
	QueryTenantBalance(ctx context.Context, tenantID string) (string, time.Time, error)
	QueryMTDSpend(ctx context.Context, tenantID string) (string, error)
	UpdateWallet(ctx context.Context, tenantID, balance string, asOf time.Time) error
	QueryAdjustmentTotal(ctx context.Context, tenantID, kind string, since time.Time) (string, error)
	QueryAdjustmentCount(ctx context.Context, tenantID, kind string, since time.Time) (int64, error)
}

// TenantAccountReconciler reconciles TenantAccount CRs by querying the ledger
// for the current balance and patching status. For ShowbackOnly mode, usage is
// rated and visible but no wallet/PSP enforcement runs.
type TenantAccountReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Ledger           BalanceQuerier
	Enforcement      enforcement.EnforcementBackend
	PSPRegistry      *BackendRegistry
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

	// Compute and apply token allowance for enforced billing modes.
	var allowanceStr string
	if r.Enforcement != nil && r.shouldApplyAllowance(state, acct.Spec.BillingMode) {
		if a, err := r.computeAndApplyAllowance(ctx, &acct, balance); err != nil {
			logger.Error(err, "allowance computation/apply failed")
		} else {
			allowanceStr = a
		}
	}

	// Detect payment method changes for cooldown enforcement.
	currentSecretRef := ""
	if acct.Spec.PSPCustomerSecretRef != nil {
		currentSecretRef = acct.Spec.PSPCustomerSecretRef.Name
	}
	if currentSecretRef != acct.Status.LastPSPCustomerSecretRef {
		if acct.Status.LastPSPCustomerSecretRef != "" {
			// Only set cooldown if there was a previous ref (not initial setup)
			changeTime := metav1.Now()
			acct.Status.PaymentMethodChangedAt = &changeTime
		}
		acct.Status.LastPSPCustomerSecretRef = currentSecretRef
	}

	r.maybeReload(ctx, &acct, balance, state)

	acct.Status.Balance = balance
	acct.Status.BalanceObservedAt = &observedAt
	acct.Status.MtdSpend = mtdSpend
	acct.Status.State = state
	acct.Status.AllowanceTokens = allowanceStr
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

	allStates := []string{
		billingv1alpha1.StatePending, billingv1alpha1.StateActive,
		billingv1alpha1.StateWarning, billingv1alpha1.StateGrace,
		billingv1alpha1.StateDunning, billingv1alpha1.StateFrozen,
	}
	for _, s := range allStates {
		val := float64(0)
		if s == state {
			val = 1
		}
		metrics.TenantState.WithLabelValues(acct.Name, s).Set(val)
	}
	if bal, err := strconv.ParseFloat(balance, 64); err == nil {
		metrics.TenantBalance.WithLabelValues(acct.Name).Set(bal)
	}
	if spend, err := strconv.ParseFloat(mtdSpend, 64); err == nil {
		metrics.TenantMTDSpend.WithLabelValues(acct.Name).Set(spend)
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

	// Payment failure escalation takes precedence over balance-based state.
	if acct.Status.ConsecutivePaymentFailures >= 3 {
		return billingv1alpha1.StateFrozen
	}
	if acct.Status.ConsecutivePaymentFailures > 0 {
		return billingv1alpha1.StateDunning
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

func (r *TenantAccountReconciler) shouldApplyAllowance(state, billingMode string) bool {
	if billingMode == billingv1alpha1.BillingModeShowbackOnly {
		return false
	}
	switch state {
	case billingv1alpha1.StateActive, billingv1alpha1.StateWarning, billingv1alpha1.StateGrace, billingv1alpha1.StateDunning:
		return true
	default:
		return false
	}
}

// computeAndApplyAllowance resolves the tenant's Plan and PriceBook, computes
// the token allowance, and calls Apply on the enforcement backend. Returns the
// allowance string for status reporting.
func (r *TenantAccountReconciler) computeAndApplyAllowance(ctx context.Context, acct *billingv1alpha1.TenantAccount, balance string) (string, error) {
	// Resolve Plan
	var plan billingv1alpha1.Plan
	if err := r.Get(ctx, types.NamespacedName{
		Name:      acct.Spec.PlanRef.Name,
		Namespace: acct.Namespace,
	}, &plan); err != nil {
		return "", fmt.Errorf("resolving plan %s: %w", acct.Spec.PlanRef.Name, err)
	}

	// Resolve PriceBook: check tenant pin first, then Plan's ref, then list Active.
	priceBookName := acct.Spec.PriceBookPin
	if priceBookName == "" {
		priceBookName = plan.Spec.PriceBookRef
	}

	var priceBook billingv1alpha1.PriceBook
	if priceBookName != "" {
		if err := r.Get(ctx, types.NamespacedName{
			Name:      priceBookName,
			Namespace: acct.Namespace,
		}, &priceBook); err != nil {
			return "", fmt.Errorf("resolving pricebook %s: %w", priceBookName, err)
		}
	} else {
		// Find the Active PriceBook in the namespace.
		var pbList billingv1alpha1.PriceBookList
		if err := r.List(ctx, &pbList, client.InNamespace(acct.Namespace)); err != nil {
			return "", fmt.Errorf("listing pricebooks: %w", err)
		}
		found := false
		for i := range pbList.Items {
			if pbList.Items[i].Status.State == priceBookStateActive {
				priceBook = pbList.Items[i]
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("no Active PriceBook found in namespace %s", acct.Namespace)
		}
	}

	// Extract price-per-token estimate from the first SKU.
	priceEstimate, err := extractPriceEstimate(&priceBook)
	if err != nil {
		return "", fmt.Errorf("extracting price estimate: %w", err)
	}

	// Compute allowance.
	allowance, err := enforcement.ComputeAllowance(balance, priceEstimate, plan.Spec.Limits.TokensPerMinute)
	if err != nil {
		return "", fmt.Errorf("computing allowance: %w", err)
	}

	// Apply to the enforcement backend.
	if err := r.Enforcement.Apply(ctx, acct, allowance); err != nil {
		return "", fmt.Errorf("applying allowance: %w", err)
	}

	return allowance, nil
}

// extractPriceEstimate computes a blended per-token price from the first SKU's
// prompt and completion rates. Returns a per-token decimal string.
func extractPriceEstimate(pb *billingv1alpha1.PriceBook) (string, error) {
	if len(pb.Spec.SKUs) == 0 {
		return "", fmt.Errorf("pricebook %s has no SKUs", pb.Name)
	}

	sku := pb.Spec.SKUs[0]
	promptRate := sku.Rates["prompt_tokens"]
	completionRate := sku.Rates["completion_tokens"]

	if promptRate == "" && completionRate == "" {
		return "", fmt.Errorf("SKU %s has no prompt_tokens or completion_tokens rate", sku.SKUName)
	}

	// If only one rate is present, use it directly.
	if promptRate == "" {
		promptRate = completionRate
	}
	if completionRate == "" {
		completionRate = promptRate
	}

	// The rates in PriceBook are per-unit (e.g., per-1M-tokens). Normalize
	// to per-token based on the unit.
	promptPerToken, err := normalizeRate(promptRate, sku.Unit)
	if err != nil {
		return "", fmt.Errorf("normalizing prompt rate: %w", err)
	}
	completionPerToken, err := normalizeRate(completionRate, sku.Unit)
	if err != nil {
		return "", fmt.Errorf("normalizing completion rate: %w", err)
	}

	return enforcement.EstimatePricePerToken(promptPerToken, completionPerToken)
}

// normalizeRate converts a rate from the PriceBook unit to a per-token rate.
func normalizeRate(rate, unit string) (string, error) {
	d, err := decimal.NewFromString(rate)
	if err != nil {
		return "", fmt.Errorf("invalid rate %q: %w", rate, err)
	}

	switch unit {
	case unitPer1MTokens:
		return d.Div(decimal.NewFromInt(1_000_000)).String(), nil
	case unitPer1KTokens:
		return d.Div(decimal.NewFromInt(1_000)).String(), nil
	case "per-token", "":
		return d.String(), nil
	default:
		return d.String(), nil
	}
}

// maybeReload creates a PaymentIntent when the tenant's balance crosses below
// the reload threshold. Skips if no PSP backend, no reload policy, already
// has a pending intent (idempotency), or tenant is Frozen.
func (r *TenantAccountReconciler) maybeReload(ctx context.Context, acct *billingv1alpha1.TenantAccount, balance, state string) {
	if r.PSPRegistry == nil {
		return
	}
	if acct.Spec.BillingConnectorRef == nil {
		return
	}
	backend, ok := r.PSPRegistry.Get(acct.Spec.BillingConnectorRef.Name)
	if !ok {
		log.FromContext(ctx).Info("BillingConnector not found in registry",
			"connector", acct.Spec.BillingConnectorRef.Name)
		return
	}
	if acct.Spec.BillingMode == billingv1alpha1.BillingModeShowbackOnly {
		return
	}
	if acct.Spec.ReloadPolicy == nil || !acct.Spec.ReloadPolicy.Enabled {
		return
	}
	switch state {
	case billingv1alpha1.StateActive, billingv1alpha1.StateWarning, billingv1alpha1.StateGrace, billingv1alpha1.StateDunning:
	default:
		return
	}
	if acct.Status.PendingReloadIntentID != "" {
		return
	}

	bal, err := decimal.NewFromString(balance)
	if err != nil {
		return
	}
	threshold, err := decimal.NewFromString(acct.Spec.ReloadPolicy.ThresholdAmount)
	if err != nil {
		return
	}
	if bal.GreaterThanOrEqual(threshold) {
		return
	}

	logger := log.FromContext(ctx)

	// Guard 1: MonthlyCapAmount enforcement
	reloadAmt, err := decimal.NewFromString(acct.Spec.ReloadPolicy.ReloadAmount)
	if err != nil {
		return
	}
	capAmt, err := decimal.NewFromString(acct.Spec.ReloadPolicy.MonthlyCapAmount)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthlyTotalStr, err := r.Ledger.QueryAdjustmentTotal(ctx, acct.Name, "payment_credit", monthStart)
	if err != nil {
		logger.Error(err, "monthly cap query failed, rejecting reload (fail-closed)")
		return
	}
	monthlyTotal, err := decimal.NewFromString(monthlyTotalStr)
	if err != nil {
		logger.Error(err, "parsing monthly total")
		return
	}
	if monthlyTotal.Add(reloadAmt).GreaterThan(capAmt) {
		logger.Info("reload rejected: monthly cap would be exceeded",
			"monthlyTotal", monthlyTotal.StringFixed(6),
			"reloadAmount", reloadAmt.StringFixed(6),
			"monthlyCap", capAmt.StringFixed(6))
		return
	}

	// Guard 2: 24-hour reload velocity limit
	if acct.Spec.ReloadPolicy.MaxReloadsPerDay > 0 {
		count, err := r.Ledger.QueryAdjustmentCount(ctx, acct.Name, "payment_credit", now.Add(-24*time.Hour))
		if err != nil {
			logger.Error(err, "velocity limit query failed, rejecting reload (fail-closed)")
			return
		}
		if count >= int64(acct.Spec.ReloadPolicy.MaxReloadsPerDay) {
			logger.Info("reload rejected: 24h velocity limit reached",
				"reloadsInWindow", count,
				"maxReloadsPerDay", acct.Spec.ReloadPolicy.MaxReloadsPerDay)
			return
		}
	}

	// Guard 3: Payment method cooldown
	if acct.Status.PaymentMethodChangedAt != nil {
		cooldown := 24 * time.Hour
		if now.Sub(acct.Status.PaymentMethodChangedAt.Time) < cooldown {
			logger.Info("reload rejected: payment method cooldown active",
				"changedAt", acct.Status.PaymentMethodChangedAt.Time,
				"cooldownRemaining", cooldown-now.Sub(acct.Status.PaymentMethodChangedAt.Time))
			return
		}
	}

	intentID, err := backend.CreatePaymentIntent(ctx, acct, acct.Spec.ReloadPolicy.ReloadAmount)
	if err != nil {
		logger.Error(err, "creating reload payment intent")
		return
	}

	acct.Status.PendingReloadIntentID = intentID
	logger.Info("reload payment intent created", "intentID", intentID, "amount", acct.Spec.ReloadPolicy.ReloadAmount)
}

func (r *TenantAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.TenantAccount{}).
		Named("tenantaccount-balance").
		Complete(r)
}
