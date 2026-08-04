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
	"time"

	"github.com/shopspring/decimal"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/enforcement"
	"github.com/rdwj/platform-billing-operator/internal/metrics"
)

const (
	requeueBudget = 60 * time.Second
)

// BudgetSpendQuerier abstracts the ledger queries the budget controller needs.
type BudgetSpendQuerier interface {
	QueryPeriodSpend(ctx context.Context, tenantID string, since time.Time) (string, error)
}

// BudgetPolicyReconciler evaluates BudgetPolicy CRs against ledger spend and
// fires threshold actions (Notify, Throttle, Freeze).
type BudgetPolicyReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	Ledger          BudgetSpendQuerier
	Enforcement     enforcement.EnforcementBackend
	Recorder        record.EventRecorder
	RequeueInterval time.Duration
	Now             func() time.Time
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=budgetpolicies,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=budgetpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=budgetpolicies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *BudgetPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var bp billingv1alpha1.BudgetPolicy
	if err := r.Get(ctx, req.NamespacedName, &bp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !bp.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	var acct billingv1alpha1.TenantAccount
	if err := r.Get(ctx, types.NamespacedName{
		Name:      bp.Spec.TenantRef.Name,
		Namespace: bp.Namespace,
	}, &acct); err != nil {
		if apierrors.IsNotFound(err) {
			apimeta.SetStatusCondition(&bp.Status.Conditions, metav1.Condition{
				Type:               billingv1alpha1.ConditionEvaluating,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: bp.Generation,
				Reason:             "TenantNotFound",
				Message:            fmt.Sprintf("TenantAccount %s not found", bp.Spec.TenantRef.Name),
			})
			bp.Status.ObservedGeneration = bp.Generation
			if err := r.Status().Update(ctx, &bp); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
		}
		return ctrl.Result{}, err
	}

	now := r.now()
	anyThresholdFired := false

	for capIdx, cap := range bp.Spec.Caps {
		periodStart, periodEnd := periodBounds(now, cap.Period)

		prevPeriodEnd := capPeriodEnd(bp.Status.CapPeriodEnds, int32(capIdx))
		if prevPeriodEnd != nil && !prevPeriodEnd.Equal(periodEnd) {
			bp.Status.FiredThresholds = clearFiredForCap(bp.Status.FiredThresholds, int32(capIdx))
		}
		bp.Status.CapPeriodEnds = setCapPeriodEnd(bp.Status.CapPeriodEnds, int32(capIdx), periodEnd)

		spend, err := r.Ledger.QueryPeriodSpend(ctx, acct.Name, periodStart)
		if err != nil {
			logger.Error(err, "querying period spend", "tenant", acct.Name, "period", cap.Period)
			return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
		}

		spendDec, err := decimal.NewFromString(spend)
		if err != nil {
			logger.Error(err, "parsing spend", "spend", spend)
			return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
		}
		capAmt, err := decimal.NewFromString(cap.Amount)
		if err != nil {
			logger.Error(err, "parsing cap amount", "amount", cap.Amount)
			return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
		}

		bp.Status.CurrentSpend = spendDec.StringFixed(6)
		if !capAmt.IsZero() {
			metrics.BudgetUtilization.WithLabelValues(acct.Name, bp.Name).Set(spendDec.Div(capAmt).InexactFloat64())
		}

		for _, threshold := range cap.Thresholds {
			if alreadyFired(bp.Status.FiredThresholds, int32(capIdx), threshold.Pct) {
				continue
			}

			triggerAmt := capAmt.Mul(decimal.NewFromInt32(threshold.Pct)).Div(decimal.NewFromInt(100))
			if spendDec.LessThan(triggerAmt) {
				continue
			}

			bp.Status.FiredThresholds = append(bp.Status.FiredThresholds, billingv1alpha1.FiredThreshold{
				CapIndex: int32(capIdx),
				Pct:      threshold.Pct,
				FiredAt:  metav1.NewTime(now),
			})
			anyThresholdFired = true

			r.Recorder.Eventf(&bp, "Warning", "BudgetThreshold",
				"Threshold %d%% fired for cap %d (spend: %s / %s, action: %s)",
				threshold.Pct, capIdx, spendDec.StringFixed(2), cap.Amount, threshold.Action)

			switch threshold.Action {
			case billingv1alpha1.ThresholdActionThrottle:
				// Throttle records the event and condition. Proper TPM reduction
				// requires dollar-to-token conversion via Plan/PriceBook and a
				// separate TRLP — deferred to Phase 2+ when the enforcement
				// coordination model between BudgetPolicy and TenantAccount is
				// finalized.
				logger.Info("budget throttle threshold fired",
					"tenant", acct.Name, "cap", capIdx, "pct", threshold.Pct)
			case billingv1alpha1.ThresholdActionFreeze:
				if r.Enforcement != nil {
					if err := r.Enforcement.Freeze(ctx, &acct); err != nil {
						logger.Error(err, "freeze enforcement failed")
					}
				}
			}
		}
	}

	if anyThresholdFired {
		apimeta.SetStatusCondition(&bp.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionThresholdFired,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: bp.Generation,
			Reason:             "ThresholdTriggered",
			Message:            "One or more budget thresholds have been triggered",
		})
		apimeta.RemoveStatusCondition(&bp.Status.Conditions, billingv1alpha1.ConditionWithinBudget)
	} else if len(bp.Status.FiredThresholds) == 0 {
		apimeta.SetStatusCondition(&bp.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionWithinBudget,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: bp.Generation,
			Reason:             "WithinAllCaps",
			Message:            "Spend is within all configured budget caps",
		})
		apimeta.RemoveStatusCondition(&bp.Status.Conditions, billingv1alpha1.ConditionThresholdFired)
	}

	apimeta.SetStatusCondition(&bp.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionEvaluating,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: bp.Generation,
		Reason:             "EvaluationComplete",
		Message:            "Budget evaluation completed successfully",
	})

	bp.Status.ObservedGeneration = bp.Generation

	if err := r.Status().Update(ctx, &bp); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("BudgetPolicy reconciled",
		"tenant", bp.Spec.TenantRef.Name,
		"spend", bp.Status.CurrentSpend,
		"firedCount", len(bp.Status.FiredThresholds))
	return ctrl.Result{RequeueAfter: r.requeueAfter()}, nil
}

