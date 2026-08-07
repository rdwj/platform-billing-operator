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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// AdjustmentPoster posts billing adjustments to the ledger HTTP API.
type AdjustmentPoster interface {
	PostEvictionCredit(ctx context.Context, tenant, amount, workloadUID string, runtimeSecs float64) error
}

// ReservationReconciler manages the Reservation state machine:
// Requested → Confirmed → Active → Expired.
// Active (or Confirmed) → Cancelled is an out-of-band terminal transition
// triggered by the HTTP cancel handler; the controller treats it as terminal.
type ReservationReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Now              func() time.Time
	GeoLabel         string
	AdjustmentPoster AdjustmentPoster
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=reservations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=reservations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=reservations/finalizers,verbs=update
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=clusterqueues,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=resourceflavors,verbs=get;list;watch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=workloads,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *ReservationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var res billingv1alpha1.Reservation
	if err := r.Get(ctx, req.NamespacedName, &res); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !res.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	now := r.now()
	origState := res.Status.State

	switch res.Status.State {
	case "", billingv1alpha1.ReservationStateRequested:
		result, err := r.handleRequested(ctx, &res, now)
		if err != nil {
			return ctrl.Result{}, err
		}
		if result != nil {
			return *result, nil
		}

	case billingv1alpha1.ReservationStateConfirmed:
		if !now.Before(res.Spec.Window.Start.Time) {
			res.Status.State = billingv1alpha1.ReservationStateActive
			logger.Info("Reservation activated", "reservation", res.Name)
		}

	case billingv1alpha1.ReservationStateActive:
		if !now.Before(res.Spec.Window.End.Time) {
			res.Status.State = billingv1alpha1.ReservationStateExpired
			logger.Info("Reservation expired", "reservation", res.Name)
		}

	case billingv1alpha1.ReservationStateExpired, billingv1alpha1.ReservationStateCancelled:
		if !apimeta.IsStatusConditionTrue(res.Status.Conditions, billingv1alpha1.ConditionKueueSynced) {
			if err := r.compileAndTrigger(ctx, &res); err != nil {
				logger.Error(err, "CQ recompilation on terminal state failed, will retry", "reservation", res.Name)
				return ctrl.Result{RequeueAfter: requeueCapacityCheck}, nil
			}
			apimeta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
				Type:               billingv1alpha1.ConditionKueueSynced,
				Status:             metav1.ConditionTrue,
				ObservedGeneration: res.Generation,
				Reason:             "Compiled",
				Message:            "CQ recompiled after terminal transition",
			})
			if err := r.Status().Update(ctx, &res); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	res.Status.ObservedGeneration = res.Generation

	if res.Status.State != origState {
		if err := r.Status().Update(ctx, &res); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.compileAndTrigger(ctx, &res); err != nil {
			logger.Error(err, "Kueue compilation failed, will retry", "reservation", res.Name)
			return ctrl.Result{RequeueAfter: requeueCapacityCheck}, nil
		}
	}

	return r.requeueResult(&res, now), nil
}

