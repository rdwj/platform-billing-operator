//go:build integration

package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rdwj/platform-billing-operator/internal/ledger"
	"github.com/rdwj/platform-billing-operator/internal/ledger/httpapi"
)

var (
	testStore    *ledger.Store
	testIngester *ledger.Ingester
	testPool     *pgxpool.Pool
	testServer   *httptest.Server
)

func TestMain(m *testing.M) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx, "postgres:16",
		postgres.WithDatabase("billing_httpapi_test"),
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

	if err := testStore.RunMigrations(findMigrationsDir()); err != nil {
		fmt.Fprintf(os.Stderr, "run migrations: %v\n", err)
		os.Exit(1)
	}

	testIngester, err = ledger.NewIngester(testStore)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create ingester: %v\n", err)
		os.Exit(1)
	}

	testPool = testStore.Pool()
	handler := httpapi.NewServer(testIngester, testStore)
	testServer = httptest.NewServer(handler)

	code := m.Run()

	testServer.Close()
	testStore.Close()
	_ = pgContainer.Terminate(ctx)
	os.Exit(code)
}

func TestBalanceEmptyTenant(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/v1/balance?tenant=nonexistent-tenant")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var b map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&b))
	require.Equal(t, "nonexistent-tenant", b["tenant"])
	require.Equal(t, "0.000000", b["balance"])
	require.Equal(t, "USD", b["currency"])
	require.Equal(t, "Active", b["state"])
	require.NotEmpty(t, b["as_of"])
}

func TestUsageEmptyTenant(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/v1/usage?tenant=nonexistent-tenant&from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var s map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))
	require.Equal(t, "nonexistent-tenant", s["tenant"])
	require.Equal(t, "day", s["bucket"])
	points, ok := s["points"].([]any)
	require.True(t, ok)
	require.Empty(t, points)
}

func TestBucketedUsageAggregation(t *testing.T) {
	ctx := context.Background()
	tenantID := "ten-usage-agg-test"
	sku := "gpt-4o"

	for i := 0; i < 3; i++ {
		recordID := fmt.Sprintf("req-usage-agg-%d", i)
		eventTS := time.Date(2026, 8, 1, 10, i, 0, 0, time.UTC)

		ev := makeEvent(t, map[string]any{
			"schema_version": "usage.v1",
			"record_id":      recordID,
			"occurred_at":    eventTS.Format(time.RFC3339),
			"tenant_id":      tenantID,
			"source":         map[string]any{"profile": "maas", "collector": "test"},
			"sku":            sku,
			"quantities":     map[string]any{"prompt_tokens": 100, "completion_tokens": 200},
			"status":         "ok",
		})
		result, err := testIngester.IngestEvent(ctx, ev)
		require.NoError(t, err)
		require.Equal(t, ledger.Inserted, result)

		_, err = testPool.Exec(ctx, `
			INSERT INTO charges (record_id, tenant_id, price_book, price_book_rev,
				amount, currency, components, rated_at)
			VALUES ($1, $2, 'pb-standard', 1, 0.010000, 'USD', '[]', $3)
			ON CONFLICT (record_id) DO NOTHING`,
			recordID, tenantID, eventTS)
		require.NoError(t, err)
	}

	resp, err := http.Get(testServer.URL + "/v1/usage?tenant=" + tenantID +
		"&from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z&bucket=day")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var s map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&s))

	points := s["points"].([]any)
	require.Len(t, points, 1, "3 events on same day should aggregate to 1 bucket")

	pt := points[0].(map[string]any)
	require.Equal(t, sku, pt["sku"])
	require.Equal(t, float64(3), pt["events"])

	charged, err := decimal.NewFromString(pt["charged"].(string))
	require.NoError(t, err)
	expected := decimal.RequireFromString("0.030000")
	require.True(t, expected.Equal(charged), "want 0.030000, got %s", charged)

	moneyPattern := `^-?\d{1,12}\.\d{6}$`
	require.Regexp(t, moneyPattern, pt["charged"].(string), "charged must match Money pattern")
}

