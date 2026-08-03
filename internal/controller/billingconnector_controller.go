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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/psp"
)

// BillingConnectorReconciler reconciles BillingConnector CRs by reading
// credentials from the referenced Secret and bootstrapping a PSPBackend
// instance in the in-process BackendRegistry.
type BillingConnectorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *BackendRegistry
}

// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=billingconnectors,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=billingconnectors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *BillingConnectorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var bc billingv1alpha1.BillingConnector
	if err := r.Get(ctx, req.NamespacedName, &bc); err != nil {
		if apierrors.IsNotFound(err) {
			r.Registry.Delete(req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion: remove from registry.
	if !bc.DeletionTimestamp.IsZero() {
		r.Registry.Delete(bc.Name)
		return ctrl.Result{}, nil
	}

	// Fetch the credentials Secret.
	var secret corev1.Secret
	secretKey := types.NamespacedName{
		Name:      bc.Spec.CredentialsSecretRef.Name,
		Namespace: bc.Namespace,
	}
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			bc.Status.Connected = false
			apimeta.SetStatusCondition(&bc.Status.Conditions, metav1.Condition{
				Type:               billingv1alpha1.ConditionCredentialsValid,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: bc.Generation,
				Reason:             "SecretNotFound",
				Message:            fmt.Sprintf("Secret %q not found in namespace %s", bc.Spec.CredentialsSecretRef.Name, bc.Namespace),
			})
			bc.Status.ObservedGeneration = bc.Generation
			if err := r.Status().Update(ctx, &bc); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Validate required keys based on connector type.
	switch bc.Spec.Type {
	case billingv1alpha1.ConnectorTypeStripe:
		sk := secret.Data["secretKey"]
		ws := secret.Data["webhookSecret"]
		if len(sk) == 0 || len(ws) == 0 {
			bc.Status.Connected = false
			apimeta.SetStatusCondition(&bc.Status.Conditions, metav1.Condition{
				Type:               billingv1alpha1.ConditionCredentialsValid,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: bc.Generation,
				Reason:             "MissingKeys",
				Message:            "Secret must contain non-empty \"secretKey\" and \"webhookSecret\" keys",
			})
			bc.Status.ObservedGeneration = bc.Generation
			if err := r.Status().Update(ctx, &bc); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// Construct and register the Stripe backend.
		backend := psp.NewStripeBackend(string(sk), string(ws))
		r.Registry.Set(bc.Name, backend)

		bc.Status.Connected = true
		apimeta.SetStatusCondition(&bc.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionCredentialsValid,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: bc.Generation,
			Reason:             "Valid",
			Message:            "Credentials validated and backend registered",
		})

	case billingv1alpha1.ConnectorTypeWebhook:
		hmacKey := secret.Data["hmacSecret"]
		if len(hmacKey) == 0 {
			bc.Status.Connected = false
			apimeta.SetStatusCondition(&bc.Status.Conditions, metav1.Condition{
				Type:               billingv1alpha1.ConditionCredentialsValid,
				Status:             metav1.ConditionFalse,
				ObservedGeneration: bc.Generation,
				Reason:             "MissingKeys",
				Message:            `Secret must contain a non-empty "hmacSecret" key`,
			})
			break
		}

		backend := psp.NewWebhookBackend(string(hmacKey))
		r.Registry.Set(bc.Name, backend)

		bc.Status.Connected = true
		apimeta.SetStatusCondition(&bc.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionCredentialsValid,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: bc.Generation,
			Reason:             "Valid",
			Message:            "Credentials validated and backend registered",
		})

	default:
		bc.Status.Connected = false
		apimeta.SetStatusCondition(&bc.Status.Conditions, metav1.Condition{
			Type:               billingv1alpha1.ConditionCredentialsValid,
			Status:             metav1.ConditionFalse,
			ObservedGeneration: bc.Generation,
			Reason:             "UnknownType",
			Message:            fmt.Sprintf("Unknown connector type %q", bc.Spec.Type),
		})
	}

	bc.Status.ObservedGeneration = bc.Generation
	if err := r.Status().Update(ctx, &bc); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("BillingConnector reconciled", "type", bc.Spec.Type, "connected", bc.Status.Connected)
	return ctrl.Result{}, nil
}

// SetupWithManager registers the BillingConnectorReconciler with the controller manager.
func (r *BillingConnectorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&billingv1alpha1.BillingConnector{}).
		Named("billingconnector").
		Complete(r)
}