// handleRequested performs the capacity feasibility check for a Requested
// reservation. Returns a ctrl.Result pointer if the reconcile should return
// early (capacity insufficient), or nil if the reservation was confirmed and
// the caller should continue with the normal state-transition flow.
func (r *ReservationReconciler) handleRequested(ctx context.Context, res *billingv1alpha1.Reservation, _ time.Time) (*ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}

	var allRes billingv1alpha1.ReservationList
	if err := r.List(ctx, &allRes); err != nil {
		return nil, fmt.Errorf("listing reservations: %w", err)
	}

	geoLabel := r.geoLabel()
	snap := BuildCapacitySnapshot(nodeList.Items, allRes.Items, geoLabel)
	available := snap.Available(res.Spec.SKU, res.Spec.Geo)

	if available < res.Spec.Count {
		apimeta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionCapacityConfirmed,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: res.Generation,
			Reason:             "CapacityInsufficient",
			Message:            fmt.Sprintf("Need %d GPUs (%s/%s), only %d available", res.Spec.Count, res.Spec.SKU, res.Spec.Geo, max(available, 0)),
		})
		res.Status.CapacityCheck = &billingv1alpha1.CapacityCheckResult{
			Available: false,
		}
		res.Status.State = billingv1alpha1.ReservationStateRequested
		res.Status.ObservedGeneration = res.Generation

		if err := r.Status().Update(ctx, res); err != nil {
			return nil, err
		}
		logger.Info("Capacity insufficient, requeueing", "reservation", res.Name,
			"requested", res.Spec.Count, "available", available)
		result := ctrl.Result{RequeueAfter: requeueCapacityCheck}
		return &result, nil
	}

	res.Status.State = billingv1alpha1.ReservationStateConfirmed
	apimeta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionCapacityConfirmed,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: res.Generation,
		Reason:             "CapacityAvailable",
		Message:            fmt.Sprintf("Capacity confirmed: %d GPUs in %s/%s", res.Spec.Count, res.Spec.SKU, res.Spec.Geo),
	})
	res.Status.CapacityCheck = &billingv1alpha1.CapacityCheckResult{
		Available: true,
		Pool:      fmt.Sprintf("%s/%s", res.Spec.SKU, res.Spec.Geo),
	}
	logger.Info("Reservation confirmed", "reservation", res.Name)

	return nil, nil
}

// compileAndTrigger runs Kueue CQ compilation and preemption checks after a
// reservation state transition. Errors are logged but not returned to avoid
// blocking the state machine.
func (r *ReservationReconciler) compileAndTrigger(ctx context.Context, res *billingv1alpha1.Reservation) error {
	logger := log.FromContext(ctx)

	if err := compileKueueState(ctx, r.Client, res.Spec.TenantRef); err != nil {
		return fmt.Errorf("compileKueueState: %w", err)
	}

	var nodeList corev1.NodeList
	if err := r.List(ctx, &nodeList); err != nil {
		return fmt.Errorf("listing nodes: %w", err)
	}
	var allRes billingv1alpha1.ReservationList
	if err := r.List(ctx, &allRes); err != nil {
		return fmt.Errorf("listing reservations: %w", err)
	}

	credits, err := triggerPreemption(ctx, r.Client, nodeList.Items, allRes.Items, r.geoLabel(), r.now())
	if err != nil {
		return fmt.Errorf("triggerPreemption: %w", err)
	}

	// Post eviction credits best-effort. The eviction already happened;
	// failing to post a credit must not block the reconcile loop.
	if r.AdjustmentPoster != nil {
		for _, cr := range credits {
			if postErr := r.AdjustmentPoster.PostEvictionCredit(ctx, cr.TenantID, cr.Amount, cr.WorkloadUID, cr.RuntimeSecs); postErr != nil {
				logger.Error(postErr, "failed to post eviction credit (best-effort)",
					"tenant", cr.TenantID, "amount", cr.Amount, "workload", cr.WorkloadUID)
			}
		}
	}

	return nil
}

func (r *ReservationReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *ReservationReconciler) geoLabel() string {
	if r.GeoLabel != "" {
		return r.GeoLabel
	}
	return defaultGeoLabel
}

// requeueResult computes when to next reconcile based on state and window times.
func (r *ReservationReconciler) requeueResult(res *billingv1alpha1.Reservation, now time.Time) ctrl.Result {
	switch res.Status.State {
	case billingv1alpha1.ReservationStateConfirmed:
		if d := res.Spec.Window.Start.Sub(now); d > 0 {
			return ctrl.Result{RequeueAfter: d}
		}
		return ctrl.Result{Requeue: true}
	case billingv1alpha1.ReservationStateActive:
		if d := res.Spec.Window.End.Sub(now); d > 0 {
			return ctrl.Result{RequeueAfter: d}
		}
		return ctrl.Result{Requeue: true}
	default:
		return ctrl.Result{}
	}
}

func (r *ReservationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.Reservation{}).
		Named("reservation").
		Complete(r)
}