func TestAdjustmentCreateAndIdempotency(t *testing.T) {
	body := `{"tenant":"ten-adj-test","amount":"50.000000","kind":"manual_credit","reason":"test credit","idempotency_key":"adj-test-key-001"}`

	// First request: 201.
	resp, err := http.Post(testServer.URL+"/v1/adjustments", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var adj map[string]any
	respBody, _ := io.ReadAll(resp.Body)
	require.NoError(t, json.Unmarshal(respBody, &adj))
	require.NotEmpty(t, adj["id"])
	require.Equal(t, "ten-adj-test", adj["tenant"])
	require.Equal(t, "manual_credit", adj["kind"])
	require.NotEmpty(t, adj["created_at"])

	amt, err := decimal.NewFromString(adj["amount"].(string))
	require.NoError(t, err)
	require.True(t, decimal.RequireFromString("50.000000").Equal(amt))

	moneyPattern := `^-?\d{1,12}\.\d{6}$`
	require.Regexp(t, moneyPattern, adj["amount"].(string))

	// Idempotent replay with same content: 201 with same ID.
	resp2, err := http.Post(testServer.URL+"/v1/adjustments", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusCreated, resp2.StatusCode)

	var adj2 map[string]any
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&adj2))
	require.Equal(t, adj["id"], adj2["id"], "idempotent replay should return same adjustment")

	// Replay with different content: 409.
	conflictBody := `{"tenant":"ten-adj-test","amount":"99.000000","kind":"manual_debit","reason":"different","idempotency_key":"adj-test-key-001"}`
	resp3, err := http.Post(testServer.URL+"/v1/adjustments", "application/json", strings.NewReader(conflictBody))
	require.NoError(t, err)
	defer resp3.Body.Close()
	require.Equal(t, http.StatusConflict, resp3.StatusCode)
}

func TestAdjustmentValidation(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"missing fields", `{}`, http.StatusBadRequest},
		{"short idempotency key", `{"tenant":"t","amount":"1.000000","kind":"manual_credit","reason":"r","idempotency_key":"short"}`, http.StatusBadRequest},
		{"invalid kind", `{"tenant":"t","amount":"1.000000","kind":"bogus","reason":"r","idempotency_key":"longenough1"}`, http.StatusBadRequest},
		{"invalid amount", `{"tenant":"t","amount":"notanumber","kind":"manual_credit","reason":"r","idempotency_key":"longenough2"}`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(testServer.URL+"/v1/adjustments", "application/json", strings.NewReader(tt.body))
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, tt.want, resp.StatusCode)
		})
	}
}

func TestBalanceReflectsAdjustmentsAndCharges(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/v1/balance?tenant=ten-adj-test")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var b map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&b))

	bal, err := decimal.NewFromString(b["balance"].(string))
	require.NoError(t, err)
	require.True(t, bal.Equal(decimal.RequireFromString("50.000000")),
		"balance should be 50.000000 (credit, no charges yet), got %s", bal)

	moneyPattern := `^-?\d{1,12}\.\d{6}$`
	require.Regexp(t, moneyPattern, b["balance"].(string))
}

func TestPricingNotImplemented(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/v1/pricing?tenant=any-tenant")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotImplemented, resp.StatusCode)
}

func TestUsageMissingParams(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"missing tenant", "/v1/usage?from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z"},
		{"missing from", "/v1/usage?tenant=t&to=2026-12-31T00:00:00Z"},
		{"missing to", "/v1/usage?tenant=t&from=2026-01-01T00:00:00Z"},
		{"invalid bucket", "/v1/usage?tenant=t&from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z&bucket=week"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(testServer.URL + tt.url)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestBalanceMissingTenant(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/v1/balance")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
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

func makeEvent(t *testing.T, fields map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(fields)
	require.NoError(t, err)
	return json.RawMessage(data)
}
