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
)

const (
	ReservationStateRequested = "Requested"
	ReservationStateConfirmed = "Confirmed"
	ReservationStateActive    = "Active"
	ReservationStateExpired   = "Expired"
	ReservationStateCancelled = "Cancelled"

	ReservationClassGuaranteed  = "Guaranteed"
	ReservationClassPreemptible = "Preemptible"

	ConditionCapacityConfirmed = "CapacityConfirmed"
	ConditionCharged           = "Charged"
	ConditionKueueSynced       = "KueueSynced"

	DefaultMinAllocationSeconds int32 = 14400 // 4 hours
	DefaultSloAdmissionSeconds  int32 = 300   // 5 minutes
)

// ReservationWindow defines the time window for a GPU reservation.
type ReservationWindow struct {
	// Start is the beginning of the reservation window.
	// +kubebuilder:validation:Required
	Start metav1.Time `json:"start"`

	// End is the end of the reservation window.
	// +kubebuilder:validation:Required
	End metav1.Time `json:"end"`
}

// CapacityCheckResult records the outcome of a capacity feasibility check.
type CapacityCheckResult struct {
	// Available indicates whether sufficient capacity was found.
	Available bool `json:"available"`

	// Pool is the capacity pool that satisfied the request.
	// +optional
	Pool string `json:"pool,omitempty"`
}

// ReservationSpec defines the desired state of a Reservation.
type ReservationSpec struct {
	// TenantRef is the name of the TenantAccount this reservation belongs to.
	// +kubebuilder:validation:Required
	TenantRef string `json:"tenantRef"`

	// SKU is the GPU product identifier (e.g. "h200", "h200-mig-3g.40gb").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	SKU string `json:"sku"`

	// Count is the number of GPUs reserved.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Count int32 `json:"count"`

	// Geo is the geographic region for this reservation.
	// +kubebuilder:validation:Required
	Geo string `json:"geo"`

	// Window defines the start and end times of the reservation.
	// +kubebuilder:validation:Required
	Window ReservationWindow `json:"window"`

	// Class determines the reservation tier.
	// Guaranteed: capacity is protected from preemption, billed regardless of utilization.
	// Preemptible: capacity may be reclaimed when Guaranteed demand exceeds supply.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Guaranteed;Preemptible
	Class string `json:"class"`

	// MinAllocationSeconds is the minimum time (in seconds) a Preemptible workload
	// must run before it can be preempted. Only applicable to Preemptible class.
	// Defaults to 14400 (4 hours) if not set.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MinAllocationSeconds *int32 `json:"minAllocationSeconds,omitempty"`

	// EvictionCreditPerGpuSecond is the credit rate applied when a Preemptible
	// workload is evicted due to capacity contention. Only applicable to
	// Preemptible class. Defaults to "0" (no credit). Positive decimal string.
	// +optional
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	EvictionCreditPerGpuSecond *string `json:"evictionCreditPerGpuSecond,omitempty"`

	// SloAdmissionSeconds is the maximum acceptable queue time (in seconds) for a
	// Guaranteed workload before an SLA credit is issued. Only applicable to
	// Guaranteed class. Defaults to 300 (5 minutes) if not set.
	// +optional
	// +kubebuilder:validation:Minimum=0
	SloAdmissionSeconds *int32 `json:"sloAdmissionSeconds,omitempty"`

	// MonthlyPrice is the fixed monthly price for this reservation as a decimal string.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\d+(\.\d{1,6})?$`
	MonthlyPrice string `json:"monthlyPrice"`
}

// ReservationStatus defines the observed state of a Reservation.
type ReservationStatus struct {
	// State is the current lifecycle state of this Reservation.
	// +kubebuilder:validation:Enum=Requested;Confirmed;Active;Expired;Cancelled
	// +optional
	State string `json:"state,omitempty"`

	// CancelledAt records when the reservation was cancelled.
	// +optional
	CancelledAt *metav1.Time `json:"cancelledAt,omitempty"`

	// CapacityCheck records the result of the most recent capacity feasibility check.
	// +optional
	CapacityCheck *CapacityCheckResult `json:"capacityCheck,omitempty"`

	// Conditions represent the latest available observations of the
	// Reservation's state.
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
// +kubebuilder:printcolumn:name="Class",type=string,JSONPath=".spec.class"
// +kubebuilder:printcolumn:name="SKU",type=string,JSONPath=".spec.sku"
// +kubebuilder:printcolumn:name="Count",type=integer,JSONPath=".spec.count"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Reservation is the Schema for the reservations API.
// It represents a calendar-committed GPU reservation with a time window,
// capacity class (Guaranteed or Preemptible), and fixed monthly pricing.
type Reservation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReservationSpec   `json:"spec,omitempty"`
	Status ReservationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReservationList contains a list of Reservation resources.
type ReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Reservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Reservation{}, &ReservationList{})
}
