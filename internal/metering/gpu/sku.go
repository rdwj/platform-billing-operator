package gpu

import (
	"fmt"
	"strings"
)

const (
	// LabelGPUProduct is the NVIDIA device plugin label for the GPU product name.
	LabelGPUProduct = "nvidia.com/gpu.product"
	// LabelGPUCount is the NVIDIA device plugin label for the GPU count on a node.
	LabelGPUCount = "nvidia.com/gpu.count"
	// LabelMIGConfig is the NVIDIA device plugin label for the MIG configuration.
	LabelMIGConfig = "nvidia.com/mig.config"
)

// DeriveWorkloadSKU resolves the billing SKU and MIG slice fraction from node labels.
// The skuMapping parameter may be nil; in that case only the raw label-derived SKU is returned.
func DeriveWorkloadSKU(nodeLabels map[string]string, skuMapping map[string]SKUInfo) (string, float64, error) {
	product, ok := nodeLabels[LabelGPUProduct]
	if !ok {
		return "", 0, fmt.Errorf("node missing label %s", LabelGPUProduct)
	}

	sku := strings.ToLower(product)
	sliceFraction := 1.0

	if migConfig, hasMIG := nodeLabels[LabelMIGConfig]; hasMIG {
		sku = fmt.Sprintf("%s-mig-%s", sku, strings.ToLower(migConfig))
	}

	if skuMapping != nil {
		if info, mapped := skuMapping[sku]; mapped {
			if info.SKU != "" {
				sku = info.SKU
			}
			if info.SliceFraction > 0 {
				sliceFraction = info.SliceFraction
			}
		}
	}

	return sku, sliceFraction, nil
}

// ParseGPUCount reads the GPU count from node labels, defaulting to 1.
func ParseGPUCount(nodeLabels map[string]string) (int64, error) {
	countStr, ok := nodeLabels[LabelGPUCount]
	if !ok {
		return 1, nil
	}
	var count int64
	if _, err := fmt.Sscanf(countStr, "%d", &count); err != nil {
		return 0, fmt.Errorf("parse %s=%q: %w", LabelGPUCount, countStr, err)
	}
	if count <= 0 {
		return 0, fmt.Errorf("invalid GPU count: %d", count)
	}
	return count, nil
}
