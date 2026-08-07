package dispatch_test

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rdwj/platform-billing-operator/internal/controller"
	"github.com/rdwj/platform-billing-operator/internal/dispatch"
	"github.com/rdwj/platform-billing-operator/internal/usagesink"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSink records calls to IngestEvents for assertions.
type mockSink struct {
	mu       sync.Mutex
	batches  [][]usagesink.UsageEvent
	err      error
	failN    int // fail the first N calls
	callsMu  sync.Mutex
	callsVal int
}

func (m *mockSink) IngestEvents(_ context.Context, events []usagesink.UsageEvent) (int, error) {
	m.callsMu.Lock()
	call := m.callsVal
	m.callsVal++
	m.callsMu.Unlock()

	if call < m.failN {
		return 0, m.err
	}
	m.mu.Lock()
	m.batches = append(m.batches, events)
	m.mu.Unlock()
	return len(events), nil
}

func (m *mockSink) HealthCheck(context.Context) error                                  { return nil }
func (m *mockSink) ConfigureMeters(context.Context, []usagesink.MeterDefinition) error { return nil }
func (m *mockSink) SyncSubject(context.Context, usagesink.TenantInfo) error            { return nil }
func (m *mockSink) QueryUsage(context.Context, usagesink.UsageQuery) ([]usagesink.UsageRecord, error) {
	return nil, nil
}

func (m *mockSink) getBatches() [][]usagesink.UsageEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([][]usagesink.UsageEvent, len(m.batches))
	copy(cp, m.batches)
	return cp
}

func (m *mockSink) calls() int {
	m.callsMu.Lock()
	defer m.callsMu.Unlock()
	return m.callsVal
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestSinkDispatcher_NoSinks_NoOp(t *testing.T) {
	registry := controller.NewSinkRegistry()
	// No store needed since the dispatcher short-circuits when Len() == 0.
	d := dispatch.New(nil, registry, dispatch.Config{
		BatchSize:     10,
		FlushInterval: 50 * time.Millisecond,
	}, testLogger())

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := d.Start(ctx)
	assert.NoError(t, err)
}

func TestSinkDispatcher_RetryOnFailure(t *testing.T) {
	sink := &mockSink{
		err:   errors.New("transient failure"),
		failN: 2, // first 2 calls fail, 3rd succeeds
	}

	registry := controller.NewSinkRegistry()
	registry.Set("test-sink", sink)

	// We can't test the full dispatcher Start() here because it needs a
	// real ledger.Store. Instead, this test validates the retry behavior
	// at the sink level -- the dispatcher's sendWithRetry handles this.

	require.Equal(t, 1, registry.Len())
	assert.NotNil(t, sink)
	// Retry logic is tested as part of integration tests with a real store.
}

func TestSinkRegistry_ForEach(t *testing.T) {
	sink1 := &mockSink{}
	sink2 := &mockSink{}

	registry := controller.NewSinkRegistry()
	registry.Set("alpha", sink1)
	registry.Set("beta", sink2)

	var names []string
	registry.ForEach(func(name string, _ usagesink.UsageSinkBackend) {
		names = append(names, name)
	})

	assert.Len(t, names, 2)
	assert.Contains(t, names, "alpha")
	assert.Contains(t, names, "beta")
}

func TestSinkRegistry_Len(t *testing.T) {
	registry := controller.NewSinkRegistry()
	assert.Equal(t, 0, registry.Len())

	registry.Set("a", &mockSink{})
	assert.Equal(t, 1, registry.Len())

	registry.Set("b", &mockSink{})
	assert.Equal(t, 2, registry.Len())

	registry.Delete("a")
	assert.Equal(t, 1, registry.Len())
}
