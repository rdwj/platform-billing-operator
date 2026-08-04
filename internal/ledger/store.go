package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/shopspring/decimal"
)

var ErrIdempotencyConflict = errors.New("idempotency key exists with different content")
var ErrKeyNotFound = errors.New("api key not found")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create pgx pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func (s *Store) RunMigrations(migrationsDir string) error {
	dsn := s.pool.Config().ConnString()
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql.DB for migrations: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	return goose.Up(db, migrationsDir)
}

func (s *Store) InsertEvent(ctx context.Context, ev *UsageEvent) error {
	quantitiesJSON, err := json.Marshal(ev.Quantities)
	if err != nil {
		return fmt.Errorf("marshal quantities: %w", err)
	}
	attrsJSON, err := json.Marshal(ev.Attributes)
	if err != nil {
		return fmt.Errorf("marshal attributes: %w", err)
	}

	_, err = s.pool.Exec(ctx, `
		INSERT INTO usage_events (
			record_id, tenant_id, key_id, sku, geo,
			profile, collector,
			prompt_tokens, completion_tokens, reasoning_tokens, cached_tokens,
			quantities, status, partial, stream, estimated, latency_ms,
			attributes, content_hash, event_ts
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7,
			$8, $9, $10, $11,
			$12, $13, $14, $15, false, $16,
			$17, $18, $19
		) ON CONFLICT (record_id) DO NOTHING`,
		ev.RecordID, ev.TenantID, nilIfEmpty(ev.KeyID), ev.SKU, nilIfEmpty(ev.Geo),
		ev.Profile, ev.Collector,
		ev.PromptTokens, ev.CompletionTokens, ev.ReasoningTokens, ev.CachedTokens,
		quantitiesJSON, ev.Status, ev.Partial, ev.Stream, ev.LatencyMs,
		attrsJSON, ev.ContentHash, ev.EventTS,
	)
	return err
}

func (s *Store) GetContentHash(ctx context.Context, recordID string) (string, bool, error) {
	var hash string
	err := s.pool.QueryRow(ctx,
		"SELECT content_hash FROM usage_events WHERE record_id = $1",
		recordID,
	).Scan(&hash)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return hash, true, nil
}

func (s *Store) InsertDLQ(ctx context.Context, raw json.RawMessage, reason, source string) error {
	_, err := s.pool.Exec(ctx,
		"INSERT INTO dlq (raw, reason, source) VALUES ($1, $2, $3)",
		raw, reason, source,
	)
	return err
}

