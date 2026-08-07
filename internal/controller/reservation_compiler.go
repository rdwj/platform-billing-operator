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
	"strings"
	"time"

	"github.com/shopspring/decimal"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kueuev1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

const (
	labelReservationClass    = "billing.opendatahub.io/reservation-class"
	managedByReservation     = "reservation-controller"
	labelTenant              = "billing.opendatahub.io/tenant-id"
	annotationSKU            = "billing.opendatahub.io/reservation-sku"
	annotationGeo            = "billing.opendatahub.io/reservation-geo"
	gpuResourceKey           = corev1.ResourceName("nvidia.com/gpu")
	AnnotationEvictionReason = "billing.opendatahub.io/eviction-reason"
)

// EvictionInfo describes a single workload evicted by the billing controller.
type EvictionInfo struct {
	WorkloadUID string
	TenantID    string
	GPUCount    int64
	RuntimeSecs float64
}

// EvictionCredit is a computed credit to post to the ledger for an evicted workload.
type EvictionCredit struct {
	TenantID    string
	Amount      string
	WorkloadUID string
	RuntimeSecs float64
}

// compileKueueState ensures Kueue ClusterQueues reflect the active reservations
// for a tenant. It creates or updates CQs for Guaranteed and Preemptible classes,
// and deletes CQs that no longer have active reservations.
func compileKueueState(ctx context.Context, c client.Client, tenantRef string) error {
	var allRes billingv1alpha1.ReservationList
	if err := c.List(ctx, &allRes); err != nil {
		return fmt.Errorf("listing reservations: %w", err)
	}

	// Per-class accumulation. Currently assumes one SKU+geo per tenant per class.
	// Multi-SKU support would require one CQ per (tenant, class, SKU).
	guaranteed := make(map[string]int32) // sku -> total count
	preemptible := make(map[string]int32)
	var guaranteedSKU, guaranteedGeo string
	var preemptibleSKU, preemptibleGeo string

	for _, res := range allRes.Items {
		if res.Spec.TenantRef != tenantRef {
			continue
		}
		switch res.Status.State {
		case billingv1alpha1.ReservationStateConfirmed, billingv1alpha1.ReservationStateActive:
		default:
			continue
		}
		switch res.Spec.Class {
		case billingv1alpha1.ReservationClassGuaranteed:
			guaranteed[res.Spec.SKU] += res.Spec.Count
			guaranteedSKU = res.Spec.SKU
			guaranteedGeo = res.Spec.Geo
		case billingv1alpha1.ReservationClassPreemptible:
			preemptible[res.Spec.SKU] += res.Spec.Count
			preemptibleSKU = res.Spec.SKU
			preemptibleGeo = res.Spec.Geo
		}
	}

	guaranteedTotal := int32(0)
	for _, v := range guaranteed {
		guaranteedTotal += v
	}
	preemptibleTotal := int32(0)
	for _, v := range preemptible {
		preemptibleTotal += v
	}

	cqNameGuaranteed := tenantRef + "-guaranteed"
	cqNamePreemptible := tenantRef + "-preemptible"

	if guaranteedTotal > 0 {
		flavorName, err := findFlavorForSKU(ctx, c, guaranteedSKU)
		if err != nil {
			return fmt.Errorf("finding flavor for guaranteed SKU %s: %w", guaranteedSKU, err)
		}
		labels := map[string]string{
			labelTenant:           tenantRef,
			labelReservationClass: billingv1alpha1.ReservationClassGuaranteed,
			labelManagedBy:        managedByReservation,
		}
		if err := ensureClusterQueue(ctx, c, cqNameGuaranteed, labels, flavorName, guaranteedSKU, guaranteedGeo, guaranteedTotal, billingv1alpha1.ReservationClassGuaranteed); err != nil {
			return fmt.Errorf("ensuring guaranteed CQ: %w", err)
		}
	} else {
		if err := deleteClusterQueueIfExists(ctx, c, cqNameGuaranteed); err != nil {
			return fmt.Errorf("cleaning up guaranteed CQ: %w", err)
		}
	}

	if preemptibleTotal > 0 {
		flavorName, err := findFlavorForSKU(ctx, c, preemptibleSKU)
		if err != nil {
			return fmt.Errorf("finding flavor for preemptible SKU %s: %w", preemptibleSKU, err)
		}
		labels := map[string]string{
			labelTenant:           tenantRef,
			labelReservationClass: billingv1alpha1.ReservationClassPreemptible,
			labelManagedBy:        managedByReservation,
		}
		if err := ensureClusterQueue(ctx, c, cqNamePreemptible, labels, flavorName, preemptibleSKU, preemptibleGeo, preemptibleTotal, billingv1alpha1.ReservationClassPreemptible); err != nil {
			return fmt.Errorf("ensuring preemptible CQ: %w", err)
		}
	} else {
		if err := deleteClusterQueueIfExists(ctx, c, cqNamePreemptible); err != nil {
			return fmt.Errorf("cleaning up preemptible CQ: %w", err)
		}
	}

	return nil
}

