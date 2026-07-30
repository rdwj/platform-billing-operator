package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(newMux())
	t.Cleanup(srv.Close)
	return srv
}

func post(t *testing.T, url, body string, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// sseEvents reads an SSE body into its data payloads. Returns the payloads and
// whether the stream terminated cleanly with [DONE] (vs. an abrupt EOF/reset).
func sseEvents(t *testing.T, body io.Reader) (events []string, done bool, readErr error) {
	t.Helper()
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			return events, true, nil
		}
		events = append(events, payload)
	}
	return events, false, scanner.Err()
}

func TestNonStreamingUsage(t *testing.T) {
	srv := startServer(t)
	resp := post(t, srv.URL+"/v1/chat/completions",
		`{"model":"llama-3-70b","messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{
			"X-Fake-Prompt-Tokens":     "412",
			"X-Fake-Completion-Tokens": "1288",
			"X-Fake-Reasoning-Tokens":  "256",
			"X-Fake-Cached-Tokens":     "128",
		})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Usage usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Usage.PromptTokens != 412 || out.Usage.CompletionTokens != 1288 || out.Usage.TotalTokens != 1700 {
		t.Fatalf("usage = %+v", out.Usage)
	}
	if out.Usage.CompletionTokensDetails == nil || out.Usage.CompletionTokensDetails.ReasoningTokens != 256 {
		t.Fatalf("reasoning detail = %+v", out.Usage.CompletionTokensDetails)
	}
	if out.Usage.PromptTokensDetails == nil || out.Usage.PromptTokensDetails.CachedTokens != 128 {
		t.Fatalf("cached detail = %+v", out.Usage.PromptTokensDetails)
	}
}

func TestStreamingWithIncludeUsage(t *testing.T) {
	srv := startServer(t)
	resp := post(t, srv.URL+"/v1/chat/completions",
		`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"X-Fake-Chunks": "3", "X-Fake-Prompt-Tokens": "10", "X-Fake-Completion-Tokens": "20"})
	defer func() { _ = resp.Body.Close() }()
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	events, done, err := sseEvents(t, resp.Body)
	if err != nil || !done {
		t.Fatalf("stream did not finish cleanly: done=%v err=%v", done, err)
	}
	// role + 3 content + finish + usage = 6
	if len(events) != 6 {
		t.Fatalf("got %d events, want 6: %v", len(events), events)
	}
	// The FINAL data chunk before [DONE] must carry usage with empty choices.
	var last struct {
		Choices []any  `json:"choices"`
		Usage   *usage `json:"usage"`
	}
	if err := json.Unmarshal([]byte(events[len(events)-1]), &last); err != nil {
		t.Fatal(err)
	}
	if last.Usage == nil || last.Usage.TotalTokens != 30 || len(last.Choices) != 0 {
		t.Fatalf("final chunk = %+v", last)
	}
	// No earlier chunk may carry usage.
	for _, e := range events[:len(events)-1] {
		if strings.Contains(e, `"usage"`) {
			t.Fatalf("non-final chunk carries usage: %s", e)
		}
	}
}

func TestStreamingWithoutIncludeUsageHasNoUsage(t *testing.T) {
	srv := startServer(t)
	resp := post(t, srv.URL+"/v1/chat/completions",
		`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`, nil)
	defer func() { _ = resp.Body.Close() }()
	events, done, err := sseEvents(t, resp.Body)
	if err != nil || !done {
		t.Fatalf("stream did not finish cleanly: done=%v err=%v", done, err)
	}
	for _, e := range events {
		if strings.Contains(e, `"usage"`) {
			t.Fatalf("usage emitted without include_usage: %s", e)
		}
	}
}

func TestDisconnectMidStream(t *testing.T) {
	srv := startServer(t)
	resp := post(t, srv.URL+"/v1/chat/completions",
		`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"X-Fake-Chunks": "6", "X-Fake-Fault": "disconnect-mid-stream", "X-Fake-Fault-After": "2"})
	defer func() { _ = resp.Body.Close() }()
	events, done, _ := sseEvents(t, resp.Body)
	if done {
		t.Fatal("stream terminated with [DONE]; expected abrupt reset")
	}
	// role + 2 content chunks were sent before the reset.
	if len(events) != 3 {
		t.Fatalf("got %d events before reset, want 3: %v", len(events), events)
	}
	for _, e := range events {
		if strings.Contains(e, `"usage"`) {
			t.Fatalf("usage must not appear before reset: %s", e)
		}
	}
}

func TestOmitUsageFault(t *testing.T) {
	srv := startServer(t)
	resp := post(t, srv.URL+"/v1/chat/completions",
		`{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`,
		map[string]string{"X-Fake-Fault": "omit-usage"})
	defer func() { _ = resp.Body.Close() }()
	events, done, err := sseEvents(t, resp.Body)
	if err != nil || !done {
		t.Fatalf("stream did not finish cleanly: done=%v err=%v", done, err)
	}
	for _, e := range events {
		if strings.Contains(e, `"usage"`) {
			t.Fatalf("usage emitted despite omit-usage: %s", e)
		}
	}
}

func TestMalformedNonStreaming(t *testing.T) {
	srv := startServer(t)
	resp := post(t, srv.URL+"/v1/chat/completions",
		`{"model":"m","messages":[]}`, map[string]string{"X-Fake-Fault": "malformed-json"})
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var v any
	if err := json.Unmarshal(body, &v); err == nil {
		t.Fatalf("body unexpectedly valid JSON: %s", body)
	}
}

func TestHTTP500Fault(t *testing.T) {
	srv := startServer(t)
	resp := post(t, srv.URL+"/v1/chat/completions",
		`{"model":"m","messages":[]}`, map[string]string{"X-Fake-Fault": "http-500"})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}
