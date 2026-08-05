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

package v1alpha1

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// +kubebuilder:webhook:path=/validate-billing-opendatahub-io-v1alpha1-reservation,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.opendatahub.io,resources=reservations,verbs=create;update,versions=v1alpha1,name=vreservation-v1alpha1.kb.io,admissionReviewVersions=v1

// ReservationValidator validates Reservation resources.
type ReservationValidator struct{}

var _ webhook.CustomValidator = &ReservationValidator{}

func (v *ReservationValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&Reservation{}).
		WithValidator(v).
		Complete()
}

// ValidateCreate validates a Reservation on creation.
func (v *ReservationValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	r, ok := obj.(*Reservation)
	if !ok {
		return nil, fmt.Errorf("expected Reservation, got %T", obj)
	}

	var allErrs field.ErrorList
	allErrs = append(allErrs, validateReservationSpec(r)...)

	// On create, window.start must be in the future.
	if !r.Spec.Window.Start.Time.After(time.Now()) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "window", "start"),
			r.Spec.Window.Start.Format(time.RFC3339),
			"must be in the future",
		))
	}

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "Reservation"},
			r.Name, allErrs,
		)
	}
	return nil, nil
}

// ValidateUpdate validates a Reservation on update.
func (v *ReservationValidator) ValidateUpdate(_ context.Context, _, newObj runtime.Object) (admission.Warnings, error) {
	r, ok := newObj.(*Reservation)
	if !ok {
		return nil, fmt.Errorf("expected Reservation for newObj, got %T", newObj)
	}

	var allErrs field.ErrorList
	allErrs = append(allErrs, validateReservationSpec(r)...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "Reservation"},
			r.Name, allErrs,
		)
	}
	return nil, nil
}

// ValidateDelete is a no-op for Reservation.
func (v *ReservationValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateReservationSpec checks common validation rules for Reservation spec fields.
func validateReservationSpec(r *Reservation) field.ErrorList {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if r.Spec.TenantRef == "" {
		errs = append(errs, field.Required(specPath.Child("tenantRef"), "tenantRef is required"))
	}

	if r.Spec.SKU == "" {
		errs = append(errs, field.Required(specPath.Child("sku"), "sku is required"))
	}

	if r.Spec.Count < 1 {
		errs = append(errs, field.Invalid(
			specPath.Child("count"), r.Spec.Count, "must be at least 1",
		))
	}

	if r.Spec.Geo == "" {
		errs = append(errs, field.Required(specPath.Child("geo"), "geo is required"))
	}

	if r.Spec.Class != ReservationClassGuaranteed && r.Spec.Class != ReservationClassPreemptible {
		errs = append(errs, field.NotSupported(
			specPath.Child("class"), r.Spec.Class,
			[]string{ReservationClassGuaranteed, ReservationClassPreemptible},
		))
	}

	if !decimalPattern.MatchString(r.Spec.MonthlyPrice) {
		errs = append(errs, field.Invalid(
			specPath.Child("monthlyPrice"), r.Spec.MonthlyPrice,
			"must be a non-negative decimal string with up to 6 fractional digits",
		))
	}

	// window.start must be before window.end.
	if !r.Spec.Window.Start.Time.Before(r.Spec.Window.End.Time) {
		errs = append(errs, field.Invalid(
			specPath.Child("window", "start"),
			r.Spec.Window.Start.Format(time.RFC3339),
			"window.start must be before window.end",
		))
	}

	return errs
}