func ensureClusterQueue(ctx context.Context, c client.Client, name string, labels map[string]string, flavorName, sku, geo string, quota int32, _ string) error {
	desiredQuota := resource.MustParse(fmt.Sprintf("%d", quota))
	var existing kueuev1beta1.ClusterQueue
	err := c.Get(ctx, client.ObjectKey{Name: name}, &existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("getting CQ %s: %w", name, err)
		}
		cq := &kueuev1beta1.ClusterQueue{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Labels:      labels,
				Annotations: map[string]string{annotationSKU: sku, annotationGeo: geo},
			},
			Spec: kueuev1beta1.ClusterQueueSpec{
				NamespaceSelector: &metav1.LabelSelector{},
				ResourceGroups: []kueuev1beta1.ResourceGroup{{
					CoveredResources: []corev1.ResourceName{"cpu", "memory", gpuResourceKey},
					Flavors: []kueuev1beta1.FlavorQuotas{{
						Name: kueuev1beta1.ResourceFlavorReference(flavorName),
						Resources: []kueuev1beta1.ResourceQuota{
							{Name: "cpu", NominalQuota: resource.MustParse("8")},
							{Name: "memory", NominalQuota: resource.MustParse("32Gi")},
							{Name: gpuResourceKey, NominalQuota: desiredQuota},
						},
					}},
				}},
			},
		}
		// BorrowingLimit=0 is only valid when the CQ is in a cohort.
		// Without a cohort, Kueue rejects it. Omit for now — a standalone
		// CQ can't borrow regardless.
		return c.Create(ctx, cq)
	}

	updated := false
	for i := range existing.Spec.ResourceGroups {
		for j := range existing.Spec.ResourceGroups[i].Flavors {
			for k := range existing.Spec.ResourceGroups[i].Flavors[j].Resources {
				r := &existing.Spec.ResourceGroups[i].Flavors[j].Resources[k]
				if r.Name == gpuResourceKey && !r.NominalQuota.Equal(desiredQuota) {
					r.NominalQuota = desiredQuota
					updated = true
				}
			}
		}
	}
	if updated {
		return c.Update(ctx, &existing)
	}
	return nil
}

func deleteClusterQueueIfExists(ctx context.Context, c client.Client, name string) error {
	var cq kueuev1beta1.ClusterQueue
	if err := c.Get(ctx, client.ObjectKey{Name: name}, &cq); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if cq.Labels[labelManagedBy] != managedByReservation {
		return nil
	}
	return c.Delete(ctx, &cq)
}

func findFlavorForSKU(ctx context.Context, c client.Client, sku string) (string, error) {
	var flavors kueuev1beta1.ResourceFlavorList
	if err := c.List(ctx, &flavors); err != nil {
		return "", fmt.Errorf("listing ResourceFlavors: %w", err)
	}
	for _, f := range flavors.Items {
		product, ok := f.Spec.NodeLabels[labelGPUProduct]
		if ok && strings.EqualFold(product, sku) {
			return f.Name, nil
		}
	}
	return "", fmt.Errorf("no ResourceFlavor found for SKU %q", sku)
}