func (r *BudgetPolicyReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *BudgetPolicyReconciler) requeueAfter() time.Duration {
	if r.RequeueInterval > 0 {
		return r.RequeueInterval
	}
	return requeueBudget
}

func periodBounds(t time.Time, period string) (time.Time, time.Time) {
	t = t.UTC()
	switch period {
	case billingv1alpha1.PeriodDaily:
		start := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 1)
	case billingv1alpha1.PeriodWeekly:
		weekday := int(t.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		start := time.Date(t.Year(), t.Month(), t.Day()-weekday+1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 0, 7)
	default: // Monthly
		start := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
		return start, start.AddDate(0, 1, 0)
	}
}

func alreadyFired(fired []billingv1alpha1.FiredThreshold, capIdx, pct int32) bool {
	for _, f := range fired {
		if f.CapIndex == capIdx && f.Pct == pct {
			return true
		}
	}
	return false
}

func clearFiredForCap(fired []billingv1alpha1.FiredThreshold, capIdx int32) []billingv1alpha1.FiredThreshold {
	result := make([]billingv1alpha1.FiredThreshold, 0, len(fired))
	for _, f := range fired {
		if f.CapIndex != capIdx {
			result = append(result, f)
		}
	}
	return result
}

func capPeriodEnd(ends []billingv1alpha1.CapPeriodEnd, capIdx int32) *time.Time {
	for _, e := range ends {
		if e.CapIndex == capIdx {
			t := e.PeriodEnd.Time
			return &t
		}
	}
	return nil
}

func setCapPeriodEnd(ends []billingv1alpha1.CapPeriodEnd, capIdx int32, t time.Time) []billingv1alpha1.CapPeriodEnd {
	for i, e := range ends {
		if e.CapIndex == capIdx {
			ends[i].PeriodEnd = metav1.NewTime(t)
			return ends
		}
	}
	return append(ends, billingv1alpha1.CapPeriodEnd{
		CapIndex:  capIdx,
		PeriodEnd: metav1.NewTime(t),
	})
}

func (r *BudgetPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.BudgetPolicy{}).
		Named("budgetpolicy-evaluator").
		Complete(r)
}
