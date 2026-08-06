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

package usagesink

import (
	"context"
)

// OpenMeterBackend implements UsageSinkBackend for the OpenMeter platform.
// Method bodies are stubs returning nil/zero; real implementation is Phase 5.
type OpenMeterBackend struct {
	Endpoint string
	APIKey   string
}

var _ UsageSinkBackend = (*OpenMeterBackend)(nil)

// NewOpenMeterBackend constructs an OpenMeterBackend. An empty apiKey is
// valid for self-hosted OpenMeter instances without authentication.
func NewOpenMeterBackend(endpoint, apiKey string) *OpenMeterBackend {
	return &OpenMeterBackend{
		Endpoint: endpoint,
		APIKey:   apiKey,
	}
}

func (b *OpenMeterBackend) IngestEvents(_ context.Context, events []UsageEvent) (int, error) {
	return len(events), nil
}

func (b *OpenMeterBackend) HealthCheck(_ context.Context) error {
	return nil
}

func (b *OpenMeterBackend) ConfigureMeters(_ context.Context, _ []MeterDefinition) error {
	return nil
}

func (b *OpenMeterBackend) SyncSubject(_ context.Context, _ TenantInfo) error {
	return nil
}

func (b *OpenMeterBackend) QueryUsage(_ context.Context, _ UsageQuery) ([]UsageRecord, error) {
	return nil, nil
}
