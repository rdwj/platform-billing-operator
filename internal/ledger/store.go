package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

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
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ledger_meta (key, value, updated_at)
		VALUES ('watermark_ts', $1::text, now())
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, updated_at = now()
		WHERE ledger_meta.value::timestamptz < $1`,
		ts,
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

// Pool exposes the underlying pgxpool for integration tests that need raw SQL access.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
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