// triggerPreemption checks whether Guaranteed demand has consumed all available
// capacity and, if so, holds the Preemptible CQ (stopping new admissions) and
// selectively evicts workloads that have exceeded their minimum allocation time.
// Workloads within their minimum allocation floor are left running.
// When Guaranteed demand drops below capacity, StopPolicy is cleared.
func triggerPreemption(ctx context.Context, c client.Client, nodes []corev1.Node, reservations []billingv1alpha1.Reservation, geoLabel string, now time.Time) ([]EvictionCredit, error) {
	guaranteedDemand := make(map[capacityKey]int32)
	nodeCapacity := make(map[capacityKey]int32)

	if geoLabel == "" {
		geoLabel = defaultGeoLabel
	}

	for _, node := range nodes {
		sku := nodeSKU(node)
		if sku == "" {
			continue
		}
		key := capacityKey{SKU: sku, Geo: nodeGeo(node, geoLabel)}
		nodeCapacity[key] += nodeGPUCapacity(node)
	}

	// Build per-tenant min-allocation floor and credit rate from active
	// Preemptible reservations. When multiple exist for a tenant, use
	// the maximum of each (most protective/generous for the tenant).
	tenantMinAlloc := make(map[string]int32)
	tenantCreditRate := make(map[string]decimal.Decimal)
	for _, res := range reservations {
		switch res.Status.State {
		case billingv1alpha1.ReservationStateConfirmed, billingv1alpha1.ReservationStateActive:
		default:
			continue
		}
		if res.Spec.Class == billingv1alpha1.ReservationClassGuaranteed {
			key := capacityKey{SKU: res.Spec.SKU, Geo: res.Spec.Geo}
			guaranteedDemand[key] += res.Spec.Count
		}
		if res.Spec.Class == billingv1alpha1.ReservationClassPreemptible {
			minAlloc := billingv1alpha1.DefaultMinAllocationSeconds
			if res.Spec.MinAllocationSeconds != nil {
				minAlloc = *res.Spec.MinAllocationSeconds
			}
			if minAlloc > tenantMinAlloc[res.Spec.TenantRef] {
				tenantMinAlloc[res.Spec.TenantRef] = minAlloc
			}
			if res.Spec.EvictionCreditPerGpuSecond != nil {
				rate, err := decimal.NewFromString(*res.Spec.EvictionCreditPerGpuSecond)
				if err == nil {
					if existing, ok := tenantCreditRate[res.Spec.TenantRef]; !ok || rate.GreaterThan(existing) {
						tenantCreditRate[res.Spec.TenantRef] = rate
					}
				}
			}
		}
	}

	contested := make(map[capacityKey]bool)
	for key, demand := range guaranteedDemand {
		if demand >= nodeCapacity[key] {
			contested[key] = true
		}
	}

	var preemptibleCQs kueuev1beta1.ClusterQueueList
	if err := c.List(ctx, &preemptibleCQs, client.MatchingLabels{
		labelManagedBy:        managedByReservation,
		labelReservationClass: billingv1alpha1.ReservationClassPreemptible,
	}); err != nil {
		return nil, fmt.Errorf("listing preemptible CQs: %w", err)
	}

	var credits []EvictionCredit

	for i := range preemptibleCQs.Items {
		cq := &preemptibleCQs.Items[i]
		cqSKU := cq.Annotations[annotationSKU]
		cqGeo := cq.Annotations[annotationGeo]
		if cqGeo == "" {
			cqGeo = defaultGeoValue
		}
		key := capacityKey{SKU: cqSKU, Geo: cqGeo}

		if contested[key] {
			// Hold: stop new admissions but don't blanket-drain running workloads.
			hold := kueuev1beta1.Hold
			if cq.Spec.StopPolicy == nil || *cq.Spec.StopPolicy != hold {
				cq.Spec.StopPolicy = &hold
				if err := c.Update(ctx, cq); err != nil {
					return credits, fmt.Errorf("holding preemptible CQ %s: %w", cq.Name, err)
				}
			}

			// Selectively evict workloads past their minimum allocation floor.
			tenant := cq.Labels[labelTenant]
			minAlloc := tenantMinAlloc[tenant]
			evictions, err := evictPastFloor(ctx, c, cq.Name, minAlloc, now)
			if err != nil {
				return credits, fmt.Errorf("evicting past-floor workloads in CQ %s: %w", cq.Name, err)
			}

			// Compute eviction credits for evicted workloads.
			rate := tenantCreditRate[tenant]
			if !rate.IsZero() {
				for _, ev := range evictions {
					amount := rate.Mul(decimal.NewFromInt(ev.GPUCount)).Mul(decimal.NewFromFloat(ev.RuntimeSecs))
					credits = append(credits, EvictionCredit{
						TenantID:    tenant,
						Amount:      amount.StringFixed(6),
						WorkloadUID: ev.WorkloadUID,
						RuntimeSecs: ev.RuntimeSecs,
					})
				}
			}
		} else {
			none := kueuev1beta1.None
			if cq.Spec.StopPolicy == nil || *cq.Spec.StopPolicy == none {
				continue
			}
			cq.Spec.StopPolicy = &none
			if err := c.Update(ctx, cq); err != nil {
				return credits, fmt.Errorf("thawing preemptible CQ %s: %w", cq.Name, err)
			}
		}
	}

	return credits, nil
}

