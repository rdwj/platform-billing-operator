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
	"time"

	"github.com/shopspring/decimal"
)

// UsageSinkBackend abstracts an external metering/usage platform (OpenMeter,
// Amberflo, Orb, etc.). Implementations handle usage event ingestion, meter
// configuration, and usage queries. The operator programs against this
// interface so the concrete platform can be swapped without touching
// reconciliation logic.
type UsageSinkBackend interface {
	// IngestEvents pushes a batch of usage events to the external platform.
	// Returns the number of events successfully ingested.
	IngestEvents(ctx context.Context, events []UsageEvent) (int, error)

	// HealthCheck verifies connectivity and credentials.
	HealthCheck(ctx context.Context) error

	// ConfigureMeters ensures the given meter definitions exist in the
	// external platform. Idempotent.
	ConfigureMeters(ctx context.Context, meters []MeterDefinition) error

	// SyncSubject ensures a tenant exists in the external platform.
	SyncSubject(ctx context.Context, tenant TenantInfo) error

	// QueryUsage reads back aggregated usage from the external platform,
	// primarily for ledger reconciliation.
	QueryUsage(ctx context.Context, query UsageQuery) ([]UsageRecord, error)
}

// UsageEvent is the operator's internal usage record, mapped to
// platform-specific formats by each backend implementation.
type UsageEvent struct {
	RecordID         string
	TenantID         string
	Timestamp        time.Time
	EventType        string // "inference", "gpu-job", etc.
	Model            string
	PromptTokens     decimal.Decimal
	CompletionTokens decimal.Decimal
	ReasoningTokens  decimal.Decimal
	CachedTokens     decimal.Decimal
	RequestID        string
	Metadata         map[string]string
}

// MeterDefinition describes an aggregation rule in the external platform.
type MeterDefinition struct {
	Slug          string
	Description   string
	EventType     string
	Aggregation   string            // SUM, COUNT, AVG, MIN, MAX, UNIQUE_COUNT
	ValueProperty string            // JSONPath into event data (e.g. "$.prompt_tokens")
	GroupBy       map[string]string // label -> JSONPath
}

// TenantInfo identifies a tenant in the external platform.
type TenantInfo struct {
	Key         string // TenantAccount name
	DisplayName string
	Metadata    map[string]string
}

// UsageQuery requests aggregated usage from the external platform.
type UsageQuery struct {
	MeterSlug string
	Subjects  []string
	From      time.Time
	To        time.Time
	GroupBy   []string
}

// UsageRecord is an aggregated usage data point from the external platform.
type UsageRecord struct {
	Subject     string
	Value       decimal.Decimal
	WindowStart time.Time
	WindowEnd   time.Time
	GroupBy     map[string]string
}
