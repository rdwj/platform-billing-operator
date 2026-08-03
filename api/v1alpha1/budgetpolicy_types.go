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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionEvaluating     = "Evaluating"
	ConditionWithinBudget   = "WithinBudget"
	ConditionThresholdFired = "ThresholdFired"

	PeriodMonthly = "Monthly"
	PeriodWeekly  = "Weekly"
	PeriodDaily   = "Daily"

	ThresholdActionNotify   = "Notify"
	ThresholdActionThrottle = "Throttle"
	ThresholdActionFreeze   = "Freeze"
)

// BudgetSelector scopes the budget to a subset of the tenant's API keys.
type BudgetSelector struct {
	// KeyLabels matches API keys carrying these labels.
	// +optional
	KeyLabels map[string]string `json:"keyLabels,omitempty"`
}

// BudgetThreshold defines a percentage trigger point within a cap.
type BudgetThreshold struct {
	// Pct is the percentage of the cap amount at which this threshold fires.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	Pct int32 `json:"pct"`

	// Action is the enforcement action to take when this threshold fires.
	// Notify always fires alongside Throttle or Freeze.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Notify;Throttle;Freeze
	Action string `json:"action"`
}

// BudgetCap defines a spending cap with thresholds.
type BudgetCap struct {
	// Amount is the cap amount as a decimal string.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	Amount string `json:"amount"`

	// Period is the time window for this cap.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Monthly;Weekly;Daily
	Period string `json:"period"`

	// Thresholds are the percentage trigger points within this cap.
	// +kubebuilder:validation:MinItems=1
	Thresholds []BudgetThreshold `json:"thresholds"`
}

// BudgetPolicySpec defines the desired state of a BudgetPolicy.
type BudgetPolicySpec struct {
	// TenantRef references the TenantAccount this budget applies to.
	// +kubebuilder:validation:Required
	TenantRef corev1.LocalObjectReference `json:"tenantRef"`

	// Selector optionally scopes the budget to a subset of the tenant's keys.
	// +optional
	Selector *BudgetSelector `json:"selector,omitempty"`

	// Caps defines the spending caps with thresholds.
	// +kubebuilder:validation:MinItems=1
	Caps []BudgetCap `json:"caps"`
}

// FiredThreshold records a threshold that has already fired in the current period.
type FiredThreshold struct {
	// CapIndex identifies which cap in the spec this threshold belongs to.
	CapIndex int32 `json:"capIndex"`

	// Pct is the threshold percentage that fired.
	Pct int32 `json:"pct"`

	// FiredAt is the timestamp when the threshold fired.
	FiredAt metav1.Time `json:"firedAt"`
}

// CapPeriodEnd tracks the period boundary for a single cap.
type CapPeriodEnd struct {
	// CapIndex identifies which cap in the spec this entry belongs to.
	CapIndex int32 `json:"capIndex"`

	// PeriodEnd is the end of the current evaluation period for this cap (UTC).
	PeriodEnd metav1.Time `json:"periodEnd"`
}

// BudgetPolicyStatus defines the observed state of a BudgetPolicy.
type BudgetPolicyStatus struct {
	// CurrentSpend is the current period spend as a decimal string.
	// Reflects the last-evaluated cap's spend.
	// +optional
	CurrentSpend string `json:"currentSpend,omitempty"`

	// CapPeriodEnds tracks the period boundary for each cap independently,
	// allowing caps with different periods (Daily/Weekly/Monthly) to coexist.
	// +optional
	CapPeriodEnds []CapPeriodEnd `json:"capPeriodEnds,omitempty"`

	// FiredThresholds tracks which thresholds have fired in the current period.
	// Reset on period rollover per cap.
	// +optional
	FiredThresholds []FiredThreshold `json:"firedThresholds,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=".spec.tenantRef.name"
// +kubebuilder:printcolumn:name="Spend",type=string,JSONPath=".status.currentSpend"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// BudgetPolicy is the Schema for the budgetpolicies API.
// It defines spending caps and threshold-based actions for a TenantAccount.
type BudgetPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BudgetPolicySpec   `json:"spec,omitempty"`
	Status BudgetPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BudgetPolicyList contains a list of BudgetPolicy resources.
type BudgetPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BudgetPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BudgetPolicy{}, &BudgetPolicyList{})
}