func (s *Store) AdvanceWatermark(ctx context.Context, ts time.Time) error {
	tsStr := ts.UTC().Format(time.RFC3339Nano)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ledger_meta (key, value, updated_at)
		VALUES ('watermark_ts', $1, now())
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, updated_at = now()
		WHERE ledger_meta.value::timestamptz < $1::timestamptz`,
		tsStr,
	)
	return err
}

func (s *Store) QueryEvents(ctx context.Context, f QueryFilter) (*QueryResult, error) {
	var clauses []string
	var args []any
	argIdx := 1

	addClause := func(clause string, vals ...any) {
		clauses = append(clauses, fmt.Sprintf(clause, argIdx))
		args = append(args, vals[0])
		argIdx++
		for i := 1; i < len(vals); i++ {
			args = append(args, vals[i])
			argIdx++
		}
	}

	if f.TenantID != "" {
		addClause("tenant_id = $%d", f.TenantID)
	}
	if f.SKU != "" {
		addClause("sku = $%d", f.SKU)
	}
	if !f.StartTime.IsZero() {
		addClause("event_ts >= $%d", f.StartTime)
	}
	if !f.EndTime.IsZero() {
		addClause("event_ts < $%d", f.EndTime)
	}
	if f.PageToken != "" {
		parts := splitPageToken(f.PageToken)
		if parts != nil {
			clause := fmt.Sprintf("(event_ts, record_id) > ($%d::timestamptz, $%d)", argIdx, argIdx+1)
			clauses = append(clauses, clause)
			args = append(args, parts[0], parts[1])
			argIdx += 2
		}
	}

	where := ""
	if len(clauses) > 0 {
		where = "WHERE " + clauses[0]
		for _, c := range clauses[1:] {
			where += " AND " + c
		}
	}

	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}

	query := fmt.Sprintf(`
		SELECT record_id, COALESCE(schema_ver, 'usage.v1'), tenant_id, COALESCE(key_id, ''),
			sku, COALESCE(geo, ''), profile, collector,
			prompt_tokens, completion_tokens, reasoning_tokens, cached_tokens,
			quantities, status, partial, COALESCE(stream, false), latency_ms,
			attributes, content_hash, event_ts, ingested_at
		FROM usage_events
		%s
		ORDER BY event_ts, record_id
		LIMIT $%d`, where, argIdx)
	args = append(args, limit+1)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	var events []UsageEvent
	for rows.Next() {
		var ev UsageEvent
		var quantitiesJSON, attributesJSON []byte
		var latencyMs *float64
		var stream bool

		if err := rows.Scan(
			&ev.RecordID, &ev.SchemaVer, &ev.TenantID, &ev.KeyID,
			&ev.SKU, &ev.Geo, &ev.Profile, &ev.Collector,
			&ev.PromptTokens, &ev.CompletionTokens, &ev.ReasoningTokens, &ev.CachedTokens,
			&quantitiesJSON, &ev.Status, &ev.Partial, &stream, &latencyMs,
			&attributesJSON, &ev.ContentHash, &ev.EventTS, &ev.IngestedAt,
		); err != nil {
			return nil, fmt.Errorf("scan event row: %w", err)
		}

		ev.Stream = &stream
		ev.LatencyMs = latencyMs
		if err := json.Unmarshal(quantitiesJSON, &ev.Quantities); err != nil {
			return nil, fmt.Errorf("unmarshal quantities: %w", err)
		}
		if err := json.Unmarshal(attributesJSON, &ev.Attributes); err != nil {
			return nil, fmt.Errorf("unmarshal attributes: %w", err)
		}
		events = append(events, ev)
	}

	result := &QueryResult{}
	if len(events) > int(limit) {
		last := events[limit-1]
		result.NextPageToken = fmt.Sprintf("%s|%s", last.EventTS.Format(time.RFC3339Nano), last.RecordID)
		result.Events = events[:limit]
	} else {
		result.Events = events
	}
	return result, nil
}

// QueryUnratedEvents returns usage events that have no corresponding row in
// the charges table (left anti-join on record_id). The batch size is capped
// to keep memory bounded; callers should loop until zero results.
func (s *Store) QueryUnratedEvents(ctx context.Context, limit int) ([]UsageEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ue.record_id, COALESCE(ue.schema_ver, 'usage.v1'), ue.tenant_id, COALESCE(ue.key_id, ''),
			ue.sku, COALESCE(ue.geo, ''), ue.profile, ue.collector,
			ue.prompt_tokens, ue.completion_tokens, ue.reasoning_tokens, ue.cached_tokens,
			ue.quantities, ue.status, ue.partial, COALESCE(ue.stream, false), ue.latency_ms,
			ue.attributes, ue.content_hash, ue.event_ts, ue.ingested_at
		FROM usage_events ue
		WHERE NOT EXISTS (SELECT 1 FROM charges c WHERE c.record_id = ue.record_id)
		ORDER BY ue.event_ts, ue.record_id
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("query unrated events: %w", err)
	}
	defer rows.Close()

	var events []UsageEvent
	for rows.Next() {
		var ev UsageEvent
		var quantitiesJSON, attributesJSON []byte
		var latencyMs *float64
		var stream bool

		if err := rows.Scan(
			&ev.RecordID, &ev.SchemaVer, &ev.TenantID, &ev.KeyID,
			&ev.SKU, &ev.Geo, &ev.Profile, &ev.Collector,
			&ev.PromptTokens, &ev.CompletionTokens, &ev.ReasoningTokens, &ev.CachedTokens,
			&quantitiesJSON, &ev.Status, &ev.Partial, &stream, &latencyMs,
			&attributesJSON, &ev.ContentHash, &ev.EventTS, &ev.IngestedAt,
		); err != nil {
			return nil, fmt.Errorf("scan unrated event row: %w", err)
		}

		ev.Stream = &stream
		ev.LatencyMs = latencyMs
		if err := json.Unmarshal(quantitiesJSON, &ev.Quantities); err != nil {
			return nil, fmt.Errorf("unmarshal quantities: %w", err)
		}
		if err := json.Unmarshal(attributesJSON, &ev.Attributes); err != nil {
			return nil, fmt.Errorf("unmarshal attributes: %w", err)
		}
		events = append(events, ev)
	}
	return events, nil
}

// InsertCharge writes a charge row to the charges table. The charges table has
// a UNIQUE constraint on record_id — duplicate inserts are silently ignored
// (ON CONFLICT DO NOTHING) since the rater is deterministic: re-rating the
// same event produces the same charge.
func (s *Store) InsertCharge(ctx context.Context, recordID, tenantID, priceBook string,
	priceBookRev int, amount, currency string, components json.RawMessage,
	warnings json.RawMessage, ratedAt time.Time) error {

	_, err := s.pool.Exec(ctx, `
		INSERT INTO charges (record_id, tenant_id, price_book, price_book_rev,
			amount, currency, components, warnings, rated_at)
		VALUES ($1, $2, $3, $4, $5::numeric, $6, $7, $8, $9)
		ON CONFLICT (record_id) DO NOTHING`,
		recordID, tenantID, priceBook, priceBookRev,
		amount, currency, components, warnings, ratedAt,
	)
	return err
}

// QueryTenantBalance computes a tenant's balance from the ledger tables
// (adjustments - charges). Returns the balance as a decimal string and the
// timestamp of the most recent charge or adjustment. If no rows exist for
// the tenant, returns "0" with a zero time.
func (s *Store) QueryTenantBalance(ctx context.Context, tenantID string) (string, time.Time, error) {
	var balance string
	var latestAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(a.total, 0) - COALESCE(c.total, 0) AS balance,
		       GREATEST(a.latest, c.latest) AS latest_at
		FROM (SELECT sum(amount) AS total, max(created_at) AS latest FROM adjustments WHERE tenant_id = $1) a,
		     (SELECT sum(amount) AS total, max(rated_at)   AS latest FROM charges     WHERE tenant_id = $1) c`,
		tenantID,
	).Scan(&balance, &latestAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("query tenant balance: %w", err)
	}
	if latestAt == nil {
		return "0", time.Time{}, nil
	}
	return balance, *latestAt, nil
}

