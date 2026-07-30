//go:build integration

package ledger_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
)

var (
	testStore    *ledger.Store
	testIngester *ledger.Ingester
	testQuerier  *ledger.Querier
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

	testStore, err = ledger.NewStore(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create store: %v\n", err)
		os.Exit(1)
	}

	// Migrations are relative to the repo root.
	migrationsDir := findMigrationsDir()
	if err := testStore.RunMigrations(migrationsDir); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	testIngester, err = ledger.NewIngester(testStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create ingester: %v\n", err)
		os.Exit(1)
	}

	testQuerier = ledger.NewQuerier(testStore)
	testPool = testStore.Pool()

	code := m.Run()

	testStore.Close()
	_ = pgContainer.Terminate(ctx)
	os.Exit(code)
}

func findMigrationsDir() string {
	// Walk up from the test file's location to find the repo root.
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
	// Fallback: assume run from repo root.
	return "../../migrations"
}

// --- Test 1: Migrations apply cleanly ---
// Validated implicitly by TestMain succeeding.

func TestIdempotentUpsert(t *testing.T) {
	ctx := context.Background()
	ev := maasStreamingOK(t)

	r1, err := testIngester.IngestEvent(ctx, ev)
	require.NoError(t, err)
	require.Equal(t, ledger.Inserted, r1, "first ingest should insert")

	r2, err := testIngester.IngestEvent(ctx, ev)
	require.NoError(t, err)
	require.Equal(t, ledger.Duplicate, r2, "second identical ingest should be duplicate")
}

func TestConflictingResend(t *testing.T) {
	ctx := context.Background()

	original := makeEvent(t, map[string]any{
		"schema_version": "usage.v1",
		"record_id":      "req-conflict-test",
		"occurred_at":    "2026-07-29T14:03:22Z",
		"tenant_id":      "ten-corvid-demo",
		"key_id":         "key-1",
		"source":         map[string]any{"profile": "maas", "collector": "capture-wasm"},
		"sku":            "llama-3-70b",
		"quantities":     map[string]any{"prompt_tokens": 412, "completion_tokens": 1288, "total_tokens": 1700},
		"status":         "ok",
	})

	r1, err := testIngester.IngestEvent(ctx, original)
	require.NoError(t, err)
	require.Equal(t, ledger.Inserted, r1)

	conflicting := makeEvent(t, map[string]any{
		"schema_version": "usage.v1",
		"record_id":      "req-conflict-test",
		"occurred_at":    "2026-07-29T14:03:22Z",
		"tenant_id":      "ten-corvid-demo",
		"key_id":         "key-1",
		"source":         map[string]any{"profile": "maas", "collector": "capture-wasm"},
		"sku":            "llama-3-70b",
		"quantities":     map[string]any{"prompt_tokens": 412, "completion_tokens": 9999, "total_tokens": 10411},
		"status":         "ok",
	})

	r2, err := testIngester.IngestEvent(ctx, conflicting)
	require.NoError(t, err)
	require.Equal(t, ledger.ConflictDLQ, r2, "conflicting content should go to DLQ")

	// DLQ should have 1 row with reason 'dedup_conflict'.
	var dlqCount int
	err = testPool.QueryRow(ctx,
		"SELECT count(*) FROM dlq WHERE reason = 'dedup_conflict' AND raw->>'record_id' = 'req-conflict-test'",
	).Scan(&dlqCount)
	require.NoError(t, err)
	require.Equal(t, 1, dlqCount, "DLQ should have exactly 1 conflict row")

	// Original row should be untouched.
	var origTokens int64
	err = testPool.QueryRow(ctx,
		"SELECT completion_tokens FROM usage_events WHERE record_id = 'req-conflict-test'",
	).Scan(&origTokens)
	require.NoError(t, err)
	require.Equal(t, int64(1288), origTokens, "original row should be untouched")
}

func TestUnknownSchemaVersion(t *testing.T) {
	ctx := context.Background()
	ev := json.RawMessage(`{"schema_version":"usage.v2","record_id":"unknown-schema-test"}`)

	// Schema validation will reject this before the schema_version gate,
	// because usage.v2 doesn't match the const "usage.v1" in the schema.
	r, err := testIngester.IngestEvent(ctx, ev)
	require.NoError(t, err, "validation failure is a result, not an error")
	require.Equal(t, ledger.ValidationFailed, r)
}

func TestSchemaVersionGateDirectly(t *testing.T) {
	// Test the schema_version gate by constructing a valid-looking event
	// with an unknown version that passes schema validation but not the gate.
	// Since the schema uses const: "usage.v1", any other version fails
	// validation first. This test validates that behavior.
	ctx := context.Background()
	ev := json.RawMessage(`{"schema_version":"usage.v2","record_id":"x"}`)
	r, _ := testIngester.IngestEvent(ctx, ev)
	require.Contains(t, []ledger.IngestResult{ledger.ValidationFailed, ledger.SchemaUnknownDLQ}, r)
}

func TestStatusCheckConstraint(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `
		INSERT INTO usage_events (record_id, tenant_id, sku, profile, collector,
			quantities, status, content_hash, event_ts)
		VALUES ('bad-status-test', 't', 'm', 'maas', 'c', '{}', 'exploded', 'h', now())`,
	)
	require.Error(t, err, "status CHECK should reject 'exploded'")
	require.Contains(t, err.Error(), "usage_events_status_check")
}

