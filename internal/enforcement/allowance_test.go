package enforcement

import (
	"testing"
)

func int64Ptr(v int64) *int64 { return &v }

func TestComputeAllowance(t *testing.T) {
	tests := []struct {
		name    string
		balance string
		price   string
		planTPM *int64
		want    string
		wantErr bool
	}{
		{
			name:    "normal calculation",
			balance: "100.00",
			price:   "0.000050",
			planTPM: nil,
			want:    "2000000",
		},
		{
			name:    "clamped to plan TPM",
			balance: "100.00",
			price:   "0.000050",
			planTPM: int64Ptr(1000000),
			want:    "1000000",
		},
		{
			name:    "zero balance",
			balance: "0.000000",
			price:   "0.000050",
			planTPM: nil,
			want:    "0",
		},
		{
			name:    "negative balance",
			balance: "-50.000000",
			price:   "0.000050",
			planTPM: nil,
			want:    "0",
		},
		{
			name:    "zero price",
			balance: "100.00",
			price:   "0",
			planTPM: nil,
			wantErr: true,
		},
		{
			name:    "negative price",
			balance: "100.00",
			price:   "-0.000050",
			planTPM: nil,
			wantErr: true,
		},
		{
			name:    "invalid balance string",
			balance: "not-a-number",
			price:   "0.000050",
			planTPM: nil,
			wantErr: true,
		},
		{
			name:    "invalid price string",
			balance: "100.00",
			price:   "abc",
			planTPM: nil,
			wantErr: true,
		},
		{
			name:    "large balance clamped to plan TPM",
			balance: "999999999.00",
			price:   "0.000001",
			planTPM: int64Ptr(5000000),
			want:    "5000000",
		},
		{
			name:    "balance just enough for 1 token",
			balance: "0.000050",
			price:   "0.000050",
			planTPM: nil,
			want:    "1",
		},
		{
			name:    "balance not enough for 1 token",
			balance: "0.000049",
			price:   "0.000050",
			planTPM: nil,
			want:    "0",
		},
		{
			name:    "nil planTPM no cap",
			balance: "500.00",
			price:   "0.000050",
			planTPM: nil,
			want:    "10000000",
		},
		{
			name:    "planTPM of zero",
			balance: "100.00",
			price:   "0.000050",
			planTPM: int64Ptr(0),
			want:    "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ComputeAllowance(tt.balance, tt.price, tt.planTPM)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ComputeAllowance(%q, %q, %v) = %q, want %q",
					tt.balance, tt.price, tt.planTPM, got, tt.want)
			}
		})
	}
}

func TestEstimatePricePerToken(t *testing.T) {
	tests := []struct {
		name       string
		prompt     string
		completion string
		want       string
		wantErr    bool
	}{
		{
			name:       "different rates",
			prompt:     "0.000003",
			completion: "0.000015",
			want:       "0.000009",
		},
		{
			name:       "same rates",
			prompt:     "0.000010",
			completion: "0.000010",
			want:       "0.00001",
		},
		{
			name:       "invalid prompt",
			prompt:     "bad",
			completion: "0.000015",
			wantErr:    true,
		},
		{
			name:       "invalid completion",
			prompt:     "0.000003",
			completion: "bad",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EstimatePricePerToken(tt.prompt, tt.completion)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("EstimatePricePerToken(%q, %q) = %q, want %q",
					tt.prompt, tt.completion, got, tt.want)
			}
		})
	}
}
