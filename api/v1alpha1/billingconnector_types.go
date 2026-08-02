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
	ConditionCredentialsValid = "CredentialsValid"
	ConditionWebhookReachable = "WebhookReachable"
	ConditionSyncHealthy      = "SyncHealthy"

	ConnectorTypeStripe  = "stripe"
	ConnectorTypeWebhook = "webhook"
)

// WebhookIngressConfig defines how the PSP reaches our webhook endpoint.
type WebhookIngressConfig struct {
	// Host is the publicly reachable hostname for webhook delivery.
	// +kubebuilder:validation:Required
	Host string `json:"host"`

	// TLSSecretRef references a Secret containing TLS certificate and key
	// for the webhook endpoint.
	// +optional
	TLSSecretRef *corev1.LocalObjectReference `json:"tlsSecretRef,omitempty"`
}

// SyncConfig defines polling intervals for usage record and invoice sync.
type SyncConfig struct {
	// UsageRecordsInterval is the interval for pushing usage records to the PSP.
	// +optional
	// +kubebuilder:default="5m"
	UsageRecordsInterval string `json:"usageRecordsInterval,omitempty"`

	// InvoiceInterval is the interval for syncing invoice state with the PSP.
	// +optional
	// +kubebuilder:default="1h"
	InvoiceInterval string `json:"invoiceInterval,omitempty"`
}

// LastSyncStatus records the result of the most recent sync operation.
type LastSyncStatus struct {
	// At is the timestamp of the last sync.
	// +optional
	At *metav1.Time `json:"at,omitempty"`

	// PushedRecords is the number of records pushed in the last sync.
	// +optional
	PushedRecords int64 `json:"pushedRecords,omitempty"`

	// Failed is the number of records that failed to push.
	// +optional
	Failed int64 `json:"failed,omitempty"`
}

// WebhookStatsStatus records webhook delivery statistics.
type WebhookStatsStatus struct {
	// Received24h is the number of webhooks received in the last 24 hours.
	// +optional
	Received24h int64 `json:"received24h,omitempty"`

	// SignatureFailures is the number of webhook signature verification failures
	// in the last 24 hours.
	// +optional
	SignatureFailures int64 `json:"signatureFailures,omitempty"`
}

// BillingConnectorSpec defines the desired state of a BillingConnector.
type BillingConnectorSpec struct {
	// Type is the payment service provider type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=stripe;webhook
	Type string `json:"type"`

	// CredentialsSecretRef references a Secret containing PSP credentials.
	// For Stripe: keys "secretKey", "webhookSecret".
	// For webhook: key "hmacSecret".
	// +kubebuilder:validation:Required
	CredentialsSecretRef corev1.LocalObjectReference `json:"credentialsSecretRef"`

	// WebhookIngress configures the publicly reachable endpoint for PSP
	// webhook delivery.
	// +optional
	WebhookIngress *WebhookIngressConfig `json:"webhookIngress,omitempty"`

	// Sync configures polling intervals for usage and invoice sync.
	// +optional
	Sync *SyncConfig `json:"sync,omitempty"`

	// TaxMode controls whether tax computation is delegated to the PSP.
	// +optional
	// +kubebuilder:validation:Enum=psp;none
	// +kubebuilder:default="none"
	TaxMode string `json:"taxMode,omitempty"`
}

// BillingConnectorStatus defines the observed state of a BillingConnector.
type BillingConnectorStatus struct {
	// Connected indicates whether the connector has successfully authenticated
	// with the PSP.
	// +optional
	Connected bool `json:"connected,omitempty"`

	// LastSync records the result of the most recent sync operation.
	// +optional
	LastSync *LastSyncStatus `json:"lastSync,omitempty"`

	// WebhookStats records webhook delivery statistics.
	// +optional
	WebhookStats *WebhookStatsStatus `json:"webhookStats,omitempty"`

	// Conditions represent the latest available observations of the
	// BillingConnector's state.
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
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Connected",type=boolean,JSONPath=".status.connected"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// BillingConnector is the Schema for the billingconnectors API.
// It configures integration with a payment service provider (Stripe, generic
// webhook, etc.) for the psp-connector binary.
type BillingConnector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BillingConnectorSpec   `json:"spec,omitempty"`
	Status BillingConnectorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BillingConnectorList contains a list of BillingConnector resources.
type BillingConnectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BillingConnector `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BillingConnector{}, &BillingConnectorList{})
}