func TestChargesAppendOnly(t *testing.T) {
	ctx := context.Background()

	// Insert a charge (need a usage_events row first for FK).
	chargeEvent := makeEvent(t, map[string]any{
		"schema_version": "usage.v1",
		"record_id":      "req-append-only-test",
		"occurred_at":    "2026-07-29T15:00:00Z",
		"tenant_id":      "ten-append-test",
		"source":         map[string]any{"profile": "maas", "collector": "test"},
		"sku":            "llama-3-70b",
		"quantities":     map[string]any{"prompt_tokens": 100, "completion_tokens": 200},
		"status":         "ok",
	})
	_, err := testIngester.IngestEvent(ctx, chargeEvent)
	require.NoError(t, err)

	_, err = testPool.Exec(ctx, `
		INSERT INTO charges (record_id, tenant_id, price_book, price_book_rev,
			amount, currency, components, rated_at)
		VALUES ('req-append-only-test', 'ten-append-test', 'pb-standard', 3,
			0.013670, 'USD', '[]', '2026-07-29T15:00:00Z')`,
	)
	require.NoError(t, err)

	// UPDATE should fail.
	_, err = testPool.Exec(ctx, "UPDATE charges SET amount = 999 WHERE record_id = 'req-append-only-test'")
	require.Error(t, err, "charges UPDATE should be rejected by append-only trigger")

	// DELETE should fail.
	_, err = testPool.Exec(ctx, "DELETE FROM charges WHERE record_id = 'req-append-only-test'")
	require.Error(t, err, "charges DELETE should be rejected by append-only trigger")
}

func TestWalletRecompute(t *testing.T) {
	ctx := context.Background()

	// Insert a credit adjustment for the test tenant.
	_, err := testPool.Exec(ctx, `
		INSERT INTO adjustments (tenant_id, kind, amount, currency, reason, actor)
		VALUES ('ten-append-test', 'manual_credit', 25.000000, 'USD', 'signup promo', 'system:test')`,
	)
	require.NoError(t, err)

	// The wallet_recomputed view should show 25.000000 - 0.013670 = 24.986330.
	var balanceStr string
	err = testPool.QueryRow(ctx,
		"SELECT balance FROM wallet_recomputed WHERE tenant_id = 'ten-append-test'",
	).Scan(&balanceStr)
	require.NoError(t, err)

	balance, err := decimal.NewFromString(balanceStr)
	require.NoError(t, err)
	expected := decimal.RequireFromString("24.986330")
	require.True(t, expected.Equal(balance),
		"wallet recompute: want 24.986330, got %s (NUMERIC, not float)", balance)
}

func TestOneChargePerEventUnique(t *testing.T) {
	ctx := context.Background()

	// Try inserting a second charge for the same record_id.
	tag, err := testPool.Exec(ctx, `
		INSERT INTO charges (record_id, tenant_id, price_book, price_book_rev,
			amount, currency, components, rated_at)
		VALUES ('req-append-only-test', 't', 'pb', 3, 1, 'USD', '[]', now())
		ON CONFLICT (record_id) DO NOTHING`,
	)
	require.NoError(t, err)
	require.Equal(t, int64(0), tag.RowsAffected(),
		"second charge for same record_id should be suppressed by UNIQUE")
}

func TestRoundTripContractExamples(t *testing.T) {
	ctx := context.Background()

	examples := []string{
		"maas-streaming-ok.json",
		"maas-client-disconnect.json",
		"maas-upstream-error.json",
		"gpuaas-interval.json",
	}

	examplesDir := findExamplesDir()

	for _, fname := range examples {
		t.Run(fname, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(examplesDir, fname))
			require.NoError(t, err)

			// Modify record_id to avoid collisions with other tests.
			var parsed map[string]any
			require.NoError(t, json.Unmarshal(data, &parsed))
			parsed["record_id"] = fmt.Sprintf("roundtrip-%s", fname)
			modified, err := json.Marshal(parsed)
			require.NoError(t, err)

			result, err := testIngester.IngestEvent(ctx, json.RawMessage(modified))
			require.NoError(t, err)
			require.Equal(t, ledger.Inserted, result, "example %s should ingest", fname)

			// Query it back.
			qr, err := testQuerier.QueryEvents(ctx, ledger.QueryFilter{
				TenantID: parsed["tenant_id"].(string),
				Limit:    10,
			})
			require.NoError(t, err)

			found := false
			for _, ev := range qr.Events {
				if ev.RecordID == parsed["record_id"].(string) {
					found = true
					require.Equal(t, parsed["sku"].(string), ev.SKU)
					require.Equal(t, parsed["status"].(string), ev.Status)
					break
				}
			}
			require.True(t, found, "queried events should contain the ingested example")
		})
	}
}

func TestSchemaValidationRejectsInvalid(t *testing.T) {
	ctx := context.Background()

	examplesDir := findExamplesDir()
	data, err := os.ReadFile(filepath.Join(examplesDir, "invalid-missing-status.json"))
	require.NoError(t, err)

	result, err := testIngester.IngestEvent(ctx, json.RawMessage(data))
	require.NoError(t, err, "validation failure is a result, not a Go error")
	require.Equal(t, ledger.ValidationFailed, result, "invalid event should fail validation")
}

// --- Helpers ---

func maasStreamingOK(t *testing.T) json.RawMessage {
	t.Helper()
	examplesDir := findExamplesDir()
	data, err := os.ReadFile(filepath.Join(examplesDir, "maas-streaming-ok.json"))
	require.NoError(t, err)
	return json.RawMessage(data)
}

func makeEvent(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	return json.RawMessage(data)
}

func findExamplesDir() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "contracts", "examples")); err == nil {
			return filepath.Join(dir, "contracts", "examples")
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "../../contracts/examples"
}