// evictPastFloor deactivates Workloads in the given ClusterQueue that have been
// admitted longer than the minimum allocation floor. It annotates each evicted
// workload so the collector can distinguish billing-driven preemption from other
// eviction causes, and returns info about each eviction for credit computation.
func evictPastFloor(ctx context.Context, c client.Client, cqName string, minAllocSeconds int32, now time.Time) ([]EvictionInfo, error) {
	var workloads kueuev1beta1.WorkloadList
	if err := c.List(ctx, &workloads); err != nil {
		return nil, fmt.Errorf("listing workloads: %w", err)
	}

	floor := time.Duration(minAllocSeconds) * time.Second
	var evictions []EvictionInfo

	for i := range workloads.Items {
		wl := &workloads.Items[i]

		if wl.Status.Admission == nil || string(wl.Status.Admission.ClusterQueue) != cqName {
			continue
		}
		if wl.Spec.Active != nil && !*wl.Spec.Active {
			continue
		}

		admittedCond := apimeta.FindStatusCondition(wl.Status.Conditions, kueuev1beta1.WorkloadAdmitted)
		if admittedCond == nil || admittedCond.Status != metav1.ConditionTrue {
			continue
		}

		if now.Sub(admittedCond.LastTransitionTime.Time) >= floor {
			// Annotate before deactivating so the collector can read the reason.
			if wl.Annotations == nil {
				wl.Annotations = make(map[string]string)
			}
			wl.Annotations[AnnotationEvictionReason] = "capacity-preemption"

			active := false
			wl.Spec.Active = &active
			if err := c.Update(ctx, wl); err != nil {
				return evictions, fmt.Errorf("evicting workload %s/%s: %w", wl.Namespace, wl.Name, err)
			}

			runtimeSecs := now.Sub(admittedCond.LastTransitionTime.Time).Seconds()
			gpuCount := workloadGPUCount(wl)

			evictions = append(evictions, EvictionInfo{
				WorkloadUID: string(wl.UID),
				TenantID:    cqName, // caller replaces with actual tenant
				GPUCount:    gpuCount,
				RuntimeSecs: runtimeSecs,
			})
		}
	}
	return evictions, nil
}

// workloadGPUCount extracts the total GPU count from a Kueue Workload's PodSets.
func workloadGPUCount(wl *kueuev1beta1.Workload) int64 {
	var total int64
	for _, ps := range wl.Spec.PodSets {
		for _, c := range ps.Template.Spec.Containers {
			if gpus, ok := c.Resources.Requests[gpuResourceKey]; ok {
				total += gpus.Value() * int64(ps.Count)
			}
		}
	}
	if total == 0 {
		total = 1
	}
	return total
}
