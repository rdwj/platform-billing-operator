//go:build integration

package ledger_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMigrationsAppliedInSequence verifies that goose recorded every
// migration file in the migrations/ directory, in order, with no gaps.
func TestMigrationsAppliedInSequence(t *testing.T) {
	ctx := context.Background()
	migrationsDir := findMigrationsDir()

	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	var wantVersions []int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		var v int64
		if _, err := fmt.Sscanf(e.Name(), "%d_", &v); err == nil {
			wantVersions = append(wantVersions, v)
		}
	}
	sort.Slice(wantVersions, func(i, j int) bool { return wantVersions[i] < wantVersions[j] })
	require.NotEmpty(t, wantVersions)

	rows, err := testPool.Query(ctx,
		"SELECT version_id FROM goose_db_version WHERE version_id > 0 ORDER BY version_id")
	require.NoError(t, err)
	defer rows.Close()

	var gotVersions []int64
	for rows.Next() {
		var v int64
		require.NoError(t, rows.Scan(&v))
		gotVersions = append(gotVersions, v)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, wantVersions, gotVersions,
		"goose version table should contain every migration file version, in order")
}

// TestUsageEventsNeverDropsColumns statically analyses migration files
// to ensure no migration after 0001 drops or renames a usage_events column.
// The audit trail depends on columns being stable once created.
func TestUsageEventsNeverDropsColumns(t *testing.T) {
	migrationsDir := findMigrationsDir()
	entries, err := os.ReadDir(migrationsDir)
	require.NoError(t, err)

	destructive := regexp.MustCompile(
		`(?i)ALTER\s+TABLE\s+usage_events\s+DROP\s+COLUMN|` +
			`ALTER\s+TABLE\s+usage_events\s+RENAME\s+COLUMN`)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if strings.HasPrefix(e.Name(), "0001_") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
		require.NoError(t, err)

		upSection := extractGooseUp(string(data))
		if destructive.MatchString(upSection) {
			t.Errorf("migration %s contains a destructive change to usage_events "+
				"(DROP COLUMN or RENAME COLUMN in +goose Up section)", e.Name())
		}
	}
}

// extractGooseUp returns the +goose Up section of a goose migration,
// stopping at +goose Down or EOF.
func extractGooseUp(sql string) string {
	start := strings.Index(sql, "-- +goose Up")
	if start < 0 {
		return sql
	}
	body := sql[start:]
	if end := strings.Index(body, "-- +goose Down"); end > 0 {
		return body[:end]
	}
	return body
}

// TestSchemaVerDefaultsToUsageV1 inserts a row without explicit schema_ver
// and verifies the column defaults to 'usage.v1'.
func TestSchemaVerDefaultsToUsageV1(t *testing.T) {
	ctx := context.Background()

	recordID := "schema-ver-default-test"
	_, err := testPool.Exec(ctx, `
		INSERT INTO usage_events
			(record_id, tenant_id, sku, profile, collector,
			 quantities, status, content_hash, event_ts)
		VALUES ($1, 'ten-schema-test', 'test-model', 'maas', 'test',
			'{"prompt_tokens":1}', 'ok', 'abc123', now())`,
		recordID)
	require.NoError(t, err)

	var schemaVer string
	err = testPool.QueryRow(ctx,
		"SELECT schema_ver FROM usage_events WHERE record_id = $1", recordID,
	).Scan(&schemaVer)
	require.NoError(t, err)
	require.Equal(t, "usage.v1", schemaVer,
		"schema_ver column must default to 'usage.v1' — backward-compat contract")
}

// TestContentHashNotNull verifies the content_hash column rejects NULL values.
func TestContentHashNotNull(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `
		INSERT INTO usage_events
			(record_id, tenant_id, sku, profile, collector,
			 quantities, status, content_hash, event_ts)
		VALUES ('content-hash-null-test', 'ten-test', 'm', 'maas', 'c',
			'{}', 'ok', NULL, now())`)
	require.Error(t, err, "content_hash must be NOT NULL — audit integrity")
	require.Contains(t, err.Error(), "content_hash",
		"error should reference the content_hash column")
}

// TestChargesRecordIdForeignKey verifies the FK from charges.record_id
// to usage_events.record_id — rated events must be traceable to source.
func TestChargesRecordIdForeignKey(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `
		INSERT INTO charges
			(record_id, tenant_id, price_book, price_book_rev,
			 amount, currency, components, rated_at)
		VALUES ('nonexistent-event-fk-test', 'ten-test', 'pb', 1,
			1.000000, 'USD', '[]', now())`)
	require.Error(t, err, "charges FK should reject record_id not in usage_events")
	require.Contains(t, err.Error(), "charges_record_id_fkey",
		"error should reference the charges FK constraint")
}

// TestDriftReportsSignatureColumn verifies the signature column exists
// and is NOT NULL via information_schema introspection.
func TestDriftReportsSignatureColumn(t *testing.T) {
	ctx := context.Background()

	var isNullable string
	var dataType string
	err := testPool.QueryRow(ctx, `
		SELECT is_nullable, data_type
		FROM information_schema.columns
		WHERE table_name = 'drift_reports' AND column_name = 'signature'`,
	).Scan(&isNullable, &dataType)
	require.NoError(t, err, "drift_reports.signature column must exist")
	require.Equal(t, "NO", isNullable,
		"drift_reports.signature must be NOT NULL — audit verification requires it")
	require.Equal(t, "text", dataType)
}

// TestAuditTablesExist verifies that the core audit-trail tables are
// present after all migrations: usage_events, charges, drift_reports.
func TestAuditTablesExist(t *testing.T) {
	ctx := context.Background()

	tables := []string{"usage_events", "charges", "drift_reports"}
	for _, tbl := range tables {
		t.Run(tbl, func(t *testing.T) {
			var exists bool
			err := testPool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM information_schema.tables
					WHERE table_schema = 'public' AND table_name = $1
				)`, tbl).Scan(&exists)
			require.NoError(t, err)
			require.True(t, exists, "table %s must exist after migrations", tbl)
		})
	}
}

// TestUsageEventsColumnInventory verifies that usage_events has the
// expected set of columns from the usage.v1 contract. New columns may
// be added (forward-compat), but the contract columns must be present.
func TestUsageEventsColumnInventory(t *testing.T) {
	ctx := context.Background()

	contractColumns := []string{
		"record_id", "schema_ver", "tenant_id", "key_id", "sku", "geo",
		"profile", "collector",
		"prompt_tokens", "completion_tokens", "reasoning_tokens", "cached_tokens",
		"quantities", "status", "partial", "stream", "estimated", "latency_ms",
		"attributes", "content_hash", "event_ts", "ingested_at",
	}

	rows, err := testPool.Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'usage_events'`)
	require.NoError(t, err)
	defer rows.Close()

	actual := make(map[string]bool)
	for rows.Next() {
		var col string
		require.NoError(t, rows.Scan(&col))
		actual[col] = true
	}
	require.NoError(t, rows.Err())

	for _, col := range contractColumns {
		require.True(t, actual[col],
			"usage_events must have column %q (usage.v1 contract)", col)
	}
}
