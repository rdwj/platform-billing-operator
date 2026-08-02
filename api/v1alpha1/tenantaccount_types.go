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
	ConditionBalanceHealthy     = "BalanceHealthy"
	ConditionPaymentMethodValid = "PaymentMethodValid"
	ConditionBalanceStale       = "BalanceStale"

	StatePending   = "Pending"
	StateActive    = "Active"
	StateWarning   = "Warning"
	StateGrace     = "Grace"
	StateFrozen    = "Frozen"
	StateSuspended = "Suspended"
	StateClosed    = "Closed"

	BillingModeShowbackOnly = "ShowbackOnly"
	BillingModePrepaid      = "Prepaid"
	BillingModeInvoiced     = "Invoiced"
)

// ReloadPolicy configures automatic wallet reload for Prepaid tenants.
type ReloadPolicy struct {
	// Enabled controls whether automatic reload is active.
	// +kubebuilder:validation:Required
	Enabled bool `json:"enabled"`

	// ThresholdAmount is the balance level that triggers a reload.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	ThresholdAmount string `json:"thresholdAmount"`

	// ReloadAmount is the amount to add on each reload.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	ReloadAmount string `json:"reloadAmount"`

	// MonthlyCapAmount is the maximum reload spend per calendar month.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	MonthlyCapAmount string `json:"monthlyCapAmount"`
}

// GracePolicy defines the behavior when a tenant's balance drops to zero.
type GracePolicy struct {
	// GraceAmount is the dollar amount below zero a tenant is allowed before
	// enforcement fires. "0.000000" means freeze on the very next request after
	// crossing zero. The request that crossed zero still completes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	// +kubebuilder:default="0.000000"
	GraceAmount string `json:"graceAmount"`

	// WarningThreshold is the balance level at which a Warning state is entered.
	// When set and balance drops below this value, the tenant enters Warning state
	// (advisory only — no enforcement action). Optional.
	// +optional
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	WarningThreshold string `json:"warningThreshold,omitempty"`
}

// TenantAccountSpec defines the desired state of a TenantAccount.
type TenantAccountSpec struct {
	// DisplayName is the human-readable tenant name.
	// +kubebuilder:validation:Required
	DisplayName string `json:"displayName"`

	// PlanRef references the Plan CR that governs this tenant's rate limits.
	// +kubebuilder:validation:Required
	PlanRef corev1.LocalObjectReference `json:"planRef"`

	// PriceBookPin optionally pins this tenant to a specific PriceBook by name,
	// overriding the default "follow Active" behavior (grandfathering).
	// +optional
	PriceBookPin string `json:"priceBookPin,omitempty"`

	// BillingMode determines how usage is billed.
	// ShowbackOnly: usage is rated and visible but no wallet/PSP enforcement.
	// Prepaid: tenant pre-funds a wallet; usage debits the balance.
	// Invoiced: usage accumulates and is invoiced on a billing cycle.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=ShowbackOnly;Prepaid;Invoiced
	BillingMode string `json:"billingMode"`

	// PSPCustomerSecretRef references a Secret containing payment processor
	// customer data (keys: customerId, defaultPaymentMethodId).
	// +optional
	PSPCustomerSecretRef *corev1.LocalObjectReference `json:"pspCustomerSecretRef,omitempty"`

	// ReloadPolicy configures automatic wallet reload (Prepaid mode only).
	// +optional
	ReloadPolicy *ReloadPolicy `json:"reloadPolicy,omitempty"`

	// GracePolicy defines behavior when the tenant's balance drops to zero.
	// +optional
	GracePolicy *GracePolicy `json:"gracePolicy,omitempty"`
}

// TenantAccountStatus defines the observed state of a TenantAccount.
type TenantAccountStatus struct {
	// State is the current lifecycle state of this TenantAccount.
	// +kubebuilder:validation:Enum=Pending;Active;Warning;Grace;Frozen;Suspended;Closed
	// +optional
	State string `json:"state,omitempty"`

	// Balance is the current wallet balance as a decimal string, mirrored from
	// the ledger. This is NOT authoritative — the ledger (charges + adjustments)
	// is the source of truth.
	// +optional
	Balance string `json:"balance,omitempty"`

	// BalanceObservedAt is the timestamp when the balance was last computed.
	// +optional
	BalanceObservedAt *metav1.Time `json:"balanceObservedAt,omitempty"`

	// RunwayDays is the estimated days of service remaining at the current
	// spend rate, as a decimal string.
	// +optional
	RunwayDays string `json:"runwayDays,omitempty"`

	// MtdSpend is the month-to-date spend as a decimal string.
	// +optional
	MtdSpend string `json:"mtdSpend,omitempty"`

	// AllowanceTokens is the token allowance computed from balance and pricing,
	// used for enforcement. Decimal string format.
	// +optional
	AllowanceTokens string `json:"allowanceTokens,omitempty"`

	// Conditions represent the latest available observations of the
	// TenantAccount's state.
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
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=".status.state"
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=".spec.billingMode"
// +kubebuilder:printcolumn:name="Balance",type=string,JSONPath=".status.balance"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TenantAccount is the Schema for the tenantaccounts API.
// It represents a billing tenant with a wallet, billing mode, and lifecycle state.
type TenantAccount struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantAccountSpec   `json:"spec,omitempty"`
	Status TenantAccountStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TenantAccountList contains a list of TenantAccount resources.
type TenantAccountList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TenantAccount `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TenantAccount{}, &TenantAccountList{})
}
