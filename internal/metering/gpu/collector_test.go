package gpu

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	ctrl "sigs.k8s.io/controller-runtime"
)

// mockSender records envelopes sent through the EventSender interface.
type mockSender struct {
	mu      sync.Mutex
	sent    []json.RawMessage
	sendErr error
}

func (m *mockSender) Send(_ context.Context, envelope json.RawMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sent = append(m.sent, envelope)
	return nil
}

func (m *mockSender) sentCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sent)
}

func (m *mockSender) sentAt(i int) json.RawMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sent[i]
}

// newTestCollector creates a Collector with a mock sender and no real k8s client.
func newTestCollector(sender *mockSender) *Collector {
	return &Collector{
		Log: ctrl.Log.WithName("test"),
		Config: CollectorConfig{
			IntervalDuration: time.Hour,
			CollectorVersion: "test-0.1",
			DefaultGeo:       "us-east-1",
		},
		Sender:    sender,
		workloads: make(map[string]*WorkloadState),
	}
}

func TestEmitFinalInterval_CorrectGPUSeconds(t *testing.T) {
	sender := &mockSender{}
	c := newTestCollector(sender)

	baseTime := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	endTime := baseTime.Add(30 * time.Minute)

	ws := &WorkloadState{
		WorkloadUID:     "wl-test-123",
		WorkloadName:    "train-job",
		Namespace:       "ml-team",
		TenantID:        "ten-acme",
		SKU:             "h200",
		SliceFraction:   1.0,
		Geo:             "us-east-1",
		Queue:           "batch",
		GPUCount:        2,
		AdmittedAt:      baseTime,
		LastIntervalEnd: baseTime,
		IntervalSeq:     0,
	}

	c.emitFinalInterval(context.Background(), c.Log, ws, endTime)

	require.Equal(t, 1, sender.sentCount(), "should emit exactly one event")

	var envelope map[string]any
	require.NoError(t, json.Unmarshal(sender.sentAt(0), &envelope))

	quantities := envelope["quantities"].(map[string]any)
	// 30 minutes = 1800 seconds, 2 GPUs = 3600 gpu_seconds
	require.InDelta(t, 3600.0, quantities["gpu_seconds"], 0.001,
		"30min x 2 GPUs = 3600 gpu_seconds")

	require.Equal(t, "wl-test-123-interval-0000", envelope["record_id"])
	require.Equal(t, "ten-acme", envelope["tenant_id"])
	require.Equal(t, "h200", envelope["sku"])

	interval := envelope["interval"].(map[string]any)
	require.Equal(t, "2026-07-29T14:00:00Z", interval["start"])
	require.Equal(t, "2026-07-29T14:30:00Z", interval["end"])
}

func TestEmitFinalInterval_NoEmissionWhenEndBeforeStart(t *testing.T) {
	sender := &mockSender{}
	c := newTestCollector(sender)

	baseTime := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)

	ws := &WorkloadState{
		WorkloadUID:     "wl-old",
		WorkloadName:    "stale-job",
		TenantID:        "ten-acme",
		SKU:             "a100",
		GPUCount:        1,
		LastIntervalEnd: baseTime,
		IntervalSeq:     0,
	}

	// endTime before LastIntervalEnd: nothing emitted
	c.emitFinalInterval(context.Background(), c.Log, ws, baseTime.Add(-5*time.Minute))
	require.Equal(t, 0, sender.sentCount(), "should not emit when endTime < LastIntervalEnd")

	// endTime equal to LastIntervalEnd: nothing emitted
	c.emitFinalInterval(context.Background(), c.Log, ws, baseTime)
	require.Equal(t, 0, sender.sentCount(), "should not emit when endTime == LastIntervalEnd")
}

