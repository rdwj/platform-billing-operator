// Package main implements fake-openai: the OpenAI-compatible test upstream for
// platform-billing-operator e2e tests (project-plan/milestones.md M1 calls this
// "the single most important test artifact in the entire project").
//
// It mimics vLLM's OpenAI-compatible server semantics precisely where billing
// capture cares:
//
//   - Non-streaming chat completions return a `usage` object, including
//     `completion_tokens_details.reasoning_tokens` and
//     `prompt_tokens_details.cached_tokens` when configured.
//   - Streaming responses emit SSE chunks and include a usage-bearing FINAL
//     chunk (empty `choices`) before `data: [DONE]` ONLY when the request set
//     `stream_options: {"include_usage": true}` — exactly like vLLM/OpenAI.
//   - Faults are injectable per request via headers (see below), covering every
//     M1 exit criterion: duplicate-safe capture, client_disconnect,
//     upstream_error (mid-stream 5xx / stream reset), malformed bodies.
//
// Fault-injection headers (all optional):
//
//	X-Fake-Prompt-Tokens     int  override prompt token count
//	X-Fake-Completion-Tokens int  override completion token count
//	X-Fake-Reasoning-Tokens  int  populate completion_tokens_details.reasoning_tokens
//	X-Fake-Cached-Tokens     int  populate prompt_tokens_details.cached_tokens
//	X-Fake-Chunks            int  number of streamed content chunks (default 5)
//	X-Fake-Delay-Ms          int  delay before each chunk / before the response
//	X-Fake-Fault             one of:
//	    http-500             fail before any body with HTTP 500
//	    malformed-json       return syntactically invalid JSON (or a malformed SSE data line)
//	    omit-usage           never emit usage, even if include_usage was requested
//	    disconnect-mid-stream  emit X-Fake-Fault-After chunks, then hard-close the TCP
//	                           connection (no final chunk, no [DONE]) — simulates the
//	                           upstream dying mid-stream (billing status: upstream_error)
//	X-Fake-Fault-After       int  chunk index after which mid-stream faults fire (default chunks/2)
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var reqCounter atomic.Int64

type chatRequest struct {
	Model         string         `json:"model"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options"`
	Messages      []chatMessage  `json:"messages"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type usage struct {
	PromptTokens            int                      `json:"prompt_tokens"`
	CompletionTokens        int                      `json:"completion_tokens"`
	TotalTokens             int                      `json:"total_tokens"`
	CompletionTokensDetails *completionTokensDetails `json:"completion_tokens_details,omitempty"`
	PromptTokensDetails     *promptTokensDetails     `json:"prompt_tokens_details,omitempty"`
}

type completionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

type promptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type fakeParams struct {
	promptTokens     int
	completionTokens int
	reasoningTokens  int
	cachedTokens     int
	chunks           int
	delay            time.Duration
	fault            string
	faultAfter       int
}

func headerInt(r *http.Request, name string, def int) int {
	if v := r.Header.Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func paramsFrom(r *http.Request, req *chatRequest) fakeParams {
	// Deterministic default prompt tokens derived from message content, so
	// tests that don't set headers still get stable, content-dependent counts.
	contentLen := 0
	for _, m := range req.Messages {
		contentLen += len(m.Content)
	}
	p := fakeParams{
		promptTokens:     headerInt(r, "X-Fake-Prompt-Tokens", contentLen/4+3),
		completionTokens: headerInt(r, "X-Fake-Completion-Tokens", 64),
		reasoningTokens:  headerInt(r, "X-Fake-Reasoning-Tokens", 0),
		cachedTokens:     headerInt(r, "X-Fake-Cached-Tokens", 0),
		chunks:           headerInt(r, "X-Fake-Chunks", 5),
		delay:            time.Duration(headerInt(r, "X-Fake-Delay-Ms", 0)) * time.Millisecond,
		fault:            r.Header.Get("X-Fake-Fault"),
	}
	if p.chunks < 1 {
		p.chunks = 1
	}
	p.faultAfter = headerInt(r, "X-Fake-Fault-After", p.chunks/2)
	return p
}

func (p fakeParams) usage() usage {
	u := usage{
		PromptTokens:     p.promptTokens,
		CompletionTokens: p.completionTokens,
		TotalTokens:      p.promptTokens + p.completionTokens,
	}
	if p.reasoningTokens > 0 {
		u.CompletionTokensDetails = &completionTokensDetails{ReasoningTokens: p.reasoningTokens}
	}
	if p.cachedTokens > 0 {
		u.PromptTokensDetails = &promptTokensDetails{CachedTokens: p.cachedTokens}
	}
	return u
}

func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/v1/chat/completions", handleChatCompletions)
	return mux
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":{"message":"invalid JSON body"}}`, http.StatusBadRequest)
		return
	}
	p := paramsFrom(r, &req)
	id := fmt.Sprintf("chatcmpl-fake-%d", reqCounter.Add(1))

	if p.fault == "http-500" {
		time.Sleep(p.delay)
		http.Error(w, `{"error":{"message":"injected upstream failure"}}`, http.StatusInternalServerError)
		return
	}

	if req.Stream {
		streamResponse(w, r, &req, p, id)
		return
	}
	nonStreamResponse(w, &req, p, id)
}

