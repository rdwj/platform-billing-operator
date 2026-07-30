# fake-openai

The OpenAI-compatible fault-injectable test upstream — the artifact `project-plan/milestones.md` M1 calls the most important in the project. Pure Go stdlib, no dependencies.

```bash
go test ./...        # 7 tests: usage shapes, SSE semantics, all fault modes
go run . -addr :8090 # run it
curl -s localhost:8090/v1/chat/completions -d '{"model":"m","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}'
```

Semantics mimic vLLM/OpenAI exactly where billing capture cares: usage in non-streaming responses (with `completion_tokens_details` / `prompt_tokens_details` when configured); in streaming, a usage-bearing final chunk (empty `choices`) before `data: [DONE]` **only** when `stream_options.include_usage` was requested. Faults via `X-Fake-*` headers — see the package doc in `server.go` for the full table (token overrides, chunk count, delays, `http-500`, `malformed-json`, `omit-usage`, `disconnect-mid-stream`).

Destination in the implementation repo: `test/fake-openai/` (own module or under the root module — either works; it has no deps). Extend before M1 exit with: `/v1/completions` and `/v1/embeddings` shapes, and a client-disconnect observation hook (`r.Context().Done()`) if the capture filter tests need the upstream to log disconnects.
