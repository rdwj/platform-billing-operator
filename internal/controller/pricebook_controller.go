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
	"strings"

	"github.com/shopspring/decimal"
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
	conditionReady         = "Ready"
	priceBookStateActivePB = "Active"
)

// PriceBookReconciler validates PriceBook specs and sets status.state to Active
// when valid, or clears it with a descriptive condition when invalid.
type PriceBookReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=pricebooks,verbs=get;list;watch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=pricebooks/status,verbs=get;update;patch

func (r *PriceBookReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pb billingv1alpha1.PriceBook
	if err := r.Get(ctx, req.NamespacedName, &pb); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Validate the spec and decide state.
	if errs := validatePriceBookSpec(&pb.Spec); len(errs) > 0 {
		pb.Status.State = ""
		apimeta.SetStatusCondition(&pb.Status.Conditions, metav1.Condition{
			Type:               conditionReady,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: pb.Generation,
			Reason:             "Invalid",
			Message:            strings.Join(errs, "; "),
		})
	} else {
		pb.Status.State = priceBookStateActivePB
		apimeta.SetStatusCondition(&pb.Status.Conditions, metav1.Condition{
			Type:               conditionReady,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: pb.Generation,
			Reason:             "Valid",
			Message:            "PriceBook spec is valid",
		})
	}

	pb.Status.ObservedGeneration = pb.Generation

	if err := r.Status().Update(ctx, &pb); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("PriceBook reconciled", "name", pb.Name, "state", pb.Status.State)
	return ctrl.Result{}, nil
}

// validatePriceBookSpec checks the spec for the minimum requirements to be
// considered Active. Returns a slice of human-readable error descriptions;
// empty means valid.
func validatePriceBookSpec(spec *billingv1alpha1.PriceBookSpec) []string {
	var errs []string

	if len(spec.SKUs) == 0 {
		errs = append(errs, "at least 1 SKU is required")
		return errs // no point checking rates on zero SKUs
	}

	if spec.EffectiveFrom.IsZero() {
		errs = append(errs, "effectiveFrom must be set")
	}

	for i, sku := range spec.SKUs {
		if len(sku.Rates) == 0 {
			errs = append(errs, fmt.Sprintf("SKU[%d] (%s): at least 1 rate entry is required", i, sku.SKUName))
			continue
		}
		for key, val := range sku.Rates {
			d, err := decimal.NewFromString(val)
			if err != nil {
				errs = append(errs, fmt.Sprintf("SKU[%d] (%s) rate %q: %q is not a valid decimal", i, sku.SKUName, key, val))
			} else if d.IsNegative() {
				errs = append(errs, fmt.Sprintf("SKU[%d] (%s) rate %q: %q must not be negative", i, sku.SKUName, key, val))
			}
		}
	}

	return errs
}

func (r *PriceBookReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.PriceBook{}).
		Named("pricebook").
		Complete(r)
}
