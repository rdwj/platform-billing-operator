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

package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validReservation(class string) *Reservation {
	future := metav1.NewTime(time.Now().Add(1 * time.Hour))
	end := metav1.NewTime(time.Now().Add(2 * time.Hour))
	return &Reservation{
		ObjectMeta: metav1.ObjectMeta{Name: "test-res", Namespace: "default"},
		Spec: ReservationSpec{
			TenantRef:    "test-tenant",
			SKU:          "h200",
			Count:        1,
			Geo:          "us-east-1",
			Class:        class,
			MonthlyPrice: "100.00",
			Window:       ReservationWindow{Start: future, End: end},
		},
	}
}

func int32Ptr(v int32) *int32 { return &v }

func TestValidateReservationSpec_MinAllocationSeconds(t *testing.T) {
	tests := []struct {
		name      string
		class     string
		minAlloc  *int32
		wantError bool
		errField  string
	}{
		{
			name:      "Preemptible with valid minAllocationSeconds",
			class:     ReservationClassPreemptible,
			minAlloc:  int32Ptr(3600),
			wantError: false,
		},
		{
			name:      "Preemptible without minAllocationSeconds",
			class:     ReservationClassPreemptible,
			minAlloc:  nil,
			wantError: false,
		},
		{
			name:      "Preemptible with zero minAllocationSeconds",
			class:     ReservationClassPreemptible,
			minAlloc:  int32Ptr(0),
			wantError: false,
		},
		{
			name:      "Preemptible with negative minAllocationSeconds",
			class:     ReservationClassPreemptible,
			minAlloc:  int32Ptr(-1),
			wantError: true,
			errField:  "spec.minAllocationSeconds",
		},
		{
			name:      "Guaranteed with minAllocationSeconds rejected",
			class:     ReservationClassGuaranteed,
			minAlloc:  int32Ptr(3600),
			wantError: true,
			errField:  "spec.minAllocationSeconds",
		},
		{
			name:      "Guaranteed without minAllocationSeconds",
			class:     ReservationClassGuaranteed,
			minAlloc:  nil,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validReservation(tt.class)
			r.Spec.MinAllocationSeconds = tt.minAlloc

			errs := validateReservationSpec(r)

			if tt.wantError && len(errs) == 0 {
				t.Errorf("expected validation error for %s, got none", tt.errField)
			}
			if !tt.wantError && len(errs) > 0 {
				t.Errorf("expected no errors, got: %v", errs)
			}
			if tt.wantError && len(errs) > 0 {
				found := false
				for _, e := range errs {
					if e.Field == tt.errField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, got errors: %v", tt.errField, errs)
				}
			}
		})
	}
}

func stringPtr(s string) *string { return &s }

func TestValidateReservationSpec_EvictionCreditPerGpuSecond(t *testing.T) {
	tests := []struct {
		name      string
		class     string
		credit    *string
		wantError bool
		errField  string
	}{
		{
			name:      "Preemptible with valid credit rate",
			class:     ReservationClassPreemptible,
			credit:    stringPtr("0.001000"),
			wantError: false,
		},
		{
			name:      "Preemptible without credit (nil)",
			class:     ReservationClassPreemptible,
			credit:    nil,
			wantError: false,
		},
		{
			name:      "Preemptible with zero credit",
			class:     ReservationClassPreemptible,
			credit:    stringPtr("0"),
			wantError: false,
		},
		{
			name:      "Guaranteed with credit rejected",
			class:     ReservationClassGuaranteed,
			credit:    stringPtr("0.001000"),
			wantError: true,
			errField:  "spec.evictionCreditPerGpuSecond",
		},
		{
			name:      "Invalid decimal format",
			class:     ReservationClassPreemptible,
			credit:    stringPtr("not-a-number"),
			wantError: true,
			errField:  "spec.evictionCreditPerGpuSecond",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := validReservation(tt.class)
			r.Spec.EvictionCreditPerGpuSecond = tt.credit

			errs := validateReservationSpec(r)

			if tt.wantError && len(errs) == 0 {
				t.Errorf("expected validation error for %s, got none", tt.errField)
			}
			if !tt.wantError && len(errs) > 0 {
				t.Errorf("expected no errors, got: %v", errs)
			}
			if tt.wantError && len(errs) > 0 {
				found := false
				for _, e := range errs {
					if e.Field == tt.errField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error on field %q, got errors: %v", tt.errField, errs)
				}
			}
		})
	}
}
