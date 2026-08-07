/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package usagesink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

var testTime = time.Date(2026, 8, 6, 14, 30, 0, 0, time.UTC)

func sampleEvent() UsageEvent {
	return UsageEvent{
		RecordID:         "rec-001",
		TenantID:         "tenant-alpha",
		Timestamp:        testTime,
		EventType:        "inference",
		Model:            "gpt-4o",
		PromptTokens:     decimal.NewFromInt(1024),
		CompletionTokens: decimal.NewFromInt(512),
		ReasoningTokens:  decimal.NewFromInt(0),
		CachedTokens:     decimal.NewFromInt(256),
		RequestID:        "req-abc",
		Metadata:         map[string]string{"region": "us-east-1"},
	}
}

// cloudEventJSON mirrors the wire format for test assertions.
type cloudEventJSON struct {
	SpecVersion string            `json:"specversion"`
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	Source      string            `json:"source"`
	Subject     string            `json:"subject"`
	Time        string            `json:"time"`
	Data        map[string]string `json:"data"`
}

// ---------------------------------------------------------------------------
// IngestEvents
// ---------------------------------------------------------------------------

func TestIngestEvents_Success(t *testing.T) {
	var captured []cloudEventJSON
	var capturedPath string
	var capturedContentType string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedContentType = r.Header.Get("Content-Type")

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))

		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	ev := sampleEvent()

	n, err := backend.IngestEvents(context.Background(), []UsageEvent{ev})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	assert.Equal(t, "/api/v1/events", capturedPath)
	assert.Equal(t, "application/cloudevents-batch+json", capturedContentType)

	require.Len(t, captured, 1)
	ce := captured[0]
	assert.Equal(t, "1.0", ce.SpecVersion)
	assert.Equal(t, "platform-billing-operator", ce.Source)
	assert.Equal(t, "rec-001", ce.ID)
	assert.Equal(t, "tenant-alpha", ce.Subject)
	assert.Equal(t, "inference", ce.Type)
	assert.Equal(t, testTime.Format(time.RFC3339), ce.Time)

	// Token values must be strings, not numbers.
	assert.Equal(t, "1024", ce.Data["prompt_tokens"])
	assert.Equal(t, "512", ce.Data["completion_tokens"])
	assert.Equal(t, "0", ce.Data["reasoning_tokens"])
	assert.Equal(t, "256", ce.Data["cached_tokens"])

	// Metadata merged into data.
	assert.Equal(t, "us-east-1", ce.Data["region"])
	assert.Equal(t, "gpt-4o", ce.Data["model"])
	assert.Equal(t, "req-abc", ce.Data["request_id"])
}

func TestIngestEvents_MetadataCannotOverwriteCanonicalFields(t *testing.T) {
	var captured []cloudEventJSON

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	ev := sampleEvent()
	ev.Metadata = map[string]string{
		"prompt_tokens": "9999",
		"model":         "overwritten",
		"region":        "us-west-2",
	}

	_, err := backend.IngestEvents(context.Background(), []UsageEvent{ev})
	require.NoError(t, err)

	require.Len(t, captured, 1)
	assert.Equal(t, "1024", captured[0].Data["prompt_tokens"], "canonical field must not be overwritten by metadata")
	assert.Equal(t, "gpt-4o", captured[0].Data["model"], "canonical field must not be overwritten by metadata")
	assert.Equal(t, "us-west-2", captured[0].Data["region"], "non-conflicting metadata should be preserved")
}

func TestIngestEvents_MultipleBatch(t *testing.T) {
	var captured []cloudEventJSON

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &captured))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")

	ev1 := sampleEvent()
	ev2 := sampleEvent()
	ev2.RecordID = "rec-002"
	ev2.TenantID = "tenant-beta"

	n, err := backend.IngestEvents(context.Background(), []UsageEvent{ev1, ev2})
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	require.Len(t, captured, 2)
	assert.Equal(t, "rec-001", captured[0].ID)
	assert.Equal(t, "tenant-alpha", captured[0].Subject)
	assert.Equal(t, "rec-002", captured[1].ID)
	assert.Equal(t, "tenant-beta", captured[1].Subject)
}

func TestIngestEvents_EmptyBatch(t *testing.T) {
	var called atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")

	n, err := backend.IngestEvents(context.Background(), []UsageEvent{})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.False(t, called.Load(), "server should not have been called for empty batch")
}

func TestIngestEvents_DedupSemantics(t *testing.T) {
	var batches [][]cloudEventJSON

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var batch []cloudEventJSON
		require.NoError(t, json.Unmarshal(body, &batch))
		batches = append(batches, batch)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	ev := sampleEvent()

	_, err := backend.IngestEvents(context.Background(), []UsageEvent{ev})
	require.NoError(t, err)
	_, err = backend.IngestEvents(context.Background(), []UsageEvent{ev})
	require.NoError(t, err)

	require.Len(t, batches, 2)
	assert.Equal(t, batches[0][0].ID, batches[1][0].ID,
		"same record_id must produce same CloudEvents id for dedup")
}

func TestIngestEvents_AuthHeader(t *testing.T) {
	tests := []struct {
		name       string
		apiKey     string
		wantHeader string
	}{
		{
			name:       "with API key",
			apiKey:     "test-key-123",
			wantHeader: "Bearer test-key-123",
		},
		{
			name:       "without API key",
			apiKey:     "",
			wantHeader: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var capturedAuth string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			backend := NewOpenMeterBackend(srv.URL, tc.apiKey)
			_, err := backend.IngestEvents(context.Background(), []UsageEvent{sampleEvent()})
			require.NoError(t, err)

			assert.Equal(t, tc.wantHeader, capturedAuth)
		})
	}
}

