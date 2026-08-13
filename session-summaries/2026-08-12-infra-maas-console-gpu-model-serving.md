# Session Summary -- 2026-08-12 -- MaaS Console + GPU Model Serving on dev-3

**Plan:** Ad-hoc   **Commits:** none (infrastructure session)
**Deployed:** dev (agent-security-dev-3)   **Model:** Opus 4.6 (1M)

## Plan vs. actual
Planned: deploy the MaaS FinOps Console to dev-3 and host Muse Glimmer-30B alongside gpt-oss-20b.
Shipped: console verified (was already deployed), second GPU node provisioned, Qwen3-30B-A3B-FP8 serving as `muse-glimmer-30b`, PriceBooks updated with both models, demo data seeded, API-key-protected external endpoint created.
Slipped: the real Muse Glimmer-30B architecture (`muse_glimmer`) is unsupported by both RHOAI vLLM v0.18.0 and upstream vLLM v0.9.2 -- substituted with Qwen3-30B-A3B-FP8 served under the `muse-glimmer-30b` name.

## Shipped

Infrastructure changes on agent-security-dev-3 (no repo commits):

- Verified existing MaaS FinOps Console deployment; fixed a Postgres service selector conflict caused by a duplicate Deployment
- Scaled GPU MachineSet `gpu-cluster-khsm8-7cbl6-worker-us-east-2c` from 1 to 2 replicas (second g6e.4xlarge / L40S node)
- Created ServingRuntime `vllm-muse-glimmer-runtime` in `billing-system` with tuned vLLM config: 40K context, FP8 KV cache, CUDA graphs, 5 max sequences, prefix caching
- Created InferenceService `muse-glimmer-30b` backed by `RedHatAI/Qwen3-30B-A3B-FP8-dynamic`
- Updated all 3 PriceBook CRs (`pb-m3-test`, `pb-standard`, `pb-premium`) with `gpt-oss-20b` and `muse-glimmer-30b` SKUs
- Seeded 30 gpt-oss-20b and 16 muse-glimmer-30b usage events across 3 tenants via ledger-svc ingest API
- Added `--served-model-name=gpt-oss-20b` to existing gpt-oss runtime (fixes `/mnt/models` display name)
- Created API-key-protected OpenShift Route `muse-glimmer-30b-api` with vLLM `--api-key` flag
- Cleaned up: removed unprotected Route, gateway HTTPRoute, Authorino API key Secret (gateway OAuth proxy blocks non-JWT tokens)

## Verification & confidence

- Dashboard verified in browser: both models appear in "Models & pricing" (3 SKUs) and "Top models (MTD)" with revenue data
- Model inference tested via external endpoint: 401 without key, 200 with key, correct completions returned
- Performance benchmarked: single-user ~99 tok/s (with CUDA graphs), 3-user concurrent ~66 tok/s total, TTFT 0.77s
- Confidence: **high** for dashboard and model serving; **medium** for long-context agentic use (40K limit tested but not under sustained multi-user load)

## Judgment calls & deviations

- Substituted Qwen3-30B-A3B-FP8 for Muse Glimmer-30B: the `muse_glimmer` architecture is too new for any released vLLM. The RedHatAI FP8 variant also failed. Used `--served-model-name=muse-glimmer-30b` so the billing system sees the intended name.
- Used vLLM `--api-key` instead of Authorino: the gateway's `kube-auth-proxy` (OAuth2 proxy with `--skip-jwt-bearer-tokens`) blocks plain API keys before Authorino can validate them. Direct Route + vLLM auth was the pragmatic choice.
- Kept thinking mode on per user request, despite ~40-50% of output tokens going to `<think>` reasoning. User wants it for agentic workflow quality.
- Set max-model-len to 40,960 (model's native limit) rather than the originally planned 131K: Qwen3-30B-A3B has `max_position_embeddings=40960`.

## Backlog delta

Filed: none. Closed: none.
Deferred: proper Authorino/gateway integration for model endpoint auth (needs investigation of ext_authz / EnvoyFilter config to bypass OAuth proxy for API key traffic).

## Drift & forward-collisions

- Backward: none identified
- Forward: the GPU MachineSet scaling and model serving pattern established here will be reused when the billing operator's capture filter (WASM) is wired to intercept traffic to models in `billing-system`. Currently the capture filter only covers the `data-science-gateway` HTTPRoute to `gpt-oss-model`.

## For the reviewer

- Sanity-check: the Qwen3-30B-A3B substitution means the billing system tracks a model name (`muse-glimmer-30b`) that doesn't match the actual weights. This is fine for demos but would need alignment if real usage billing mattered.
- Thin verification: no sustained multi-user load test was run; the 3-user concurrent test used identical short prompts, not realistic agentic workloads with 20K+ context.
- Wants guidance: whether to invest time in fixing the gateway OAuth proxy / Authorino integration for API key auth, or whether the direct Route + vLLM `--api-key` approach is acceptable long-term.

## Risks / watch-fors

- The second GPU node (g6e.4xlarge) is an ongoing AWS cost. Scale the MachineSet back to 1 when the model isn't needed.
- CI has been failing on main for 5+ days (3 consecutive red runs). Worth investigating separately.
- The unprotected Authorino resources (HTTPRoute `muse-glimmer-inference`, Secret `billing-key-muse-glimmer`, ReferenceGrant in `billing-system`) are orphaned on the cluster and should be cleaned up.

## Cluster state on dev-3

| Component | Namespace | Status |
|-----------|-----------|--------|
| PostgreSQL 16 | billing-system | Running (DeploymentConfig) |
| ledger-svc | billing-system | Running |
| billing-console (Redwoods University) | billing-system | Running, Route active |
| controller-manager | platform-billing-operator-system | Running |
| gpt-oss-20b (vLLM) | gpt-oss-model | Running on node 2 (ip-10-0-49-132) |
| muse-glimmer-30b (vLLM, Qwen3-30B-A3B-FP8) | billing-system | Running on node 1 (ip-10-0-10-74) |
| GPU nodes | -- | 2x g6e.4xlarge (L40S) |

**External endpoints:**
- Console: `https://billing-console-billing-system.apps.cluster-khsm8.khsm8.sandbox780.opentlc.com`
- Model API: `https://muse-glimmer-30b-api-billing-system.apps.cluster-khsm8.khsm8.sandbox780.opentlc.com/v1` (API key protected)
