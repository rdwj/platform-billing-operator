package gpu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// EventSender sends usage.v1 envelopes to the ledger.
type EventSender interface {
	Send(ctx context.Context, envelope json.RawMessage) error
}

// HTTPSender posts usage.v1 events to the ledger-svc HTTP ingest endpoint.
type HTTPSender struct {
	client  *http.Client
	baseURL string
}

// NewHTTPSender creates an HTTPSender targeting the given ledger-svc base URL.
func NewHTTPSender(baseURL string) *HTTPSender {
	return &HTTPSender{
		client:  &http.Client{Timeout: 10 * time.Second},
		baseURL: baseURL,
	}
}

// ingestRequest matches the POST /v1/ingest body format.
// event_json is a STRING, not a nested JSON object.
type ingestRequest struct {
	EventJSON string `json:"event_json"`
}

type ingestResponse struct {
	Result  string `json:"result"`
	Message string `json:"message"`
}

// Send posts a usage.v1 envelope to the ledger-svc ingest endpoint.
func (s *HTTPSender) Send(ctx context.Context, envelope json.RawMessage) error {
	req := ingestRequest{EventJSON: string(envelope)}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal ingest request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/ingest", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("POST /v1/ingest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading ingest response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ingest returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result ingestResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return fmt.Errorf("parse ingest response: %w", err)
	}

	// "inserted" and "duplicate" are both success outcomes.
	switch result.Result {
	case "inserted", "duplicate":
		return nil
	default:
		return fmt.Errorf("ingest result: %s (%s)", result.Result, result.Message)
	}
}