func TestIngestEvents_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")

	n, err := backend.IngestEvents(context.Background(), []UsageEvent{sampleEvent()})
	require.Error(t, err)
	assert.Equal(t, 0, n)
	assert.Contains(t, err.Error(), "500")
}

func TestIngestEvents_ClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")

	n, err := backend.IngestEvents(context.Background(), []UsageEvent{sampleEvent()})
	require.Error(t, err)
	assert.Equal(t, 0, n)
	assert.Contains(t, err.Error(), "400")
}

// ---------------------------------------------------------------------------
// HealthCheck
// ---------------------------------------------------------------------------

func TestHealthCheck_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/meters", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	err := backend.HealthCheck(context.Background())
	assert.NoError(t, err)
}

func TestHealthCheck_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("service unavailable"))
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	err := backend.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
}

// ---------------------------------------------------------------------------
// ConfigureMeters
// ---------------------------------------------------------------------------

func TestConfigureMeters_Idempotent(t *testing.T) {
	var postCalled atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Meter already exists.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"slug":"prompt-tokens"}`))
			return
		}
		postCalled.Store(true)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	meters := []MeterDefinition{{
		Slug:          "prompt-tokens",
		Description:   "Total prompt tokens",
		EventType:     "inference",
		Aggregation:   "SUM",
		ValueProperty: "$.prompt_tokens",
		GroupBy:       map[string]string{"model": "$.model"},
	}}

	err := backend.ConfigureMeters(context.Background(), meters)
	require.NoError(t, err)
	assert.False(t, postCalled.Load(), "POST should not be called when meter already exists")
}

func TestConfigureMeters_CreatesNew(t *testing.T) {
	var capturedBody meterCreateRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			// Meter does not exist.
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPost:
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(body, &capturedBody))
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	meters := []MeterDefinition{{
		Slug:          "prompt-tokens",
		Description:   "Total prompt tokens",
		EventType:     "inference",
		Aggregation:   "SUM",
		ValueProperty: "$.prompt_tokens",
		GroupBy:       map[string]string{"model": "$.model"},
	}}

	err := backend.ConfigureMeters(context.Background(), meters)
	require.NoError(t, err)

	assert.Equal(t, "prompt-tokens", capturedBody.Slug)
	assert.Equal(t, "Total prompt tokens", capturedBody.Description)
	assert.Equal(t, "inference", capturedBody.EventType)
	assert.Equal(t, "SUM", capturedBody.Aggregation)
	assert.Equal(t, "$.prompt_tokens", capturedBody.ValueProperty)
	assert.Equal(t, map[string]string{"model": "$.model"}, capturedBody.GroupBy)
}

// ---------------------------------------------------------------------------
// SyncSubject
// ---------------------------------------------------------------------------

func TestSyncSubject(t *testing.T) {
	var capturedBody []subjectUpsert

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/subjects", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &capturedBody))

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	tenant := TenantInfo{
		Key:         "tenant-alpha",
		DisplayName: "Tenant Alpha",
		Metadata:    map[string]string{"tier": "gold"},
	}

	err := backend.SyncSubject(context.Background(), tenant)
	require.NoError(t, err)

	require.Len(t, capturedBody, 1)
	assert.Equal(t, "tenant-alpha", capturedBody[0].Key)
	assert.Equal(t, "Tenant Alpha", capturedBody[0].DisplayName)
	assert.Equal(t, map[string]string{"tier": "gold"}, capturedBody[0].Metadata)
}

// ---------------------------------------------------------------------------
// QueryUsage
// ---------------------------------------------------------------------------

func TestQueryUsage(t *testing.T) {
	windowStart := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 6, 23, 59, 59, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Contains(t, r.URL.Path, "/api/v1/meters/prompt-tokens/query")

		q := r.URL.Query()
		assert.Equal(t, []string{"tenant-alpha"}, q["subject[]"])
		assert.Equal(t, []string{"model"}, q["groupBy[]"])
		assert.NotEmpty(t, q.Get("from"))
		assert.NotEmpty(t, q.Get("to"))

		resp := map[string]any{
			"data": []map[string]any{
				{
					"value":       1024,
					"windowStart": windowStart.Format(time.RFC3339),
					"windowEnd":   windowEnd.Format(time.RFC3339),
					"subject":     "tenant-alpha",
					"groupBy":     map[string]string{"model": "gpt-4o"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	query := UsageQuery{
		MeterSlug: "prompt-tokens",
		Subjects:  []string{"tenant-alpha"},
		From:      windowStart,
		To:        windowEnd,
		GroupBy:   []string{"model"},
	}

	records, err := backend.QueryUsage(context.Background(), query)
	require.NoError(t, err)
	require.Len(t, records, 1)

	assert.Equal(t, "tenant-alpha", records[0].Subject)
	assert.True(t, decimal.NewFromInt(1024).Equal(records[0].Value),
		"value = %s, want 1024", records[0].Value)
	assert.Equal(t, windowStart, records[0].WindowStart)
	assert.Equal(t, windowEnd, records[0].WindowEnd)
	assert.Equal(t, "gpt-4o", records[0].GroupBy["model"])
}

func TestQueryUsage_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer srv.Close()

	backend := NewOpenMeterBackend(srv.URL, "")
	query := UsageQuery{
		MeterSlug: "prompt-tokens",
		Subjects:  []string{"tenant-alpha"},
		From:      testTime,
		To:        testTime.Add(24 * time.Hour),
	}

	records, err := backend.QueryUsage(context.Background(), query)
	require.NoError(t, err)
	assert.Empty(t, records, "expected empty slice for no data")
	assert.NotNil(t, records, "expected non-nil empty slice")
}
