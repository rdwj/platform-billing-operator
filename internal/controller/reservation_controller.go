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

// ReservationReconciler manages the Reservation state machine:
// Requested → Confirmed → Active → Expired.
type ReservationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Now    func() time.Time
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=reservations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=reservations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=reservations/finalizers,verbs=update
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=clusterqueues,verbs=get;list;watch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=resourceflavors,verbs=get;list;watch
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
		// Phase 1: passthrough — always confirm.
		// Phase 2 will add real capacity checks here.
		res.Status.State = billingv1alpha1.ReservationStateConfirmed
		apimeta.SetStatusCondition(&res.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionCapacityConfirmed,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: res.Generation,
			Reason:             "Passthrough",
			Message:            "Capacity check deferred to Phase 2",
		})
		logger.Info("Reservation confirmed (passthrough)", "reservation", res.Name)

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

	case billingv1alpha1.ReservationStateExpired:
		return ctrl.Result{}, nil
	}

	res.Status.ObservedGeneration = res.Generation

	if res.Status.State != origState {
		if err := r.Status().Update(ctx, &res); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.requeueResult(&res, now), nil
}

func (r *ReservationReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

// requeueResult computes when to next reconcile based on state and window times.
func (r *ReservationReconciler) requeueResult(res *billingv1alpha1.Reservation, now time.Time) ctrl.Result {
	switch res.Status.State {
	case billingv1alpha1.ReservationStateConfirmed:
		if d := res.Spec.Window.Start.Time.Sub(now); d > 0 {
			return ctrl.Result{RequeueAfter: d}
		}
		return ctrl.Result{Requeue: true}
	case billingv1alpha1.ReservationStateActive:
		if d := res.Spec.Window.End.Time.Sub(now); d > 0 {
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
