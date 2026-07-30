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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// Condition type constants for Plan status.
const (
	ConditionCompiled             = "Compiled"
	ConditionDrifted              = "Drifted"
	ConditionAdoptionConflict     = "AdoptionConflict"
	ConditionKuadrantNotInstalled = "KuadrantNotInstalled"
	ConditionGatewayNotFound      = "GatewayNotFound"
	ConditionUnsupportedField     = "UnsupportedField"
)

// PlanSpec defines the desired state of a Plan.
type PlanSpec struct {
	// Limits configures the rate limits for this plan.
	Limits PlanLimits `json:"limits"`

	// ModelScopes is a list of glob patterns for model matching.
	// Compiled into AuthPolicy CEL predicates.
	// +optional
	ModelScopes []string `json:"modelScopes,omitempty"`

	// GeoScopes is a list of geographic scopes. ["*"] means all.
	// +optional
	// +kubebuilder:default={"*"}
	GeoScopes []string `json:"geoScopes,omitempty"`

	// QueueClass is an informational annotation passed to the serving layer.
	// +optional
	QueueClass string `json:"queueClass,omitempty"`

	// TargetRef identifies the Gateway API resource this Plan attaches to.
	// Uses the standard Gateway API policy attachment pattern.
	TargetRef gatewayapiv1alpha2.LocalPolicyTargetReferenceWithSectionName `json:"targetRef"`

	// PriceBookRef is the name of the PriceBook to use for rating.
	// +optional
	PriceBookRef string `json:"priceBookRef,omitempty"`
}

// PlanLimits defines rate limit thresholds for a Plan.
type PlanLimits struct {
	// TokensPerMinute is the token rate limit per minute.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TokensPerMinute *int64 `json:"tokensPerMinute,omitempty"`

	// TokensPerDay is the token rate limit per day.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TokensPerDay *int64 `json:"tokensPerDay,omitempty"`

	// RequestsPerMinute is the request rate limit per minute.
	// +optional
	// +kubebuilder:validation:Minimum=0
	RequestsPerMinute *int32 `json:"requestsPerMinute,omitempty"`

	// MaxConcurrency is the concurrent request limit.
	// This field is not compilable to Kuadrant and is stored only.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxConcurrency *int32 `json:"maxConcurrency,omitempty"`
}

// PlanStatus defines the observed state of a Plan.
type PlanStatus struct {
	// CompiledPolicies lists the managed Kuadrant CRs produced by the PolicyCompiler.
	// +optional
	CompiledPolicies []CompiledPolicyRef `json:"compiledPolicies,omitempty"`

	// Conditions represent the latest available observations of the Plan's state.
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

// CompiledPolicyRef identifies a Kuadrant CR managed by the PolicyCompiler.
type CompiledPolicyRef struct {
	// Group is the API group of the compiled CR.
	Group string `json:"group"`

	// Kind is the kind of the compiled CR.
	Kind string `json:"kind"`

	// Name is the name of the compiled CR.
	Name string `json:"name"`

	// Namespace is the namespace of the compiled CR.
	Namespace string `json:"namespace"`

	// UpToDate indicates whether the compiled CR matches the desired state.
	UpToDate bool `json:"upToDate"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TargetKind",type=string,JSONPath=".spec.targetRef.kind",priority=1
// +kubebuilder:printcolumn:name="TargetName",type=string,JSONPath=".spec.targetRef.name",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Plan is the Schema for the plans API.
// It defines a billing plan with rate limits and model scopes that compiles
// down to Kuadrant TokenRateLimitPolicy, RateLimitPolicy, and AuthPolicy CRs.
type Plan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PlanSpec   `json:"spec,omitempty"`
	Status PlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PlanList contains a list of Plan.
type PlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Plan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Plan{}, &PlanList{})
}