// QueryMTDSpend returns the month-to-date charge total for a tenant as a
// decimal string. If no charges exist for the current month, returns "0".
func (s *Store) QueryMTDSpend(ctx context.Context, tenantID string) (string, error) {
	var spend string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(amount), 0)
		FROM charges
		WHERE tenant_id = $1
		  AND rated_at >= date_trunc('month', now())`,
		tenantID,
	).Scan(&spend)
	if err != nil {
		return "", fmt.Errorf("query mtd spend: %w", err)
	}
	return spend, nil
}

// UpdateWallet upserts the wallet table with the computed balance. The wallet
// is a materialized cache for fast reads — not a source of truth.
func (s *Store) UpdateWallet(ctx context.Context, tenantID, balance string, asOf time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO wallet (tenant_id, balance, as_of, version)
		VALUES ($1, $2::numeric, $3, 1)
		ON CONFLICT (tenant_id) DO UPDATE
		SET balance = $2::numeric, as_of = $3, version = wallet.version + 1`,
		tenantID, balance, asOf,
	)
	if err != nil {
		return fmt.Errorf("update wallet: %w", err)
	}
	return nil
}

// QueryBucketedUsage returns usage charges aggregated by time bucket and SKU.
// The bucket parameter must be one of "hour", "day", "month" — validated
// here before SQL interpolation. Aggregation is on the charges table (rated
// amounts), with quantities summed from usage_events via record_id join.
func (s *Store) QueryBucketedUsage(ctx context.Context, tenantID string, from, to time.Time, bucket, sku string) ([]UsageBucket, error) {
	switch bucket {
	case "hour", "day", "month":
	default:
		return nil, fmt.Errorf("invalid bucket %q: must be hour, day, or month", bucket)
	}

	args := []any{tenantID, from, to}
	skuClause := ""
	if sku != "" {
		skuClause = "AND ue.sku = $4"
		args = append(args, sku)
	}

	query := fmt.Sprintf(`
		WITH charge_agg AS (
		  SELECT date_trunc('%s', c.rated_at) AS bucket_start,
		         ue.sku,
		         sum(c.amount) AS charged,
		         count(*) AS events
		  FROM charges c
		  JOIN usage_events ue ON ue.record_id = c.record_id
		  WHERE c.tenant_id = $1 AND c.rated_at >= $2 AND c.rated_at < $3 %s
		  GROUP BY 1, 2
		),
		qty_agg AS (
		  SELECT date_trunc('%s', c.rated_at) AS bucket_start,
		         ue.sku,
		         kv.key,
		         sum(kv.value::numeric) AS val
		  FROM charges c
		  JOIN usage_events ue ON ue.record_id = c.record_id
		  CROSS JOIN LATERAL jsonb_each_text(ue.quantities) AS kv(key, value)
		  WHERE c.tenant_id = $1 AND c.rated_at >= $2 AND c.rated_at < $3 %s
		  GROUP BY 1, 2, 3
		)
		SELECT ca.bucket_start, ca.sku, ca.charged, ca.events,
		       COALESCE(
		         (SELECT jsonb_object_agg(q.key, q.val)
		          FROM qty_agg q
		          WHERE q.bucket_start = ca.bucket_start AND q.sku = ca.sku),
		         '{}'::jsonb
		       ) AS quantities
		FROM charge_agg ca
		ORDER BY ca.bucket_start, ca.sku`, bucket, skuClause, bucket, skuClause)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query bucketed usage: %w", err)
	}
	defer rows.Close()

	var buckets []UsageBucket
	for rows.Next() {
		var b UsageBucket
		var quantitiesJSON []byte
		if err := rows.Scan(&b.Start, &b.SKU, &b.Charged, &b.Events, &quantitiesJSON); err != nil {
			return nil, fmt.Errorf("scan usage bucket: %w", err)
		}
		b.Quantities = make(map[string]float64)
		if len(quantitiesJSON) > 0 {
			if err := json.Unmarshal(quantitiesJSON, &b.Quantities); err != nil {
				return nil, fmt.Errorf("unmarshal bucket quantities: %w", err)
			}
		}
		buckets = append(buckets, b)
	}
	return buckets, nil
}

