//go:build integration

package httpapi_test

import (
	"context"
	"encoding/csv"
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
	handler := httpapi.NewServer(testIngester, testStore, nil, "default")
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

func TestPricingNoK8sReader(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/v1/pricing?tenant=any-tenant")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
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

func TestUnifiedWalletAcrossProfiles(t *testing.T) {
	ctx := context.Background()
	tenantID := "ten-unified-wallet-test"

	// 1. Seed the wallet with a 100 USD credit.
	adjBody := `{"tenant":"ten-unified-wallet-test","amount":"100.000000","kind":"manual_credit","reason":"unified wallet test seed","idempotency_key":"unified-wallet-seed-001"}`
	resp, err := http.Post(testServer.URL+"/v1/adjustments", "application/json", strings.NewReader(adjBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode,
		"seed adjustment should return 201")

	// 2. Ingest a MaaS event.
	maasEv := makeEvent(t, map[string]any{
		"schema_version": "usage.v1",
		"record_id":      "req-unified-maas-001",
		"occurred_at":    "2026-08-01T10:00:00Z",
		"tenant_id":      tenantID,
		"source":         map[string]any{"profile": "maas", "collector": "test"},
		"sku":            "llama-3-70b",
		"quantities":     map[string]any{"prompt_tokens": 1000, "completion_tokens": 500},
		"status":         "ok",
	})
	maasResult, err := testIngester.IngestEvent(ctx, maasEv)
	require.NoError(t, err)
	require.Equal(t, ledger.Inserted, maasResult)

	// 3. Insert a MaaS charge: prompt 1000 @ 2.50/M + completion 500 @ 10.00/M = 0.007500.
	_, err = testPool.Exec(ctx, `
		INSERT INTO charges (record_id, tenant_id, price_book, price_book_rev,
			amount, currency, components, rated_at)
		VALUES ($1, $2, 'pb-standard', 3, 0.007500, 'USD', '[]', $3)
		ON CONFLICT (record_id) DO NOTHING`,
		"req-unified-maas-001", tenantID, "2026-08-01T10:00:00Z")
	require.NoError(t, err)

	// 4. Ingest a GPUaaS event.
	gpuEv := makeEvent(t, map[string]any{
		"schema_version": "usage.v1",
		"record_id":      "wl-unified-gpu-001-interval-0001",
		"occurred_at":    "2026-08-01T11:00:00Z",
		"tenant_id":      tenantID,
		"source":         map[string]any{"profile": "gpuaas", "collector": "kueue-collector"},
		"sku":            "h200-mig-3g.40gb",
		"geo":            "us-east-1",
		"quantities":     map[string]any{"gpu_seconds": 3600, "slice_fraction": 0.375},
		"status":         "ok",
		"interval":       map[string]any{"start": "2026-08-01T10:00:00Z", "end": "2026-08-01T11:00:00Z", "seq": 1},
		"attributes":     map[string]any{"queue": "team-ml-batch", "workload": "test-unified"},
	})
	gpuResult, err := testIngester.IngestEvent(ctx, gpuEv)
	require.NoError(t, err)
	require.Equal(t, ledger.Inserted, gpuResult)

	// 5. Insert a GPUaaS charge: 3600 gpu_seconds @ 0.001250 = 4.500000.
	_, err = testPool.Exec(ctx, `
		INSERT INTO charges (record_id, tenant_id, price_book, price_book_rev,
			amount, currency, components, rated_at)
		VALUES ($1, $2, 'pb-standard', 3, 4.500000, 'USD', '[]', $3)
		ON CONFLICT (record_id) DO NOTHING`,
		"wl-unified-gpu-001-interval-0001", tenantID, "2026-08-01T11:00:00Z")
	require.NoError(t, err)

	// 6. Verify unified balance: 100.000000 - 0.007500 - 4.500000 = 95.492500.
	balResp, err := http.Get(testServer.URL + "/v1/balance?tenant=" + tenantID)
	require.NoError(t, err)
	defer balResp.Body.Close()
	require.Equal(t, http.StatusOK, balResp.StatusCode)

	var balBody map[string]any
	require.NoError(t, json.NewDecoder(balResp.Body).Decode(&balBody))

	gotBal, err := decimal.NewFromString(balBody["balance"].(string))
	require.NoError(t, err)
	wantBal := decimal.RequireFromString("95.492500")
	require.True(t, wantBal.Equal(gotBal),
		"unified balance: want 95.492500, got %s", gotBal)

	// 7. Verify SKU-filtered usage returns only the GPUaaS SKU.
	skuResp, err := http.Get(testServer.URL + "/v1/usage?tenant=" + tenantID +
		"&from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z&bucket=day&sku=h200-mig-3g.40gb")
	require.NoError(t, err)
	defer skuResp.Body.Close()
	require.Equal(t, http.StatusOK, skuResp.StatusCode)

	var skuBody map[string]any
	require.NoError(t, json.NewDecoder(skuResp.Body).Decode(&skuBody))
	skuPoints := skuBody["points"].([]any)
	require.Len(t, skuPoints, 1, "SKU filter should return exactly 1 point for h200-mig-3g.40gb")

	skuPt := skuPoints[0].(map[string]any)
	require.Equal(t, "h200-mig-3g.40gb", skuPt["sku"])
	require.Equal(t, float64(1), skuPt["events"])

	// 8. Verify unfiltered usage returns both SKUs.
	allResp, err := http.Get(testServer.URL + "/v1/usage?tenant=" + tenantID +
		"&from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z&bucket=day")
	require.NoError(t, err)
	defer allResp.Body.Close()
	require.Equal(t, http.StatusOK, allResp.StatusCode)

	var allBody map[string]any
	require.NoError(t, json.NewDecoder(allResp.Body).Decode(&allBody))
	allPoints := allBody["points"].([]any)
	require.Len(t, allPoints, 2, "unfiltered usage should return 2 points (one per SKU)")
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

// ---------------------------------------------------------------------------
// FOCUS 1.2 export endpoint tests
// ---------------------------------------------------------------------------

func TestExportMissingParams(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"missing tenant", "/v1/export?from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z"},
		{"missing from", "/v1/export?tenant=t&to=2026-12-31T00:00:00Z"},
		{"missing to", "/v1/export?tenant=t&from=2026-01-01T00:00:00Z"},
		{"invalid from", "/v1/export?tenant=t&from=not-a-date&to=2026-12-31T00:00:00Z"},
		{"invalid to", "/v1/export?tenant=t&from=2026-01-01T00:00:00Z&to=not-a-date"},
		{"from after to", "/v1/export?tenant=t&from=2026-12-31T00:00:00Z&to=2026-01-01T00:00:00Z"},
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

func TestExportEmptyTenant(t *testing.T) {
	resp, err := http.Get(testServer.URL + "/v1/export?tenant=nonexistent-export-tenant&from=2026-01-01T00:00:00Z&to=2026-12-31T00:00:00Z")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Rows []map[string]any `json:"rows"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Empty(t, body.Rows)
}

func TestExportJSONWithData(t *testing.T) {
	ctx := context.Background()
	tenantID := "ten-export-test"

	// Insert a charge row (needed for currency resolution via tenant_currency CTE).
	_, err := testPool.Exec(ctx, `
		INSERT INTO charges (record_id, tenant_id, price_book, price_book_rev,
			amount, currency, components, rated_at)
		VALUES ('req-export-001', $1, 'pb-standard', 1, '0.010000', 'USD', '[]', '2026-08-01T10:00:00Z')
		ON CONFLICT (record_id) DO NOTHING`, tenantID)
	require.NoError(t, err)

	// Insert two usage_hourly rows: different quantity types, same hour.
	_, err = testPool.Exec(ctx, `
		INSERT INTO usage_hourly (tenant_id, key_id, sku, geo, quantity_type, hour, quantity, amount)
		VALUES
			($1, '', 'gpt-4o', '', 'prompt_tokens',     '2026-08-01T10:00:00Z', 500, '0.001250'),
			($1, '', 'gpt-4o', '', 'completion_tokens',  '2026-08-01T10:00:00Z', 200, '0.002000')
		ON CONFLICT DO NOTHING`, tenantID)
	require.NoError(t, err)

	resp, err := http.Get(testServer.URL + "/v1/export?tenant=" + tenantID +
		"&from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Rows []map[string]any `json:"rows"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Len(t, body.Rows, 2, "expect 2 rows (one per quantity_type)")

	// Rows are ordered by hour, sku, quantity_type — so completion_tokens < prompt_tokens.
	ct := body.Rows[0]
	require.Equal(t, "0.002000", ct["BilledCost"])
	require.Equal(t, tenantID, ct["BillingAccountId"])
	require.Equal(t, "USD", ct["BillingCurrency"])
	require.Equal(t, "Usage", ct["ChargeCategory"])
	require.Equal(t, "200", ct["ConsumedQuantity"])
	require.Equal(t, "Tokens", ct["ConsumedUnit"])
	require.Equal(t, "gpt-4o", ct["SkuId"])
	require.Equal(t, "completion_tokens", ct["SkuMeter"])
	require.Equal(t, "AI and Machine Learning", ct["ServiceCategory"])
	require.Equal(t, "Usage-Based", ct["ChargeFrequency"])

	// Verify money-format pattern for all cost columns.
	moneyPattern := `^-?\d{1,12}\.\d{6}$`
	for _, col := range []string{"BilledCost", "ContractedCost", "EffectiveCost", "ListCost"} {
		require.Regexp(t, moneyPattern, ct[col].(string), "%s must match Money pattern", col)
	}
}

func TestExportCSVMatchesJSON(t *testing.T) {
	tenantID := "ten-export-test" // reuses data seeded by TestExportJSONWithData

	// Fetch JSON for comparison baseline.
	jsonResp, err := http.Get(testServer.URL + "/v1/export?tenant=" + tenantID +
		"&from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z")
	require.NoError(t, err)
	defer jsonResp.Body.Close()
	require.Equal(t, http.StatusOK, jsonResp.StatusCode)

	var jsonBody struct {
		Rows []map[string]any `json:"rows"`
	}
	require.NoError(t, json.NewDecoder(jsonResp.Body).Decode(&jsonBody))
	require.NotEmpty(t, jsonBody.Rows, "JSON baseline must have data (seeded by TestExportJSONWithData)")

	// Fetch CSV.
	csvResp, err := http.Get(testServer.URL + "/v1/export?tenant=" + tenantID +
		"&from=2026-08-01T00:00:00Z&to=2026-08-02T00:00:00Z&format=csv")
	require.NoError(t, err)
	defer csvResp.Body.Close()
	require.Equal(t, http.StatusOK, csvResp.StatusCode)
	require.Contains(t, csvResp.Header.Get("Content-Type"), "text/csv")

	csvBody, err := io.ReadAll(csvResp.Body)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(string(csvBody)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	require.True(t, len(records) >= 3, "CSV should have header + 2 data rows, got %d lines", len(records))

	// Header row must match FOCUSCSVHeader.
	expectedHeader := ledger.FOCUSCSVHeader()
	require.Equal(t, expectedHeader, records[0], "CSV header must match ledger.FOCUSCSVHeader()")

	// Build a column-index map for CSV lookup.
	colIdx := make(map[string]int, len(expectedHeader))
	for i, h := range expectedHeader {
		colIdx[h] = i
	}

	// Verify first CSV data row matches corresponding JSON row.
	csvRow := records[1]
	jsonRow := jsonBody.Rows[0]
	for _, col := range []string{"BilledCost", "SkuId", "SkuMeter", "BillingCurrency", "ChargeCategory"} {
		idx, ok := colIdx[col]
		require.True(t, ok, "column %s must exist in header", col)
		require.Equal(t, jsonRow[col].(string), csvRow[idx],
			"CSV %s should match JSON value", col)
	}

	// Verify correct number of data rows.
	require.Equal(t, len(jsonBody.Rows), len(records)-1,
		"CSV data rows should match JSON row count")
}
