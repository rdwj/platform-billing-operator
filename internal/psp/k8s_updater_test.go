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

package psp_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/psp"
)

const testNamespace = "test-ns"

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = billingv1alpha1.AddToScheme(scheme)
	return scheme
}

func newTenantAccount(failures int32, pendingIntent string) *billingv1alpha1.TenantAccount {
	return &billingv1alpha1.TenantAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-tenant",
			Namespace: testNamespace,
		},
		Status: billingv1alpha1.TenantAccountStatus{
			ConsecutivePaymentFailures: failures,
			PendingReloadIntentID:      pendingIntent,
		},
	}
}

func TestRealK8sStatusUpdater_PatchPaymentSucceeded(t *testing.T) {
	acct := newTenantAccount(3, "intent-1")
	scheme := newTestScheme()
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(acct).
		WithStatusSubresource(&billingv1alpha1.TenantAccount{}).
		Build()

	updater := &psp.RealK8sStatusUpdater{Client: k8sClient, Namespace: testNamespace}
	err := updater.PatchPaymentSucceeded(context.Background(), "test-tenant")
	require.NoError(t, err)

	var updated billingv1alpha1.TenantAccount
	err = k8sClient.Get(context.Background(), types.NamespacedName{Name: "test-tenant", Namespace: testNamespace}, &updated)
	require.NoError(t, err)

	assert.Equal(t, int32(0), updated.Status.ConsecutivePaymentFailures)
	assert.Equal(t, "", updated.Status.PendingReloadIntentID)
}

func TestRealK8sStatusUpdater_PatchPaymentFailed(t *testing.T) {
	acct := newTenantAccount(1, "intent-2")
	scheme := newTestScheme()
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(acct).
		WithStatusSubresource(&billingv1alpha1.TenantAccount{}).
		Build()

	updater := &psp.RealK8sStatusUpdater{Client: k8sClient, Namespace: testNamespace}
	err := updater.PatchPaymentFailed(context.Background(), "test-tenant")
	require.NoError(t, err)

	var updated billingv1alpha1.TenantAccount
	err = k8sClient.Get(context.Background(), types.NamespacedName{Name: "test-tenant", Namespace: testNamespace}, &updated)
	require.NoError(t, err)

	assert.Equal(t, int32(2), updated.Status.ConsecutivePaymentFailures)
	assert.Equal(t, "", updated.Status.PendingReloadIntentID)
}

func TestRealK8sStatusUpdater_PatchPaymentDisputed(t *testing.T) {
	acct := newTenantAccount(0, "")
	scheme := newTestScheme()
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(acct).
		WithStatusSubresource(&billingv1alpha1.TenantAccount{}).
		Build()

	updater := &psp.RealK8sStatusUpdater{Client: k8sClient, Namespace: testNamespace}
	err := updater.PatchPaymentDisputed(context.Background(), "test-tenant", "pi_disputed_1")
	require.NoError(t, err)

	var updated billingv1alpha1.TenantAccount
	err = k8sClient.Get(context.Background(), types.NamespacedName{Name: "test-tenant", Namespace: testNamespace}, &updated)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(updated.Status.Conditions, billingv1alpha1.ConditionPaymentDisputed)
	require.NotNil(t, cond, "PaymentDisputed condition should be set")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "pi_disputed_1")
}

func TestRealK8sStatusUpdater_PatchCorroborationFailed(t *testing.T) {
	acct := newTenantAccount(0, "")
	scheme := newTestScheme()
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(acct).
		WithStatusSubresource(&billingv1alpha1.TenantAccount{}).
		Build()

	updater := &psp.RealK8sStatusUpdater{Client: k8sClient, Namespace: testNamespace}
	err := updater.PatchCorroborationFailed(context.Background(), "test-tenant", "pi_corrob_1")
	require.NoError(t, err)

	var updated billingv1alpha1.TenantAccount
	err = k8sClient.Get(context.Background(), types.NamespacedName{Name: "test-tenant", Namespace: testNamespace}, &updated)
	require.NoError(t, err)

	cond := meta.FindStatusCondition(updated.Status.Conditions, billingv1alpha1.ConditionPaymentCorroborationFailed)
	require.NotNil(t, cond, "PaymentCorroborationFailed condition should be set")
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Contains(t, cond.Message, "pi_corrob_1")
}

func TestRealK8sStatusUpdater_NotFound(t *testing.T) {
	scheme := newTestScheme()
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&billingv1alpha1.TenantAccount{}).
		Build()

	updater := &psp.RealK8sStatusUpdater{Client: k8sClient, Namespace: testNamespace}

	err := updater.PatchPaymentSucceeded(context.Background(), "nonexistent-tenant")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent-tenant")
}