// InsertAdjustment creates an adjustment record. If an idempotency_key
// collision occurs, the existing row is returned if content matches;
// otherwise ErrIdempotencyConflict is returned.
func (s *Store) InsertAdjustment(ctx context.Context, adj AdjustmentInput) (AdjustmentRow, error) {
	var row AdjustmentRow
	err := s.pool.QueryRow(ctx, `
		INSERT INTO adjustments (tenant_id, kind, amount, currency, reason, actor, idempotency_key)
		VALUES ($1, $2, $3::numeric, 'USD', $4, $5, $6)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
		RETURNING adjustment_id, tenant_id, amount, kind, reason, idempotency_key, created_at`,
		adj.TenantID, adj.Kind, adj.Amount, adj.Reason, adj.Actor, adj.IdempotencyKey,
	).Scan(&row.ID, &row.TenantID, &row.Amount, &row.Kind, &row.Reason, &row.IdempotencyKey, &row.CreatedAt)

	if err == pgx.ErrNoRows {
		// ON CONFLICT fired — check if content matches for idempotent replay
		err = s.pool.QueryRow(ctx, `
			SELECT adjustment_id, tenant_id, amount, kind, reason, idempotency_key, created_at
			FROM adjustments
			WHERE idempotency_key = $1`,
			adj.IdempotencyKey,
		).Scan(&row.ID, &row.TenantID, &row.Amount, &row.Kind, &row.Reason, &row.IdempotencyKey, &row.CreatedAt)
		if err != nil {
			return AdjustmentRow{}, fmt.Errorf("query existing adjustment: %w", err)
		}
		if row.TenantID != adj.TenantID || row.Kind != adj.Kind || row.Reason != adj.Reason {
			return AdjustmentRow{}, ErrIdempotencyConflict
		}
		existAmt, err := decimal.NewFromString(row.Amount)
		if err != nil {
			return AdjustmentRow{}, fmt.Errorf("parse existing amount: %w", err)
		}
		inputAmt, err := decimal.NewFromString(adj.Amount)
		if err != nil {
			return AdjustmentRow{}, fmt.Errorf("parse input amount: %w", err)
		}
		if !existAmt.Equal(inputAmt) {
			return AdjustmentRow{}, ErrIdempotencyConflict
		}
		return row, nil
	}
	if err != nil {
		return AdjustmentRow{}, fmt.Errorf("insert adjustment: %w", err)
	}
	return row, nil
}