func TestEmitPendingIntervals_HourlyEmission(t *testing.T) {
	sender := &mockSender{}
	c := newTestCollector(sender)

	baseTime := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	now := baseTime.Add(2*time.Hour + 30*time.Minute) // 2.5 hours later

	ws := &WorkloadState{
		WorkloadUID:     "wl-long-run",
		WorkloadName:    "long-training",
		Namespace:       "ml-team",
		TenantID:        "ten-helix",
		SKU:             "h200",
		SliceFraction:   1.0,
		Geo:             "us-east-1",
		Queue:           "priority",
		GPUCount:        4,
		AdmittedAt:      baseTime,
		LastIntervalEnd: baseTime,
		IntervalSeq:     0,
	}
	c.workloads["wl-long-run"] = ws

	c.emitPendingIntervals(context.Background(), now)

	// 2.5 hours elapsed with 1h interval = 2 full intervals emitted (seq 0 and 1)
	require.Equal(t, 2, sender.sentCount(), "should emit 2 interval events for 2.5h elapsed")

	// Verify first interval (seq 0)
	var env0 map[string]any
	require.NoError(t, json.Unmarshal(sender.sentAt(0), &env0))
	q0 := env0["quantities"].(map[string]any)
	require.InDelta(t, 14400.0, q0["gpu_seconds"], 0.001,
		"1h x 4 GPUs = 14400 gpu_seconds")
	require.Equal(t, "wl-long-run-interval-0000", env0["record_id"])

	i0 := env0["interval"].(map[string]any)
	require.Equal(t, "2026-07-29T10:00:00Z", i0["start"])
	require.Equal(t, "2026-07-29T11:00:00Z", i0["end"])

	// Verify second interval (seq 1)
	var env1 map[string]any
	require.NoError(t, json.Unmarshal(sender.sentAt(1), &env1))
	q1 := env1["quantities"].(map[string]any)
	require.InDelta(t, 14400.0, q1["gpu_seconds"], 0.001)
	require.Equal(t, "wl-long-run-interval-0001", env1["record_id"])

	i1 := env1["interval"].(map[string]any)
	require.Equal(t, "2026-07-29T11:00:00Z", i1["start"])
	require.Equal(t, "2026-07-29T12:00:00Z", i1["end"])

	// Verify state was advanced
	require.Equal(t, baseTime.Add(2*time.Hour), ws.LastIntervalEnd,
		"LastIntervalEnd should advance by 2 full intervals")
	require.Equal(t, 2, ws.IntervalSeq, "IntervalSeq should be 2 after emitting seq 0 and 1")
}

func TestEmitPendingIntervals_NothingBeforeInterval(t *testing.T) {
	sender := &mockSender{}
	c := newTestCollector(sender)

	baseTime := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	now := baseTime.Add(30 * time.Minute) // only 30 min, less than 1h interval

	ws := &WorkloadState{
		WorkloadUID:     "wl-short",
		WorkloadName:    "quick-job",
		TenantID:        "ten-acme",
		SKU:             "a100",
		GPUCount:        1,
		LastIntervalEnd: baseTime,
		IntervalSeq:     0,
	}
	c.workloads["wl-short"] = ws

	c.emitPendingIntervals(context.Background(), now)

	require.Equal(t, 0, sender.sentCount(),
		"should not emit any events when elapsed time < interval duration")
	require.Equal(t, 0, ws.IntervalSeq, "IntervalSeq should remain 0")
}

func TestEmitPendingIntervals_SendError_StopsEmission(t *testing.T) {
	sender := &mockSender{sendErr: context.DeadlineExceeded}
	c := newTestCollector(sender)

	baseTime := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	now := baseTime.Add(3 * time.Hour)

	ws := &WorkloadState{
		WorkloadUID:     "wl-err",
		WorkloadName:    "err-job",
		TenantID:        "ten-acme",
		SKU:             "a100",
		GPUCount:        1,
		LastIntervalEnd: baseTime,
		IntervalSeq:     0,
	}
	c.workloads["wl-err"] = ws

	c.emitPendingIntervals(context.Background(), now)

	// Send error should stop further emission attempts for this workload
	require.Equal(t, 0, ws.IntervalSeq,
		"IntervalSeq should not advance when send fails")
}

func TestActiveWorkloadCount(t *testing.T) {
	sender := &mockSender{}
	c := newTestCollector(sender)

	require.Equal(t, 0, c.ActiveWorkloadCount())

	c.workloads["uid-1"] = &WorkloadState{WorkloadUID: "uid-1"}
	c.workloads["uid-2"] = &WorkloadState{WorkloadUID: "uid-2"}

	require.Equal(t, 2, c.ActiveWorkloadCount())
}
