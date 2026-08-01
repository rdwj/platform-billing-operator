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
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

const (
	requeueBalance   = 30 * time.Second
	minPatchInterval = 30 * time.Second
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
	Scheme    *runtime.Scheme
	Ledger    BalanceQuerier
	lastPatch sync.Map
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=tenantaccounts,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=tenantaccounts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=tenantaccounts/finalizers,verbs=update

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
		return ctrl.Result{RequeueAfter: requeueBalance}, nil
	}

	balance, asOf, err := r.Ledger.QueryTenantBalance(ctx, acct.Name)
	if err != nil {
		logger.Error(err, "querying tenant balance")
		return ctrl.Result{RequeueAfter: requeueBalance}, nil
	}

	mtdSpend, err := r.Ledger.QueryMTDSpend(ctx, acct.Name)
	if err != nil {
		logger.Error(err, "querying mtd spend")
		return ctrl.Result{RequeueAfter: requeueBalance}, nil
	}

	now := metav1.Now()
	if asOf.IsZero() {
		asOf = now.Time
	}
	observedAt := metav1.NewTime(asOf)

	state := r.computeState(&acct, balance)

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

	if err := r.Ledger.UpdateWallet(ctx, acct.Name, balance, asOf); err != nil {
		logger.Error(err, "updating wallet cache")
	}

	if err := r.Status().Update(ctx, &acct); err != nil {
		return ctrl.Result{}, err
	}
	r.lastPatch.Store(req.String(), time.Now())

	logger.Info("TenantAccount reconciled", "state", state, "balance", balance)
	return ctrl.Result{RequeueAfter: requeueBalance}, nil
}

func (r *TenantAccountReconciler) computeState(acct *billingv1alpha1.TenantAccount, _ string) string {
	switch acct.Spec.BillingMode {
	case "ShowbackOnly":
		return "Active"
	default:
		if acct.Status.State == "" {
			return "Pending"
		}
		return acct.Status.State
	}
}

func (r *TenantAccountReconciler) rateLimited(req ctrl.Request) bool {
	val, ok := r.lastPatch.Load(req.String())
	if !ok {
		return false
	}
	last := val.(time.Time)
	return time.Since(last) < minPatchInterval
}

func (r *TenantAccountReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.TenantAccount{}).
		Named("tenantaccount-balance").
		Complete(r)
}
