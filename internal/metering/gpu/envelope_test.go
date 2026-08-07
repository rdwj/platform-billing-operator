package gpu_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/metering/gpu"
	"github.com/stretchr/testify/require"
)

// referenceEvent returns an IntervalEvent matching the reference example
// at contracts/examples/gpuaas-interval.json.
func referenceEvent() gpu.IntervalEvent {
	return gpu.IntervalEvent{
		RecordID:      "wl-9be2c1d4-interval-0007",
		TenantID:      "ten-helix",
		SKU:           "h200-mig-3g.40gb",
		Geo:           "us-east-1",
		GPUSeconds:    3600,
		SliceFraction: 0.375,
		IntervalStart: time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
		IntervalEnd:   time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC),
		IntervalSeq:   7,
		Queue:         "team-ml-batch",
		WorkloadName:  "train-run-42",
		Status:        "ok",
		OccurredAt:    time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC),
	}
}

func TestBuildEnvelope_MatchesReferenceStructure(t *testing.T) {
	ev := referenceEvent()
	raw, err := gpu.BuildEnvelope(ev, "")
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	require.Equal(t, "usage.v1", m["schema_version"])
	require.Equal(t, "wl-9be2c1d4-interval-0007", m["record_id"])
	require.Equal(t, "2026-07-29T15:00:00Z", m["occurred_at"])
	require.Equal(t, "ten-helix", m["tenant_id"])
	require.Equal(t, "h200-mig-3g.40gb", m["sku"])
	require.Equal(t, "us-east-1", m["geo"])
	require.Equal(t, "ok", m["status"])

	source, ok := m["source"].(map[string]any)
	require.True(t, ok, "source should be an object")
	require.Equal(t, "gpuaas", source["profile"])
	require.Equal(t, "kueue-collector", source["collector"])

	quantities, ok := m["quantities"].(map[string]any)
	require.True(t, ok, "quantities should be an object")
	require.InDelta(t, 3600.0, quantities["gpu_seconds"], 0.001)
	require.InDelta(t, 0.375, quantities["slice_fraction"], 0.001)

	interval, ok := m["interval"].(map[string]any)
	require.True(t, ok, "interval should be an object")
	require.Equal(t, "2026-07-29T14:00:00Z", interval["start"])
	require.Equal(t, "2026-07-29T15:00:00Z", interval["end"])
	require.InDelta(t, 7.0, interval["seq"], 0.001)

	attrs, ok := m["attributes"].(map[string]any)
	require.True(t, ok, "attributes should be an object")
	require.Equal(t, "team-ml-batch", attrs["queue"])
	require.Equal(t, "train-run-42", attrs["workload"])
}

func TestBuildEnvelope_RequiredFields(t *testing.T) {
	ev := referenceEvent()
	raw, err := gpu.BuildEnvelope(ev, "")
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	// interval is required for gpuaas profile per schema allOf conditional
	require.Contains(t, m, "interval", "interval must be present for gpuaas profile")
	interval, ok := m["interval"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, interval, "start")
	require.Contains(t, interval, "end")
	require.Contains(t, interval, "seq")
}

func TestBuildEnvelope_OmitsEmptyOptional(t *testing.T) {
	ev := referenceEvent()
	ev.Geo = ""
	ev.Queue = ""
	ev.WorkloadName = ""
	ev.Reason = ""

	raw, err := gpu.BuildEnvelope(ev, "")
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	_, hasGeo := m["geo"]
	require.False(t, hasGeo, "geo should be omitted when empty")

	_, hasAttrs := m["attributes"]
	require.False(t, hasAttrs, "attributes should be omitted when no queue, workload, or reason")

	// collector_version should also be omitted when empty
	source := m["source"].(map[string]any)
	_, hasVersion := source["collector_version"]
	require.False(t, hasVersion, "collector_version should be omitted when empty")
}

func TestBuildEnvelope_IncludesReason(t *testing.T) {
	ev := referenceEvent()
	ev.Reason = "capacity-preemption"

	raw, err := gpu.BuildEnvelope(ev, "")
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	attrs, ok := m["attributes"].(map[string]any)
	require.True(t, ok, "attributes should be present")
	require.Equal(t, "capacity-preemption", attrs["reason"])
}

func TestBuildEnvelope_ReasonOnlyInAttributes(t *testing.T) {
	ev := gpu.IntervalEvent{
		RecordID:      "wl-test-interval-0000",
		TenantID:      "ten-test",
		SKU:           "h200",
		GPUSeconds:    100,
		SliceFraction: 1.0,
		IntervalStart: time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
		IntervalEnd:   time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC),
		IntervalSeq:   0,
		Status:        "ok",
		Reason:        "finished",
		OccurredAt:    time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC),
	}

	raw, err := gpu.BuildEnvelope(ev, "")
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	attrs, ok := m["attributes"].(map[string]any)
	require.True(t, ok, "attributes should be present when reason is set")
	require.Equal(t, "finished", attrs["reason"])
	_, hasQueue := attrs["queue"]
	require.False(t, hasQueue, "queue should not be in attributes when empty")
}

func TestBuildEnvelope_IncludesCollectorVersion(t *testing.T) {
	ev := referenceEvent()
	raw, err := gpu.BuildEnvelope(ev, "v0.1.0")
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	source := m["source"].(map[string]any)
	require.Equal(t, "v0.1.0", source["collector_version"])
}

func TestFormatRecordID(t *testing.T) {
	tests := []struct {
		name string
		uid  string
		seq  int
		want string
	}{
		{
			name: "typical interval",
			uid:  "wl-9be2c1d4",
			seq:  7,
			want: "wl-9be2c1d4-interval-0007",
		},
		{
			name: "first interval",
			uid:  "wl-abc123",
			seq:  0,
			want: "wl-abc123-interval-0000",
		},
		{
			name: "large sequence number",
			uid:  "wl-def456",
			seq:  9999,
			want: "wl-def456-interval-9999",
		},
		{
			name: "five digit sequence",
			uid:  "wl-ghi789",
			seq:  10000,
			want: "wl-ghi789-interval-10000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gpu.FormatRecordID(tt.uid, tt.seq)
			require.Equal(t, tt.want, got)
		})
	}
}
