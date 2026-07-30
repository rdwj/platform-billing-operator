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

// PassThroughConfig defines pass-through pricing where rates are derived from
// an upstream provider's costs plus a markup.
type PassThroughConfig struct {
	// Provider is the upstream provider name (e.g. "openai", "anthropic").
	// +kubebuilder:validation:Required
	Provider string `json:"provider"`

	// MarkupPct is the markup percentage applied on top of provider cost,
	// expressed as a decimal string (e.g. "15.0" for 15%).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	MarkupPct string `json:"markupPct"`

	// CostSecretRef references a Secret containing the provider's cost data.
	// +kubebuilder:validation:Required
	CostSecretRef corev1.LocalObjectReference `json:"costSecretRef"`
}

// SKU defines pricing for a single model or product SKU.
type SKU struct {
	// SKU is the model or product identifier (e.g. "gpt-4o", "llama-3.1-70b").
	// +kubebuilder:validation:Required
	SKUName string `json:"sku"`

	// Unit is the pricing unit (e.g. "per-1M-tokens", "per-gpu-hour").
	// +kubebuilder:validation:Required
	Unit string `json:"unit"`

	// Geo is the geographic region this pricing applies to.
	// Empty string means this is the default rate for all regions.
	// +optional
	Geo string `json:"geo,omitempty"`

	// Rates maps quantity names to decimal-string rates.
	// Keys are usage dimensions (e.g. "prompt_tokens", "completion_tokens",
	// "cached_tokens", "reasoning_tokens", "requests").
	// Values are decimal strings validated by the admission webhook.
	// +kubebuilder:validation:Required
	Rates map[string]string `json:"rates"`

	// PassThrough configures pass-through pricing derived from upstream costs.
	// +optional
	PassThrough *PassThroughConfig `json:"passThrough,omitempty"`
}

// VolumeTier defines a volume-based discount tier.
type VolumeTier struct {
	// ThresholdTokens is the monthly token count at which this tier activates.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	ThresholdTokens int64 `json:"thresholdTokens"`

	// DiscountPct is the discount percentage for this tier as a decimal string
	// (e.g. "10.0" for 10%).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	DiscountPct string `json:"discountPct"`
}

// CommittedUseTier defines a committed-use pricing tier where tenants
// pre-commit to a sustained throughput level in exchange for a fixed monthly rate.
type CommittedUseTier struct {
	// Name is the human-readable tier name (e.g. "starter", "growth", "enterprise").
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// TokensPerSecond is the committed throughput in tokens per second.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	TokensPerSecond int64 `json:"tokensPerSecond"`

	// MonthlyPrice is the fixed monthly price for this tier as a decimal string.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	MonthlyPrice string `json:"monthlyPrice"`
}

// PriceBookSpec defines the desired state of a PriceBook.
type PriceBookSpec struct {
	// Currency is the ISO 4217 currency code for all rates in this PriceBook.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=USD;EUR;GBP;JPY;CAD;AUD;CHF
	Currency string `json:"currency"`

	// EffectiveFrom is the timestamp when this PriceBook becomes Active.
	// +kubebuilder:validation:Required
	EffectiveFrom metav1.Time `json:"effectiveFrom"`

	// NoticeDays is the minimum notice period (in days) tenants receive
	// before price changes take effect.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	NoticeDays int32 `json:"noticeDays"`

	// Supersedes is the name of the prior PriceBook this one replaces.
	// +optional
	Supersedes string `json:"supersedes,omitempty"`

	// SKUs is the list of SKU pricing entries. At least one SKU is required.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	SKUs []SKU `json:"skus"`

	// VolumeTiers defines volume-based discount tiers applied to aggregate usage.
	// +optional
	VolumeTiers []VolumeTier `json:"volumeTiers,omitempty"`

	// CommittedUse defines committed-use pricing tiers for sustained throughput.
	// +optional
	CommittedUse []CommittedUseTier `json:"committedUse,omitempty"`
}

// PriceBookStatus defines the observed state of a PriceBook.
type PriceBookStatus struct {
	// State is the current lifecycle state of this PriceBook.
	// +kubebuilder:validation:Enum=Draft;Announced;Active;Superseded;Retired
	// +optional
	State string `json:"state,omitempty"`

	// ActiveTenants is the count of TenantAccounts currently using this PriceBook.
	// +optional
	ActiveTenants int32 `json:"activeTenants,omitempty"`

	// GrandfatheredTenants lists tenant names that are pinned to this PriceBook
	// via TenantAccount.spec.priceBookPin and will not be migrated automatically.
	// +optional
	GrandfatheredTenants []string `json:"grandfatheredTenants,omitempty"`

	// Conditions represent the latest available observations of the PriceBook's state.
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
// +kubebuilder:printcolumn:name="Effective",type=date,JSONPath=".spec.effectiveFrom"
// +kubebuilder:printcolumn:name="Currency",type=string,JSONPath=".spec.currency"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// PriceBook is the Schema for the pricebooks API.
// It defines the catalog of SKU rates, volume discounts, and committed-use tiers
// that govern how tenant usage is rated and invoiced.
type PriceBook struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PriceBookSpec   `json:"spec,omitempty"`
	Status PriceBookStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PriceBookList contains a list of PriceBook resources.
type PriceBookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PriceBook `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PriceBook{}, &PriceBookList{})
}
