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
	"time"

	"github.com/go-logr/logr"
	"github.com/shopspring/decimal"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
	"github.com/rdwj/platform-billing-operator/internal/metrics"
	"github.com/rdwj/platform-billing-operator/internal/rater"
)

const (
	annotationSLOChecked = "billing.opendatahub.io/slo-checked"
	annotationValueTrue  = "true"
)

// SLOController watches Kueue Workloads and checks admission latency against
// the SLO threshold on Guaranteed GPU reservations. When latency exceeds the
// threshold, it posts an sla_credit adjustment to the ledger.
type SLOController struct {
	client.Client
	Log              logr.Logger
	AdjustmentPoster AdjustmentPoster
	Resolver         *rater.K8sResolver
}

// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=workloads,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=kueue.x-k8s.io,resources=clusterqueues,verbs=get
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=reservations,verbs=list
// +kubebuilder:rbac:groups=billing.opendatahub.io,resources=pricebooks,verbs=list

func (s *SLOController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := s.Log.WithValues("workload", req.NamespacedName)

	var wl kueuev1beta1.Workload
	if err := s.Get(ctx, req.NamespacedName, &wl); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if wl.Status.Admission == nil {
		return ctrl.Result{}, nil
	}

	if wl.Annotations[annotationSLOChecked] == annotationValueTrue {
		return ctrl.Result{}, nil
	}

	cqName := string(wl.Status.Admission.ClusterQueue)
	var cq kueuev1beta1.ClusterQueue
	if err := s.Get(ctx, client.ObjectKey{Name: cqName}, &cq); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cq.Labels[labelReservationClass] != billingv1alpha1.ReservationClassGuaranteed {
		return ctrl.Result{}, nil
	}
	if cq.Labels[labelManagedBy] != managedByReservation {
		return ctrl.Result{}, nil
	}

	admittedCond := findCondition(wl.Status.Conditions, "Admitted")
	if admittedCond == nil || admittedCond.Status != metav1.ConditionTrue {
		return ctrl.Result{}, nil
	}

	tenantID := cq.Labels[labelTenant]
	sku := cq.Annotations[annotationSKU]
	latency := admittedCond.LastTransitionTime.Sub(wl.CreationTimestamp.Time)
	latencySecs := latency.Seconds()

	metrics.AdmissionLatencySeconds.WithLabelValues(tenantID, sku).Observe(latencySecs)

	threshold := s.lookupSLOThreshold(ctx, tenantID)
	thresholdDur := time.Duration(threshold) * time.Second

	if latency > thresholdDur && s.AdjustmentPoster != nil && s.Resolver != nil {
		excessSecs := (latency - thresholdDur).Seconds()
		gpuCount := workloadGPUCount(&wl)

		amount, err := s.computeCredit(ctx, sku, gpuCount, excessSecs)
		if err != nil {
			log.Error(err, "failed to compute SLA credit (best-effort)", "tenant", tenantID)
		} else if amount != "" {
			if postErr := s.AdjustmentPoster.PostSLACredit(ctx, tenantID, amount, string(wl.UID), latencySecs); postErr != nil {
				log.Error(postErr, "failed to post SLA credit (best-effort)", "tenant", tenantID, "amount", amount)
			} else {
				log.Info("SLO breach: posted sla_credit", "tenant", tenantID, "amount", amount,
					"latency_s", latencySecs, "threshold_s", threshold)
			}
			metrics.SLOBreachTotal.WithLabelValues(tenantID, sku).Inc()
		}
	}

	if wl.Annotations == nil {
		wl.Annotations = make(map[string]string)
	}
	wl.Annotations[annotationSLOChecked] = annotationValueTrue
	if err := s.Update(ctx, &wl); err != nil {
		return ctrl.Result{}, fmt.Errorf("annotating workload: %w", err)
	}

	return ctrl.Result{}, nil
}

func (s *SLOController) lookupSLOThreshold(ctx context.Context, tenantID string) int32 {
	var reservations billingv1alpha1.ReservationList
	if err := s.List(ctx, &reservations); err != nil {
		return billingv1alpha1.DefaultSloAdmissionSeconds
	}

	threshold := billingv1alpha1.DefaultSloAdmissionSeconds
	found := false
	for _, res := range reservations.Items {
		if res.Spec.TenantRef != tenantID || res.Spec.Class != billingv1alpha1.ReservationClassGuaranteed {
			continue
		}
		switch res.Status.State {
		case billingv1alpha1.ReservationStateConfirmed, billingv1alpha1.ReservationStateActive:
		default:
			continue
		}
		if res.Spec.SloAdmissionSeconds != nil {
			if !found || *res.Spec.SloAdmissionSeconds < threshold {
				threshold = *res.Spec.SloAdmissionSeconds
				found = true
			}
		}
	}
	return threshold
}

func (s *SLOController) computeCredit(ctx context.Context, sku string, gpuCount int64, excessSecs float64) (string, error) {
	guaranteedSKU := sku + "-guaranteed"
	pb, err := s.Resolver.Resolve(ctx, guaranteedSKU, time.Now())
	if err != nil {
		return "", fmt.Errorf("resolve PriceBook for %s: %w", guaranteedSKU, err)
	}

	skuDef, ok := pb.SKUs[guaranteedSKU]
	if !ok {
		return "", fmt.Errorf("SKU %s not found in resolved PriceBook", guaranteedSKU)
	}

	comp, ok := skuDef.Components["gpu_seconds"]
	if !ok {
		comp, ok = skuDef.Components["gpu_months"]
	}
	if !ok {
		return "", fmt.Errorf("no gpu_seconds or gpu_months component for SKU %s", guaranteedSKU)
	}

	rateStr := comp.RatePerUnit
	if rateStr == "" {
		return "", fmt.Errorf("no RatePerUnit for SKU %s", guaranteedSKU)
	}

	rate, err := decimal.NewFromString(rateStr)
	if err != nil {
		return "", fmt.Errorf("parse rate %q: %w", rateStr, err)
	}

	amount := rate.Mul(decimal.NewFromInt(gpuCount)).Mul(decimal.NewFromFloat(excessSecs))
	return amount.StringFixed(6), nil
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func (s *SLOController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kueuev1beta1.Workload{}).
		Named("gpu-slo").
		Complete(s)
}