// QueryAdjustmentTotal returns the sum of adjustment amounts for a tenant
// filtered by kind and time range. Returns "0" if no rows match.
func (s *Store) QueryAdjustmentTotal(ctx context.Context, tenantID, kind string, since time.Time) (string, error) {
	var total string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(amount), 0)
		FROM adjustments
		WHERE tenant_id = $1 AND kind = $2 AND created_at >= $3`,
		tenantID, kind, since,
	).Scan(&total)
	if err != nil {
		return "", fmt.Errorf("query adjustment total: %w", err)
	}
	return total, nil
}

// QueryAdjustmentCount returns the count of adjustments for a tenant
// filtered by kind and time range.
func (s *Store) QueryAdjustmentCount(ctx context.Context, tenantID, kind string, since time.Time) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM adjustments
		WHERE tenant_id = $1 AND kind = $2 AND created_at >= $3`,
		tenantID, kind, since,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("query adjustment count: %w", err)
	}
	return count, nil
}

// QueryPeriodSpend returns the total charges for a tenant since the given time.
func (s *Store) QueryPeriodSpend(ctx context.Context, tenantID string, since time.Time) (string, error) {
	var spend string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(amount), 0)
		FROM charges
		WHERE tenant_id = $1
		  AND rated_at >= $2`,
		tenantID, since,
	).Scan(&spend)
	if err != nil {
		return "", fmt.Errorf("query period spend: %w", err)
	}
	return spend, nil
}

type Key struct {
	KeyName    string
	TenantID   string
	Enabled    bool
	Source     string
	CreatedAt  time.Time
	DisabledAt *time.Time
}

func (s *Store) ListKeys(ctx context.Context, tenantID string) ([]Key, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key_name, tenant_id, enabled, source, created_at, disabled_at
         FROM api_keys WHERE tenant_id = $1 ORDER BY key_name`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list keys: %w", err)
	}
	defer rows.Close()
	var keys []Key
	for rows.Next() {
		var k Key
		if err := rows.Scan(&k.KeyName, &k.TenantID, &k.Enabled, &k.Source, &k.CreatedAt, &k.DisabledAt); err != nil {
			return nil, fmt.Errorf("scan key: %w", err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *Store) RegisterKey(ctx context.Context, tenantID, keyName, source string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO api_keys (tenant_id, key_name, source) VALUES ($1, $2, $3)
         ON CONFLICT (tenant_id, key_name) DO NOTHING`, tenantID, keyName, source)
	if err != nil {
		return fmt.Errorf("register key: %w", err)
	}
	return nil
}

func (s *Store) SetKeyEnabled(ctx context.Context, tenantID, keyName string, enabled bool) error {
	var disabledAt interface{}
	if !enabled {
		disabledAt = time.Now()
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET enabled = $1, disabled_at = $2 WHERE tenant_id = $3 AND key_name = $4`,
		enabled, disabledAt, tenantID, keyName)
	if err != nil {
		return fmt.Errorf("set key enabled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %s for tenant %s", ErrKeyNotFound, keyName, tenantID)
	}
	return nil
}

func (s *Store) DisableAllKeys(ctx context.Context, tenantID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET enabled = false, disabled_at = now() WHERE tenant_id = $1 AND enabled = true`,
		tenantID)
	if err != nil {
		return fmt.Errorf("disable all keys: %w", err)
	}
	return nil
}

func (s *Store) EnableAllKeys(ctx context.Context, tenantID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE api_keys SET enabled = true, disabled_at = NULL WHERE tenant_id = $1 AND enabled = false`,
		tenantID)
	if err != nil {
		return fmt.Errorf("enable all keys: %w", err)
	}
	return nil
}

