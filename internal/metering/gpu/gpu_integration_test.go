//go:build integration

package gpu_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/metering/gpu"
)

var (
	testIngester *ledger.Ingester
	testPool     *pgxpool.Pool
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("billing_ledger_test"),
		postgres.WithUsername("billing"),
		postgres.WithPassword("billing"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "start postgres container: %v\n", err)
		os.Exit(1)
	}

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "get connection string: %v\n", err)
		os.Exit(1)
	}

	store, err := ledger.NewStore(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create store: %v\n", err)
		os.Exit(1)
	}

	migrationsDir := findMigrationsDir()
	if err := store.RunMigrations(migrationsDir); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	testIngester, err = ledger.NewIngester(store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create ingester: %v\n", err)
		os.Exit(1)
	}

	testPool = store.Pool()

	code := m.Run()

	store.Close()
	_ = pgContainer.Terminate(ctx)
	os.Exit(code)
}

func findMigrationsDir() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "migrations")); err == nil {
			return filepath.Join(dir, "migrations")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "../../../migrations"
}

// TestSingleIntervalIngest verifies that a GPU interval event built by
// BuildEnvelope is accepted by the ledger ingester and stored correctly.
func TestSingleIntervalIngest(t *testing.T) {
	ctx := context.Background()

	ev := gpu.IntervalEvent{
		RecordID:      gpu.FormatRecordID("wl-inttest-single", 0),
		TenantID:      "ten-helix",
		SKU:           "h200-mig-3g.40gb",
		Geo:           "us-east-1",
		GPUSeconds:    3600,
		SliceFraction: 0.375,
		IntervalStart: time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC),
		IntervalEnd:   time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC),
		IntervalSeq:   0,
		Queue:         "team-ml-batch",
		WorkloadName:  "train-run-42",
		Status:        "ok",
		OccurredAt:    time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC),
	}
	envelope, err := gpu.BuildEnvelope(ev, "0.0.1-test")
	require.NoError(t, err)

	result, err := testIngester.IngestEvent(ctx, envelope)
	require.NoError(t, err)
	require.Equal(t, ledger.Inserted, result)

	// Verify the stored row.
	var profile, collector, sku, status string
	err = testPool.QueryRow(ctx,
		"SELECT profile, collector, sku, status FROM usage_events WHERE record_id = $1",
		"wl-inttest-single-interval-0000",
	).Scan(&profile, &collector, &sku, &status)
	require.NoError(t, err)
	require.Equal(t, "gpuaas", profile)
	require.Equal(t, "kueue-collector", collector)
	require.Equal(t, "h200-mig-3g.40gb", sku)
	require.Equal(t, "ok", status)
}

// TestMultipleIntervalsIngest verifies that multiple sequential interval events
// for the same workload are each stored as distinct rows.
func TestMultipleIntervalsIngest(t *testing.T) {
	ctx := context.Background()

	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		ev := gpu.IntervalEvent{
			RecordID:      gpu.FormatRecordID("wl-inttest-multi", i),
			TenantID:      "ten-helix",
			SKU:           "h200",
			GPUSeconds:    3600,
			SliceFraction: 1.0,
			IntervalStart: base.Add(time.Duration(i) * time.Hour),
			IntervalEnd:   base.Add(time.Duration(i+1) * time.Hour),
			IntervalSeq:   i,
			Status:        "ok",
			OccurredAt:    base.Add(time.Duration(i+1) * time.Hour),
		}
		envelope, err := gpu.BuildEnvelope(ev, "0.0.1-test")
		require.NoError(t, err)

		result, err := testIngester.IngestEvent(ctx, envelope)
		require.NoError(t, err, "interval %d should ingest without error", i)
		require.Equal(t, ledger.Inserted, result, "interval %d should insert", i)
	}

	// Verify all 3 rows exist.
	var count int
	err := testPool.QueryRow(ctx,
		"SELECT count(*) FROM usage_events WHERE record_id LIKE 'wl-inttest-multi-interval-%'",
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 3, count, "all 3 interval events should be stored")

	// Verify record_id format for each.
	for i := 0; i < 3; i++ {
		expectedID := gpu.FormatRecordID("wl-inttest-multi", i)
		var exists bool
		err := testPool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM usage_events WHERE record_id = $1)",
			expectedID,
		).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "record_id %q should exist", expectedID)
	}
}

// TestIdempotentReplay verifies that ingesting the same GPU event twice
// produces Inserted then Duplicate, matching the at-least-once guarantee.
func TestIdempotentReplay(t *testing.T) {
	ctx := context.Background()

	ev := gpu.IntervalEvent{
		RecordID:      gpu.FormatRecordID("wl-inttest-idemp", 0),
		TenantID:      "ten-helix",
		SKU:           "h200",
		GPUSeconds:    3600,
		SliceFraction: 1.0,
		IntervalStart: time.Date(2026, 7, 29, 16, 0, 0, 0, time.UTC),
		IntervalEnd:   time.Date(2026, 7, 29, 17, 0, 0, 0, time.UTC),
		IntervalSeq:   0,
		Status:        "ok",
		OccurredAt:    time.Date(2026, 7, 29, 17, 0, 0, 0, time.UTC),
	}
	envelope, err := gpu.BuildEnvelope(ev, "0.0.1-test")
	require.NoError(t, err)

	r1, err := testIngester.IngestEvent(ctx, envelope)
	require.NoError(t, err)
	require.Equal(t, ledger.Inserted, r1, "first ingest should insert")

	r2, err := testIngester.IngestEvent(ctx, envelope)
	require.NoError(t, err)
	require.Equal(t, ledger.Duplicate, r2, "second identical ingest should be duplicate")
}

// TestSchemaValidation_IntervalRequired verifies that the schema's conditional
// rule (gpuaas profile requires interval) causes the ingester to reject an
// event missing the interval field.
func TestSchemaValidation_IntervalRequired(t *testing.T) {
	ctx := context.Background()

	badEvent := json.RawMessage(`{
		"schema_version": "usage.v1",
		"record_id": "wl-inttest-bad-no-interval",
		"occurred_at": "2026-07-29T15:00:00Z",
		"tenant_id": "ten-helix",
		"source": {"profile": "gpuaas", "collector": "kueue-collector"},
		"sku": "h200",
		"quantities": {"gpu_seconds": 3600},
		"status": "ok"
	}`)

	result, err := testIngester.IngestEvent(ctx, badEvent)
	require.NoError(t, err, "validation failure is a result, not a Go error")
	require.Equal(t, ledger.ValidationFailed, result,
		"gpuaas event without interval should fail schema validation")
}
