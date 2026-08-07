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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type OpenMeterOption func(*OpenMeterBackend)

func WithHTTPClient(c *http.Client) OpenMeterOption {
	return func(b *OpenMeterBackend) { b.httpClient = c }
}

// OpenMeterBackend implements UsageSinkBackend for the OpenMeter platform.
type OpenMeterBackend struct {
	Endpoint   string
	APIKey     string
	httpClient *http.Client
}

var _ UsageSinkBackend = (*OpenMeterBackend)(nil)

// NewOpenMeterBackend constructs an OpenMeterBackend. An empty apiKey is
// valid for self-hosted OpenMeter instances without authentication.
func NewOpenMeterBackend(endpoint, apiKey string, opts ...OpenMeterOption) *OpenMeterBackend {
	b := &OpenMeterBackend{
		Endpoint: strings.TrimRight(endpoint, "/"),
		APIKey:   apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

type cloudEvent struct {
	SpecVersion string            `json:"specversion"`
	Type        string            `json:"type"`
	ID          string            `json:"id"`
	Source      string            `json:"source"`
	Subject     string            `json:"subject"`
	Time        string            `json:"time"`
	Data        map[string]string `json:"data"`
}

type meterCreateRequest struct {
	Slug          string            `json:"slug"`
	Description   string            `json:"description"`
	EventType     string            `json:"eventType"`
	Aggregation   string            `json:"aggregation"`
	ValueProperty string            `json:"valueProperty"`
	GroupBy       map[string]string `json:"groupBy"`
}

type subjectUpsert struct {
	Key         string            `json:"key"`
	DisplayName string            `json:"displayName"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type queryResponseRow struct {
	Value       json.Number       `json:"value"`
	WindowStart string            `json:"windowStart"`
	WindowEnd   string            `json:"windowEnd"`
	Subject     string            `json:"subject"`
	GroupBy     map[string]string `json:"groupBy"`
}

type queryResponse struct {
	Data []queryResponseRow `json:"data"`
}

func (b *OpenMeterBackend) doRequest(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, b.Endpoint+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if b.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.APIKey)
	}

	return b.httpClient.Do(req)
}

func (b *OpenMeterBackend) IngestEvents(ctx context.Context, events []UsageEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}

	batch := make([]cloudEvent, len(events))
	for i, ev := range events {
		data := make(map[string]string, len(ev.Metadata)+6)
		for k, v := range ev.Metadata {
			data[k] = v
		}
		// Canonical fields set after metadata so they cannot be overwritten.
		data["model"] = ev.Model
		data["prompt_tokens"] = ev.PromptTokens.String()
		data["completion_tokens"] = ev.CompletionTokens.String()
		data["reasoning_tokens"] = ev.ReasoningTokens.String()
		data["cached_tokens"] = ev.CachedTokens.String()
		data["request_id"] = ev.RequestID

		batch[i] = cloudEvent{
			SpecVersion: "1.0",
			Type:        ev.EventType,
			ID:          ev.RecordID,
			Source:      "platform-billing-operator",
			Subject:     ev.TenantID,
			Time:        ev.Timestamp.UTC().Format(time.RFC3339),
			Data:        data,
		}
	}

	buf, err := json.Marshal(batch)
	if err != nil {
		return 0, fmt.Errorf("IngestEvents: marshal batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.Endpoint+"/api/v1/events", bytes.NewReader(buf))
	if err != nil {
		return 0, fmt.Errorf("IngestEvents: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/cloudevents-batch+json")
	if b.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.APIKey)
	}

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("IngestEvents: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("IngestEvents: unexpected status %d: %s", resp.StatusCode, snippet)
	}

	return len(events), nil
}

func (b *OpenMeterBackend) HealthCheck(ctx context.Context) error {
	resp, err := b.doRequest(ctx, http.MethodGet, "/api/v1/meters", nil)
	if err != nil {
		return fmt.Errorf("HealthCheck: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("HealthCheck: unexpected status %d: %s", resp.StatusCode, snippet)
	}

	return nil
}

func (b *OpenMeterBackend) ConfigureMeters(ctx context.Context, meters []MeterDefinition) error {
	for _, m := range meters {
		resp, err := b.doRequest(ctx, http.MethodGet, "/api/v1/meters/"+url.PathEscape(m.Slug), nil)
		if err != nil {
			return fmt.Errorf("ConfigureMeters: check %q: %w", m.Slug, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			continue
		}

		if resp.StatusCode != http.StatusNotFound {
			return fmt.Errorf("ConfigureMeters: check %q: unexpected status %d", m.Slug, resp.StatusCode)
		}

		if err := b.createMeter(ctx, m); err != nil {
			return err
		}
	}

	return nil
}

func (b *OpenMeterBackend) createMeter(ctx context.Context, m MeterDefinition) error {
	resp, err := b.doRequest(ctx, http.MethodPost, "/api/v1/meters", meterCreateRequest{
		Slug:          m.Slug,
		Description:   m.Description,
		EventType:     m.EventType,
		Aggregation:   m.Aggregation,
		ValueProperty: m.ValueProperty,
		GroupBy:       m.GroupBy,
	})
	if err != nil {
		return fmt.Errorf("ConfigureMeters: create %q: %w", m.Slug, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ConfigureMeters: create %q: unexpected status %d: %s", m.Slug, resp.StatusCode, snippet)
	}

	return nil
}

func (b *OpenMeterBackend) SyncSubject(ctx context.Context, tenant TenantInfo) error {
	subjects := []subjectUpsert{{
		Key:         tenant.Key,
		DisplayName: tenant.DisplayName,
		Metadata:    tenant.Metadata,
	}}

	resp, err := b.doRequest(ctx, http.MethodPost, "/api/v1/subjects", subjects)
	if err != nil {
		return fmt.Errorf("SyncSubject: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("SyncSubject: unexpected status %d: %s", resp.StatusCode, snippet)
	}

	return nil
}

func (b *OpenMeterBackend) QueryUsage(ctx context.Context, query UsageQuery) ([]UsageRecord, error) {
	params := url.Values{}
	for _, s := range query.Subjects {
		params.Add("subject[]", s)
	}
	if !query.From.IsZero() {
		params.Set("from", query.From.UTC().Format(time.RFC3339))
	}
	if !query.To.IsZero() {
		params.Set("to", query.To.UTC().Format(time.RFC3339))
	}
	for _, g := range query.GroupBy {
		params.Add("groupBy[]", g)
	}

	path := "/api/v1/meters/" + url.PathEscape(query.MeterSlug) + "/query"
	if encoded := params.Encode(); encoded != "" {
		path += "?" + encoded
	}

	resp, err := b.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("QueryUsage: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("QueryUsage: unexpected status %d: %s", resp.StatusCode, snippet)
	}

	var qr queryResponse
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		return nil, fmt.Errorf("QueryUsage: decode response: %w", err)
	}

	records := make([]UsageRecord, 0, len(qr.Data))
	for _, row := range qr.Data {
		val, err := decimal.NewFromString(row.Value.String())
		if err != nil {
			return nil, fmt.Errorf("QueryUsage: parse value %q: %w", row.Value, err)
		}
		windowStart, err := time.Parse(time.RFC3339, row.WindowStart)
		if err != nil {
			return nil, fmt.Errorf("QueryUsage: parse windowStart %q: %w", row.WindowStart, err)
		}
		windowEnd, err := time.Parse(time.RFC3339, row.WindowEnd)
		if err != nil {
			return nil, fmt.Errorf("QueryUsage: parse windowEnd %q: %w", row.WindowEnd, err)
		}
		records = append(records, UsageRecord{
			Subject:     row.Subject,
			Value:       val,
			WindowStart: windowStart,
			WindowEnd:   windowEnd,
			GroupBy:     row.GroupBy,
		})
	}

	return records, nil
}
