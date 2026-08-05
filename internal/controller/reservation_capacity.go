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
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	billingv1alpha1 "github.com/rdwj/platform-billing-operator/api/v1alpha1"
)

const (
	labelGPUProduct = "nvidia.com/gpu.product"
	labelGPUCount   = "nvidia.com/gpu.count"
	defaultGeoLabel = "topology.kubernetes.io/region"
	defaultGeoValue = "default"
	gpuResource     = "nvidia.com/gpu"

	requeueCapacityCheck = 30 * time.Second
)

type capacityKey struct {
	SKU string
	Geo string
}

// CapacitySnapshot holds a point-in-time view of GPU capacity and reservations
// across the cluster. It is built from pre-fetched node and reservation lists
// so that the caller controls when and how k8s objects are listed.
type CapacitySnapshot struct {
	NodeCapacity map[capacityKey]int32
	Reserved     map[capacityKey]int32
}

// BuildCapacitySnapshot constructs a CapacitySnapshot from the provided nodes
// and reservations. Only nodes with the nvidia.com/gpu.product label are
// considered. Only reservations in the Confirmed or Active state count toward
// reserved capacity. If geoLabel is empty, topology.kubernetes.io/region is
// used.
func BuildCapacitySnapshot(nodes []corev1.Node, reservations []billingv1alpha1.Reservation, geoLabel string) *CapacitySnapshot {
	if geoLabel == "" {
		geoLabel = defaultGeoLabel
	}

	snap := &CapacitySnapshot{
		NodeCapacity: make(map[capacityKey]int32),
		Reserved:     make(map[capacityKey]int32),
	}

	for _, node := range nodes {
		sku := nodeSKU(node)
		if sku == "" {
			// Not a GPU node (no nvidia.com/gpu.product label).
			continue
		}
		cap := nodeGPUCapacity(node)
		key := capacityKey{SKU: sku, Geo: nodeGeo(node, geoLabel)}
		snap.NodeCapacity[key] += cap
	}

	for _, res := range reservations {
		switch res.Status.State {
		case billingv1alpha1.ReservationStateConfirmed, billingv1alpha1.ReservationStateActive:
			key := capacityKey{SKU: res.Spec.SKU, Geo: res.Spec.Geo}
			snap.Reserved[key] += res.Spec.Count
		}
	}

	return snap
}

// Available returns the unreserved GPU count for a given SKU and geo. The
// result can be negative when reservations exceed node capacity (over-reserved).
func (s *CapacitySnapshot) Available(sku, geo string) int32 {
	key := capacityKey{SKU: sku, Geo: geo}
	return s.NodeCapacity[key] - s.Reserved[key]
}

// nodeGPUCapacity returns the GPU count for a node. It prefers the allocatable
// resource nvidia.com/gpu; if that is absent or zero it falls back to the
// nvidia.com/gpu.count label.
func nodeGPUCapacity(node corev1.Node) int32 {
	if qty, ok := node.Status.Allocatable[corev1.ResourceName(gpuResource)]; ok {
		val := qty.Value()
		if val > 0 {
			return int32(val)
		}
	}

	if countStr, ok := node.Labels[labelGPUCount]; ok {
		if n, err := strconv.ParseInt(countStr, 10, 32); err == nil {
			return int32(n)
		}
	}

	return 0
}

// nodeSKU returns the lowercase GPU product name from the node's labels.
func nodeSKU(node corev1.Node) string {
	return strings.ToLower(node.Labels[labelGPUProduct])
}

// nodeGeo returns the geographic region label value, falling back to
// defaultGeoValue when the label is absent.
func nodeGeo(node corev1.Node, geoLabel string) string {
	if v, ok := node.Labels[geoLabel]; ok {
		return v
	}
	return defaultGeoValue
}