// Pool exposes the underlying pgxpool for integration tests that need raw SQL access.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

func (s *Store) QueryTenantTokens(ctx context.Context, tenantID string, from, to time.Time) (int64, error) {
	var total int64
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(prompt_tokens), 0)
		FROM usage_events
		WHERE tenant_id = $1 AND event_ts >= $2 AND event_ts < $3`,
		tenantID, from, to,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("query tenant tokens: %w", err)
	}
	return total, nil
}

func (s *Store) InsertDriftReport(ctx context.Context, r DriftReport) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO drift_reports (tenant_id, window_start, window_end, ledger_tokens, prom_tokens,
			drift_pct, tolerance_pct, exceeded, signature)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7::numeric, $8, $9)`,
		r.TenantID, r.WindowStart, r.WindowEnd, r.LedgerTokens, r.PromTokens,
		r.DriftPct, r.TolerancePct, r.Exceeded, r.Signature,
	)
	if err != nil {
		return fmt.Errorf("insert drift report: %w", err)
	}
	return nil
}

func (s *Store) QueryDriftReports(ctx context.Context, tenantID string, limit int) ([]DriftReport, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, window_start, window_end, ledger_tokens, prom_tokens,
			drift_pct, tolerance_pct, exceeded, signature, created_at
		FROM drift_reports
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("query drift reports: %w", err)
	}
	defer rows.Close()

	var reports []DriftReport
	for rows.Next() {
		var r DriftReport
		if err := rows.Scan(&r.ID, &r.TenantID, &r.WindowStart, &r.WindowEnd,
			&r.LedgerTokens, &r.PromTokens, &r.DriftPct, &r.TolerancePct,
			&r.Exceeded, &r.Signature, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan drift report: %w", err)
		}
		reports = append(reports, r)
	}
	return reports, rows.Err()
}

func (s *Store) QueryLatestDriftReport(ctx context.Context, tenantID string) (*DriftReport, error) {
	var r DriftReport
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, window_start, window_end, ledger_tokens, prom_tokens,
			drift_pct, tolerance_pct, exceeded, signature, created_at
		FROM drift_reports
		WHERE tenant_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, tenantID,
	).Scan(&r.ID, &r.TenantID, &r.WindowStart, &r.WindowEnd,
		&r.LedgerTokens, &r.PromTokens, &r.DriftPct, &r.TolerancePct,
		&r.Exceeded, &r.Signature, &r.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query latest drift report: %w", err)
	}
	return &r, nil
}

