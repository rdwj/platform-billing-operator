package gpu_test

import (
	"testing"

	"github.com/rdwj/platform-billing-operator/internal/metering/gpu"
	"github.com/stretchr/testify/require"
)

func TestDeriveWorkloadSKU(t *testing.T) {
	tests := []struct {
		name         string
		labels       map[string]string
		mapping      map[string]gpu.SKUInfo
		wantSKU      string
		wantFraction float64
		wantErr      bool
		errContains  string
	}{
		{
			name: "full GPU no MIG",
			labels: map[string]string{
				gpu.LabelGPUProduct: "H200",
			},
			mapping:      nil,
			wantSKU:      "h200",
			wantFraction: 1.0,
		},
		{
			name: "MIG slice without mapping",
			labels: map[string]string{
				gpu.LabelGPUProduct: "H200",
				gpu.LabelMIGConfig:  "3g.40gb",
			},
			mapping:      nil,
			wantSKU:      "h200-mig-3g.40gb",
			wantFraction: 1.0,
		},
		{
			name: "MIG with slice fraction mapping",
			labels: map[string]string{
				gpu.LabelGPUProduct: "H200",
				gpu.LabelMIGConfig:  "3g.40gb",
			},
			mapping: map[string]gpu.SKUInfo{
				"h200-mig-3g.40gb": {SliceFraction: 0.375},
			},
			wantSKU:      "h200-mig-3g.40gb",
			wantFraction: 0.375,
		},
		{
			name: "SKU override in mapping",
			labels: map[string]string{
				gpu.LabelGPUProduct: "H200",
			},
			mapping: map[string]gpu.SKUInfo{
				"h200": {SKU: "h200-full", SliceFraction: 1.0},
			},
			wantSKU:      "h200-full",
			wantFraction: 1.0,
		},
		{
			name:        "missing product label",
			labels:      map[string]string{},
			mapping:     nil,
			wantErr:     true,
			errContains: gpu.LabelGPUProduct,
		},
		{
			name: "nil mapping does not panic",
			labels: map[string]string{
				gpu.LabelGPUProduct: "A100",
			},
			mapping:      nil,
			wantSKU:      "a100",
			wantFraction: 1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sku, fraction, err := gpu.DeriveWorkloadSKU(tt.labels, tt.mapping)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantSKU, sku)
			require.InDelta(t, tt.wantFraction, fraction, 0.001)
		})
	}
}

func TestParseGPUCount(t *testing.T) {
	tests := []struct {
		name        string
		labels      map[string]string
		want        int64
		wantErr     bool
		errContains string
	}{
		{
			name: "present label",
			labels: map[string]string{
				gpu.LabelGPUCount: "2",
			},
			want: 2,
		},
		{
			name:   "missing label defaults to 1",
			labels: map[string]string{},
			want:   1,
		},
		{
			name: "invalid non-numeric",
			labels: map[string]string{
				gpu.LabelGPUCount: "abc",
			},
			wantErr:     true,
			errContains: "parse",
		},
		{
			name: "zero is invalid",
			labels: map[string]string{
				gpu.LabelGPUCount: "0",
			},
			wantErr:     true,
			errContains: "invalid GPU count",
		},
		{
			name: "negative is invalid",
			labels: map[string]string{
				gpu.LabelGPUCount: "-1",
			},
			wantErr:     true,
			errContains: "invalid GPU count",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := gpu.ParseGPUCount(tt.labels)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
