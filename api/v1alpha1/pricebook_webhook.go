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
	"reflect"
	"regexp"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// decimalPattern matches non-negative decimal strings with up to 6 fractional digits.
var decimalPattern = regexp.MustCompile(`^\d+(\.\d{1,6})?$`)

// +kubebuilder:webhook:path=/validate-billing-opendatahub-io-v1alpha1-pricebook,mutating=false,failurePolicy=fail,sideEffects=None,groups=billing.opendatahub.io,resources=pricebooks,verbs=create;update,versions=v1alpha1,name=vpricebook-v1alpha1.kb.io,admissionReviewVersions=v1

// PriceBookValidator validates PriceBook resources.
type PriceBookValidator struct{}

var _ webhook.CustomValidator = &PriceBookValidator{}

func (v *PriceBookValidator) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&PriceBook{}).
		WithValidator(v).
		Complete()
}

// ValidateCreate validates a PriceBook on creation.
func (v *PriceBookValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	pb, ok := obj.(*PriceBook)
	if !ok {
		return nil, fmt.Errorf("expected PriceBook, got %T", obj)
	}

	var allErrs field.ErrorList

	// NoticeDays constraint: effectiveFrom must be at least noticeDays in the future.
	minEffective := time.Now().AddDate(0, 0, int(pb.Spec.NoticeDays))
	if pb.Spec.EffectiveFrom.Time.Before(minEffective) {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec", "effectiveFrom"),
			pb.Spec.EffectiveFrom.Format(time.RFC3339),
			fmt.Sprintf("must be at least %d days in the future (earliest: %s)",
				pb.Spec.NoticeDays, minEffective.Format(time.RFC3339)),
		))
	}

	// Validate decimal fields across all SKUs and pricing tiers.
	allErrs = append(allErrs, validateDecimalFields(pb)...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "PriceBook"},
			pb.Name, allErrs,
		)
	}
	return nil, nil
}

// ValidateUpdate validates a PriceBook on update.
func (v *PriceBookValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	oldPB, ok := oldObj.(*PriceBook)
	if !ok {
		return nil, fmt.Errorf("expected PriceBook for oldObj, got %T", oldObj)
	}
	newPB, ok := newObj.(*PriceBook)
	if !ok {
		return nil, fmt.Errorf("expected PriceBook for newObj, got %T", newObj)
	}

	var allErrs field.ErrorList

	// Immutability after effectiveFrom: reject SKU changes once the PriceBook is effective.
	if oldPB.Spec.EffectiveFrom.Time.Before(time.Now()) {
		if !reflect.DeepEqual(oldPB.Spec.SKUs, newPB.Spec.SKUs) {
			allErrs = append(allErrs, field.Forbidden(
				field.NewPath("spec", "skus"),
				"SKUs are immutable after effectiveFrom has passed",
			))
		}
	}

	// Validate decimal fields on the new version.
	allErrs = append(allErrs, validateDecimalFields(newPB)...)

	if len(allErrs) > 0 {
		return nil, apierrors.NewInvalid(
			schema.GroupKind{Group: GroupVersion.Group, Kind: "PriceBook"},
			newPB.Name, allErrs,
		)
	}
	return nil, nil
}

// ValidateDelete is a no-op for PriceBook.
func (v *PriceBookValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// validateDecimalFields checks that all money-related string fields in the
// PriceBook match the canonical decimal pattern (non-negative, up to 6
// fractional digits). This duplicates the kubebuilder CRD-level Pattern
// markers for defense in depth -- the webhook catches values that bypass
// CRD validation (e.g. strategic-merge patches, future API changes).
func validateDecimalFields(pb *PriceBook) field.ErrorList {
	var errs field.ErrorList
	skusPath := field.NewPath("spec", "skus")

	for i, sku := range pb.Spec.SKUs {
		skuPath := skusPath.Index(i)
		ratesPath := skuPath.Child("rates")

		// Each SKU must have at least one rate entry.
		if len(sku.Rates) == 0 {
			errs = append(errs, field.Required(ratesPath, "at least one rate entry is required"))
		}

		// Validate each rate value.
		for key, val := range sku.Rates {
			if !decimalPattern.MatchString(val) {
				errs = append(errs, field.Invalid(
					ratesPath.Key(key),
					val,
					"must be a non-negative decimal string with up to 6 fractional digits",
				))
			}
		}

		// Validate passThrough.markupPct if present.
		if sku.PassThrough != nil {
			ptPath := skuPath.Child("passThrough", "markupPct")
			if !decimalPattern.MatchString(sku.PassThrough.MarkupPct) {
				errs = append(errs, field.Invalid(
					ptPath,
					sku.PassThrough.MarkupPct,
					"must be a non-negative decimal string with up to 6 fractional digits",
				))
			}
		}
	}

	// Validate volumeTiers[*].discountPct.
	vtPath := field.NewPath("spec", "volumeTiers")
	for i, vt := range pb.Spec.VolumeTiers {
		if !decimalPattern.MatchString(vt.DiscountPct) {
			errs = append(errs, field.Invalid(
				vtPath.Index(i).Child("discountPct"),
				vt.DiscountPct,
				"must be a non-negative decimal string with up to 6 fractional digits",
			))
		}
	}

	// Validate committedUse[*].monthlyPrice.
	cuPath := field.NewPath("spec", "committedUse")
	for i, cu := range pb.Spec.CommittedUse {
		if !decimalPattern.MatchString(cu.MonthlyPrice) {
			errs = append(errs, field.Invalid(
				cuPath.Index(i).Child("monthlyPrice"),
				cu.MonthlyPrice,
				"must be a non-negative decimal string with up to 6 fractional digits",
			))
		}
	}

	return errs
}
