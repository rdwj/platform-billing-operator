package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	MaxTokens     int            `json:"max_tokens"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatResponse struct {
	Usage *usageInfo `json:"usage"`
}

type usageInfo struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type usageEvent struct {
	SchemaVersion string            `json:"schema_version"`
	RecordID      string            `json:"record_id"`
	OccurredAt    string            `json:"occurred_at"`
	TenantID      string            `json:"tenant_id"`
	Source        eventSource       `json:"source"`
	SKU           string            `json:"sku"`
	Geo           string            `json:"geo"`
	Quantities    map[string]int    `json:"quantities"`
	Status        string            `json:"status"`
	Partial       bool              `json:"partial"`
	Estimated     bool              `json:"estimated,omitempty"`
	Stream        bool              `json:"stream"`
	LatencyMs     float64           `json:"latency_ms"`
	Attributes    map[string]string `json:"attributes"`
}

type eventSource struct {
	Profile          string `json:"profile"`
	Collector        string `json:"collector"`
	CollectorVersion string `json:"collector_version"`
}

type ingestRequest struct {
	EventJSON string `json:"event_json"`
}

func newUUID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

func GatewayRequest(ctx context.Context, client *http.Client, cfg *config, token string, stream bool) (time.Duration, error) {
	reqBody := chatRequest{
		Model:     cfg.model,
		Messages:  []chatMessage{{Role: "user", Content: cfg.prompt}},
		MaxTokens: cfg.maxTokens,
		Stream:    stream,
	}
	if stream {
		reqBody.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("marshal request: %w", err)
	}

	targetURL := cfg.url + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return time.Since(start), fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return time.Since(start), fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}

	if stream {
		err = drainSSE(resp.Body)
	} else {
		var cr chatResponse
		err = json.NewDecoder(resp.Body).Decode(&cr)
	}
	elapsed := time.Since(start)
	if err != nil {
		return elapsed, fmt.Errorf("read response: %w", err)
	}
	return elapsed, nil
}

func drainSSE(r io.Reader) error {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		// drain all lines; we only care about latency
	}
	return scanner.Err()
}

func IngestRequest(ctx context.Context, client *http.Client, url string) (time.Duration, error) {
	promptTokens := 100
	completionTokens := 50
	totalTokens := promptTokens + completionTokens

	ev := usageEvent{
		SchemaVersion: "usage.v1",
		RecordID:      "loadgen-" + newUUID(),
		OccurredAt:    time.Now().UTC().Format(time.RFC3339),
		TenantID:      "loadgen-tenant",
		Source: eventSource{
			Profile:          "maas",
			Collector:        "loadgen",
			CollectorVersion: "0.1.0",
		},
		SKU: "test-model",
		Geo: "us-east-2",
		Quantities: map[string]int{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      totalTokens,
		},
		Status:     "ok",
		Partial:    false,
		Stream:     false,
		LatencyMs:  250.0,
		Attributes: map[string]string{},
	}

	eventJSON, err := json.Marshal(ev)
	if err != nil {
		return 0, fmt.Errorf("marshal event: %w", err)
	}

	wrapper := ingestRequest{EventJSON: string(eventJSON)}
	body, err := json.Marshal(wrapper)
	if err != nil {
		return 0, fmt.Errorf("marshal ingest request: %w", err)
	}

	targetURL := url + "/v1/ingest"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return time.Since(start), fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	elapsed := time.Since(start)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return elapsed, fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	return elapsed, nil
}