// QueryExportData returns FOCUS 1.2-formatted rows for a tenant's usage within [from, to).
// Currency is resolved from the charges table; if the tenant has no charges yet, the CROSS JOIN
// produces zero rows and the caller gets an empty slice (not an error). This is intentional:
// a valid FOCUS export requires a currency, and usage_hourly rows without rated charges
// cannot produce one. Aggregates across key_id and geo — the export grain is (sku, quantity_type, hour).
func (s *Store) QueryExportData(ctx context.Context, tenantID string, from, to time.Time) ([]FOCUSRow, error) {
	const exportSQL = `
		WITH tenant_currency AS (
			SELECT DISTINCT currency FROM charges WHERE tenant_id = $1 LIMIT 1
		)
		SELECT uh.sku, uh.quantity_type, uh.hour,
		       SUM(uh.quantity) AS quantity, SUM(uh.amount) AS amount,
		       tc.currency
		FROM usage_hourly uh
		CROSS JOIN tenant_currency tc
		WHERE uh.tenant_id = $1 AND uh.hour >= $2 AND uh.hour < $3
		GROUP BY uh.sku, uh.quantity_type, uh.hour, tc.currency
		ORDER BY uh.hour, uh.sku, uh.quantity_type`

	rows, err := s.pool.Query(ctx, exportSQL, tenantID, from, to)
	if err != nil {
		return nil, fmt.Errorf("query export data: %w", err)
	}
	defer rows.Close()

	periodStart := from.UTC().Format(time.RFC3339)
	periodEnd := to.UTC().Format(time.RFC3339)

	var result []FOCUSRow
	for rows.Next() {
		var sku, quantityType, quantityStr, amountStr, currency string
		var hour time.Time
		if err := rows.Scan(&sku, &quantityType, &hour, &quantityStr, &amountStr, &currency); err != nil {
			return nil, fmt.Errorf("scan export row: %w", err)
		}

		amt, err := decimal.NewFromString(amountStr)
		if err != nil {
			return nil, fmt.Errorf("parse amount %q: %w", amountStr, err)
		}
		qty, err := decimal.NewFromString(quantityStr)
		if err != nil {
			return nil, fmt.Errorf("parse quantity %q: %w", quantityStr, err)
		}

		billedCost := amt.StringFixed(6)
		consumedQty := qty.StringFixed(0)

		result = append(result, FOCUSRow{
			BilledCost:         billedCost,
			BillingAccountId:   tenantID,
			BillingAccountName: tenantID,
			BillingCurrency:    currency,
			BillingPeriodStart: periodStart,
			BillingPeriodEnd:   periodEnd,
			ChargeCategory:     "Usage",
			ChargeClass:        "",
			ChargeDescription:  fmt.Sprintf("%s %s", sku, quantityType),
			ChargePeriodStart:  hour.UTC().Format(time.RFC3339),
			ChargePeriodEnd:    hour.Add(time.Hour).UTC().Format(time.RFC3339),
			ContractedCost:     billedCost,
			EffectiveCost:      billedCost,
			ConsumedQuantity:   consumedQty,
			ConsumedUnit:       FOCUSConsumedUnit,
			ListCost:           billedCost,
			PricingQuantity:    consumedQty,
			PricingUnit:        FOCUSConsumedUnit,
			Provider:           FOCUSProvider,
			Publisher:          FOCUSProvider,
			ResourceId:         sku,
			ResourceName:       sku,
			ServiceCategory:    "AI and Machine Learning",
			ServiceName:        "Models as a Service",
			SkuId:              sku,
			SkuMeter:           quantityType,
			SubAccountId:       tenantID,
			SubAccountName:     tenantID,
			ChargeFrequency:    FOCUSChargeFrequency,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate export rows: %w", err)
	}
	return result, nil
}

func splitPageToken(token string) []string {
	idx := strings.LastIndex(token, "|")
	if idx < 0 {
		return nil
	}
	return []string{token[:idx], token[idx+1:]}
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
