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

package psp

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// RealK8sStatusUpdater implements K8sStatusUpdater by reading and patching
// TenantAccount status via the controller-runtime client.
type RealK8sStatusUpdater struct {
	Client    client.Client
	Namespace string
}

var _ K8sStatusUpdater = (*RealK8sStatusUpdater)(nil)

func (u *RealK8sStatusUpdater) PatchPaymentSucceeded(ctx context.Context, tenant string) error {
	var acct billingv1alpha1.TenantAccount
	if err := u.Client.Get(ctx, types.NamespacedName{Name: tenant, Namespace: u.Namespace}, &acct); err != nil {
		return fmt.Errorf("get TenantAccount %s: %w", tenant, err)
	}
	acct.Status.ConsecutivePaymentFailures = 0
	acct.Status.PendingReloadIntentID = ""
	return u.Client.Status().Update(ctx, &acct)
}

func (u *RealK8sStatusUpdater) PatchPaymentFailed(ctx context.Context, tenant string) error {
	var acct billingv1alpha1.TenantAccount
	if err := u.Client.Get(ctx, types.NamespacedName{Name: tenant, Namespace: u.Namespace}, &acct); err != nil {
		return fmt.Errorf("get TenantAccount %s: %w", tenant, err)
	}
	acct.Status.ConsecutivePaymentFailures++
	acct.Status.PendingReloadIntentID = ""
	return u.Client.Status().Update(ctx, &acct)
}

func (u *RealK8sStatusUpdater) PatchPaymentDisputed(ctx context.Context, tenant, intentID string) error {
	var acct billingv1alpha1.TenantAccount
	if err := u.Client.Get(ctx, types.NamespacedName{Name: tenant, Namespace: u.Namespace}, &acct); err != nil {
		return fmt.Errorf("get TenantAccount %s: %w", tenant, err)
	}
	meta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionPaymentDisputed,
		Status:             metav1.ConditionTrue,
		Reason:             "Disputed",
		Message:            fmt.Sprintf("Payment intent %s disputed", intentID),
		LastTransitionTime: metav1.NewTime(time.Now()),
	})
	return u.Client.Status().Update(ctx, &acct)
}

func (u *RealK8sStatusUpdater) PatchCorroborationFailed(ctx context.Context, tenant, intentID string) error {
	var acct billingv1alpha1.TenantAccount
	if err := u.Client.Get(ctx, types.NamespacedName{Name: tenant, Namespace: u.Namespace}, &acct); err != nil {
		return fmt.Errorf("get TenantAccount %s: %w", tenant, err)
	}
	meta.SetStatusCondition(&acct.Status.Conditions, metav1.Condition{
		Type:               billingv1alpha1.ConditionPaymentCorroborationFailed,
		Status:             metav1.ConditionTrue,
		Reason:             "CorroborationFailed",
		Message:            fmt.Sprintf("Corroboration failed for intent %s", intentID),
		LastTransitionTime: metav1.NewTime(time.Now()),
	})
	return u.Client.Status().Update(ctx, &acct)
}