func nonStreamResponse(w http.ResponseWriter, req *chatRequest, p fakeParams, id string) {
	time.Sleep(p.delay)
	w.Header().Set("Content-Type", "application/json")

	if p.fault == "malformed-json" {
		_, _ = fmt.Fprint(w, `{"id":"`+id+`","object":"chat.completion","choices":[{`) // truncated on purpose
		return
	}

	resp := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": fakeContent(p.completionTokens)},
			"finish_reason": "stop",
		}},
	}
	if p.fault != "omit-usage" {
		resp["usage"] = p.usage()
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func streamResponse(w http.ResponseWriter, r *http.Request, req *chatRequest, p fakeParams, id string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, canFlush := w.(http.Flusher)

	writeChunk := func(v any) {
		b, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
		if canFlush {
			flusher.Flush()
		}
	}
	baseChunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}

	// Role announcement chunk (as vLLM/OpenAI do).
	writeChunk(baseChunk(map[string]any{"role": "assistant"}, nil))

	for i := 0; i < p.chunks; i++ {
		time.Sleep(p.delay)

		if p.fault == "disconnect-mid-stream" && i == p.faultAfter {
			// Simulate the upstream dying mid-stream: hard TCP close. The
			// gateway/capture filter sees a stream reset with no usage chunk —
			// this MUST land in the ledger as status=upstream_error (Q15).
			if hj, ok := w.(http.Hijacker); ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
					return
				}
			}
			panic(http.ErrAbortHandler) // fallback: abort the handler => stream reset
		}
		if p.fault == "malformed-json" && i == p.faultAfter {
			_, _ = fmt.Fprintf(w, "data: {not valid json]\n\n")
			if canFlush {
				flusher.Flush()
			}
			continue
		}

		writeChunk(baseChunk(map[string]any{"content": fmt.Sprintf("tok%d ", i)}, nil))
	}

	// Finish chunk.
	writeChunk(baseChunk(map[string]any{}, "stop"))

	// Usage chunk: ONLY when requested via stream_options.include_usage and not
	// suppressed — final data chunk, empty choices, exactly like vLLM.
	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
	if includeUsage && p.fault != "omit-usage" {
		writeChunk(map[string]any{
			"id":      id,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{},
			"usage":   p.usage(),
		})
	}

	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if canFlush {
		flusher.Flush()
	}
	_ = r // reserved for future client-disconnect detection via r.Context()
}

func fakeContent(tokens int) string {
	if tokens > 256 {
		tokens = 256
	}
	return strings.TrimSpace(strings.Repeat("tok ", tokens))
}
