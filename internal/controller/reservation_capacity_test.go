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
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

// fakeGPUNode builds a corev1.Node with GPU labels and optional allocatable
// GPU resources. Extra labels are merged on top of the defaults.
func fakeGPUNode(name, sku, geo string, allocatable int64, labels map[string]string) corev1.Node {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{labelGPUProduct: sku},
		},
	}
	if geo != "" {
		node.Labels[defaultGeoLabel] = geo
	}
	for k, v := range labels {
		node.Labels[k] = v
	}
	if allocatable > 0 {
		node.Status.Allocatable = corev1.ResourceList{
			corev1.ResourceName(gpuResource): resource.MustParse(fmt.Sprintf("%d", allocatable)),
		}
	}
	return node
}

// fakeReservation builds a billingv1alpha1.Reservation with the given state.
func fakeReservation(name, tenant, sku, geo, class, state string, count int32) billingv1alpha1.Reservation {
	return billingv1alpha1.Reservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: billingv1alpha1.ReservationSpec{
			TenantRef:    tenant,
			SKU:          sku,
			Count:        count,
			Geo:          geo,
			Class:        class,
			MonthlyPrice: "1000.000000",
		},
		Status: billingv1alpha1.ReservationStatus{
			State: state,
		},
	}
}

func TestBuildCapacitySnapshot_AllocatableGPUs(t *testing.T) {
	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "us-east", 4, nil),
		fakeGPUNode("node-2", "h200", "us-east", 4, nil),
	}

	snap := BuildCapacitySnapshot(nodes, nil, "")
	key := capacityKey{SKU: "h200", Geo: "us-east"}
	if got := snap.NodeCapacity[key]; got != 8 {
		t.Errorf("NodeCapacity[h200/us-east] = %d, want 8", got)
	}
}

func TestBuildCapacitySnapshot_LabelFallback(t *testing.T) {
	nodes := []corev1.Node{
		fakeGPUNode("node-label", "a100", "us-west", 0, map[string]string{
			labelGPUCount: "8",
		}),
	}

	snap := BuildCapacitySnapshot(nodes, nil, "")
	key := capacityKey{SKU: "a100", Geo: "us-west"}
	if got := snap.NodeCapacity[key]; got != 8 {
		t.Errorf("NodeCapacity[a100/us-west] = %d, want 8 (from label fallback)", got)
	}
}

func TestBuildCapacitySnapshot_MultipleSKUsAndGeos(t *testing.T) {
	nodes := []corev1.Node{
		fakeGPUNode("node-h200-east", "h200", "us-east", 4, nil),
		fakeGPUNode("node-a100-west", "a100", "us-west", 8, nil),
		fakeGPUNode("node-h200-west", "h200", "us-west", 2, nil),
	}

	snap := BuildCapacitySnapshot(nodes, nil, "")

	tests := []struct {
		sku, geo string
		want     int32
	}{
		{"h200", "us-east", 4},
		{"a100", "us-west", 8},
		{"h200", "us-west", 2},
	}
	for _, tt := range tests {
		key := capacityKey{SKU: tt.sku, Geo: tt.geo}
		if got := snap.NodeCapacity[key]; got != tt.want {
			t.Errorf("NodeCapacity[%s/%s] = %d, want %d", tt.sku, tt.geo, got, tt.want)
		}
	}
}

func TestBuildCapacitySnapshot_ReservationsReduceAvailable(t *testing.T) {
	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "us-east", 4, nil),
		fakeGPUNode("node-2", "h200", "us-east", 4, nil),
	}
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-1", "tenant-a", "h200", "us-east", "Guaranteed", billingv1alpha1.ReservationStateConfirmed, 2),
		fakeReservation("res-2", "tenant-b", "h200", "us-east", "Guaranteed", billingv1alpha1.ReservationStateConfirmed, 3),
	}

	snap := BuildCapacitySnapshot(nodes, reservations, "")
	if got := snap.Available("h200", "us-east"); got != 3 {
		t.Errorf("Available(h200, us-east) = %d, want 3 (8 total - 5 reserved)", got)
	}
}

func TestBuildCapacitySnapshot_OnlyActiveConfirmedCounted(t *testing.T) {
	nodes := []corev1.Node{
		fakeGPUNode("node-1", "h200", "us-east", 8, nil),
	}
	reservations := []billingv1alpha1.Reservation{
		fakeReservation("res-confirmed", "t1", "h200", "us-east", "Guaranteed", billingv1alpha1.ReservationStateConfirmed, 2),
		fakeReservation("res-active", "t2", "h200", "us-east", "Guaranteed", billingv1alpha1.ReservationStateActive, 1),
		fakeReservation("res-requested", "t3", "h200", "us-east", "Guaranteed", billingv1alpha1.ReservationStateRequested, 10),
		fakeReservation("res-expired", "t4", "h200", "us-east", "Guaranteed", billingv1alpha1.ReservationStateExpired, 10),
		fakeReservation("res-cancelled", "t5", "h200", "us-east", "Guaranteed", billingv1alpha1.ReservationStateCancelled, 10),
	}

	snap := BuildCapacitySnapshot(nodes, reservations, "")

	// Only Confirmed (2) + Active (1) = 3 should be reserved.
	// Requested (10), Expired (10), and Cancelled (10) must not count.
	if got := snap.Available("h200", "us-east"); got != 5 {
		t.Errorf("Available(h200, us-east) = %d, want 5 (8 - 3 counted)", got)
	}
}

func TestCapacitySnapshot_Available(t *testing.T) {
	snap := &CapacitySnapshot{
		NodeCapacity: map[capacityKey]int32{
			{SKU: "h200", Geo: "us-east"}: 4,
		},
		Reserved: map[capacityKey]int32{
			{SKU: "h200", Geo: "us-east"}: 6,
		},
	}

	if got := snap.Available("h200", "us-east"); got != -2 {
		t.Errorf("Available(h200, us-east) = %d, want -2 (over-reserved)", got)
	}

	// Key with no entries should return 0.
	if got := snap.Available("a100", "eu-west"); got != 0 {
		t.Errorf("Available(a100, eu-west) = %d, want 0 (no entries)", got)
	}
}

func TestNodeGPUCapacity_AllocatablePreferred(t *testing.T) {
	node := fakeGPUNode("node-both", "h200", "us-east", 4, map[string]string{
		labelGPUCount: "8",
	})

	if got := nodeGPUCapacity(node); got != 4 {
		t.Errorf("nodeGPUCapacity = %d, want 4 (allocatable should win over label)", got)
	}
}

func TestNodeGPUCapacity_ZeroWhenMissing(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "bare-node",
			Labels: map[string]string{labelGPUProduct: "h200"},
		},
	}

	if got := nodeGPUCapacity(node); got != 0 {
		t.Errorf("nodeGPUCapacity = %d, want 0 (no allocatable, no label)", got)
	}
}

func TestNodeSKU_Lowercased(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "upper-sku",
			Labels: map[string]string{labelGPUProduct: "H200"},
		},
	}

	if got := nodeSKU(node); got != "h200" {
		t.Errorf("nodeSKU = %q, want %q", got, "h200")
	}
}

func TestNodeGeo_DefaultWhenMissing(t *testing.T) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "no-geo",
			Labels: map[string]string{labelGPUProduct: "h200"},
		},
	}

	if got := nodeGeo(node, defaultGeoLabel); got != defaultGeoValue {
		t.Errorf("nodeGeo = %q, want %q", got, defaultGeoValue)
	}
}
