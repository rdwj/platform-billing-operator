// Integration tests for the OpenMeter UsageSinkBackend.
//
// Prerequisites:
//   podman-compose -f test/openmeter/podman-compose.yml up -d
//
// Run:
//   go test -tags integration -v ./test/openmeter/
//
// The tests are skipped when -short is passed or the integration build tag
// is absent. They expect the OpenMeter API on localhost:48888 (matching
// podman-compose.yml).

//go:build integration

package openmeter_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rdwj/platform-billing-operator/internal/usagesink"
)

const (
	openmeterEndpoint = "http://localhost:48888"
	testTenantKey     = "test-tenant-001"
	testModel         = "granite-3.3-8b-instruct"
)

// testMeters returns the three meter definitions matching config.yaml.
func testMeters() []usagesink.MeterDefinition {
	return []usagesink.MeterDefinition{
		{
			Slug:          "inference_prompt_tokens",
			Description:   "Total prompt tokens consumed per inference request",
			EventType:     "inference",
			Aggregation:   "SUM",
			ValueProperty: "$.prompt_tokens",
			GroupBy:       map[string]string{"model": "$.model"},
		},
		{
			Slug:          "inference_completion_tokens",
			Description:   "Total completion tokens produced per inference request",
			EventType:     "inference",
			Aggregation:   "SUM",
			ValueProperty: "$.completion_tokens",
			GroupBy:       map[string]string{"model": "$.model"},
		},
		{
			Slug:        "inference_request_count",
			Description: "Count of inference requests",
			EventType:   "inference",
			Aggregation: "COUNT",
			GroupBy:     map[string]string{"model": "$.model"},
		},
	}
}

// testEvents returns five deterministic inference events whose prompt-token
// values sum to 2500 (100+200+300+400+1500) and completion-token values sum
// to 1250 (50+100+150+200+750).
func testEvents() []usagesink.UsageEvent {
	base := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	data := []struct {
		prompt     int64
		completion int64
	}{
		{100, 50},
		{200, 100},
		{300, 150},
		{400, 200},
		{1500, 750},
	}

	events := make([]usagesink.UsageEvent, len(data))
	for i, d := range data {
		events[i] = usagesink.UsageEvent{
			RecordID:         fmt.Sprintf("test-record-%d", i+1),
			TenantID:         testTenantKey,
			Timestamp:        base.Add(time.Duration(i) * time.Second),
			EventType:        "inference",
			Model:            testModel,
			PromptTokens:     decimal.NewFromInt(d.prompt),
			CompletionTokens: decimal.NewFromInt(d.completion),
			ReasoningTokens:  decimal.Zero,
			CachedTokens:     decimal.Zero,
			RequestID:        fmt.Sprintf("req-%d", i+1),
		}
	}
	return events
}

func newBackend() *usagesink.OpenMeterBackend {
	// No API key for self-hosted OpenMeter.
	return usagesink.NewOpenMeterBackend(openmeterEndpoint, "")
}

func TestOpenMeterIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	backend := newBackend()

	// Step 1: Verify connectivity.
	t.Run("HealthCheck", func(t *testing.T) {
		err := backend.HealthCheck(ctx)
		require.NoError(t, err, "OpenMeter should be reachable at %s", openmeterEndpoint)
	})

	// Step 2: Ensure meters exist (idempotent -- config.yaml already defines
	// them, so this validates the API call round-trips without error).
	t.Run("ConfigureMeters", func(t *testing.T) {
		err := backend.ConfigureMeters(ctx, testMeters())
		require.NoError(t, err, "ConfigureMeters should succeed (meters pre-defined in config.yaml)")
	})

	// Step 3: Register the test tenant as a subject.
	t.Run("SyncSubject", func(t *testing.T) {
		err := backend.SyncSubject(ctx, usagesink.TenantInfo{
			Key:         testTenantKey,
			DisplayName: "Integration Test Tenant",
			Metadata:    map[string]string{"env": "test"},
		})
		require.NoError(t, err, "SyncSubject should register the test tenant")
	})

	// Step 4: Ingest five deterministic events.
	events := testEvents()
	t.Run("IngestEvents", func(t *testing.T) {
		n, err := backend.IngestEvents(ctx, events)
		require.NoError(t, err, "IngestEvents should succeed")
		assert.Equal(t, len(events), n, "all events should be accepted")
	})

	// Step 5: Wait for events to flow through Kafka -> ClickHouse.
	// OpenMeter's sink-worker has minCommitCount=1, so this is fast, but
	// ClickHouse merge latency means we need a short pause.
	t.Log("waiting 3s for OpenMeter to process events through Kafka -> ClickHouse")
	time.Sleep(3 * time.Second)

	// Step 6: Query prompt-token usage and verify the aggregate.
	t.Run("QueryUsage_PromptTokens", func(t *testing.T) {
		records, err := backend.QueryUsage(ctx, usagesink.UsageQuery{
			MeterSlug: "inference_prompt_tokens",
			Subjects:  []string{testTenantKey},
		})
		require.NoError(t, err, "QueryUsage should succeed")
		require.NotEmpty(t, records, "should return at least one usage record")

		// Sum all returned records (there may be multiple windows).
		total := decimal.Zero
		for _, r := range records {
			total = total.Add(r.Value)
		}

		expectedPromptTokens := decimal.NewFromInt(2500) // 100+200+300+400+1500
		assert.True(t, expectedPromptTokens.Equal(total),
			"expected prompt_tokens total %s, got %s", expectedPromptTokens, total)
	})

	// Step 7: Query completion-token usage for additional confidence.
	t.Run("QueryUsage_CompletionTokens", func(t *testing.T) {
		records, err := backend.QueryUsage(ctx, usagesink.UsageQuery{
			MeterSlug: "inference_completion_tokens",
			Subjects:  []string{testTenantKey},
		})
		require.NoError(t, err, "QueryUsage should succeed")
		require.NotEmpty(t, records, "should return at least one usage record")

		total := decimal.Zero
		for _, r := range records {
			total = total.Add(r.Value)
		}

		expectedCompletionTokens := decimal.NewFromInt(1250) // 50+100+150+200+750
		assert.True(t, expectedCompletionTokens.Equal(total),
			"expected completion_tokens total %s, got %s", expectedCompletionTokens, total)
	})

	// Step 8: Query request count.
	t.Run("QueryUsage_RequestCount", func(t *testing.T) {
		records, err := backend.QueryUsage(ctx, usagesink.UsageQuery{
			MeterSlug: "inference_request_count",
			Subjects:  []string{testTenantKey},
		})
		require.NoError(t, err, "QueryUsage should succeed")
		require.NotEmpty(t, records, "should return at least one usage record")

		total := decimal.Zero
		for _, r := range records {
			total = total.Add(r.Value)
		}

		expectedCount := decimal.NewFromInt(5)
		assert.True(t, expectedCount.Equal(total),
			"expected request count %s, got %s", expectedCount, total)
	})
}
