# CLAUDE.md — platform-billing-operator (implementation repo)

Copy this file to the ROOT of the implementation repo at scaffold time. It is deliberately a thin router: per-edit rules live here; everything else is pointed at, not duplicated. The design repo (`x-as-a-business-project-area`) remains the authoritative design reference.

## What this repo is

`platform-billing-operator` — billing, metering, and FinOps for platform services on OpenShift (MaaS on RHOAI/Connectivity Link first; GPUaaS second, per ADR-0005). CRD group `billing.opendatahub.io/v1alpha1`; CLI `billctl`. "Corvid" appears only as a fictitious example customer in docs — never in code identifiers.

## Non-negotiable rules (enforce on every edit)

1. **Money is never a float.** All money in Go is `shopspring/decimal`; in CRDs, decimal strings with pattern validation; in SQL, `NUMERIC(18,6)`. A float money field is a review-blocking defect.
2. **Money is never authoritative in etcd.** CR `status` mirrors ledger state (rate-limited, ≥30s between status patches per object) and must be reconstructible from the ledger.
3. **Fail degraded, never free.** No error path may remove or loosen rate limits. Allowance failure → hold last-known grant → degrade to plan defaults. Never fall through to unlimited. Test this; don't comment it.
4. **Enforcement is observed, not forked.** We write Kuadrant CRs (TokenRateLimitPolicy / RateLimitPolicy / AuthPolicy) via server-side apply, `fieldManager: platform-billing-operator`. Never bypass or reimplement Limitador.
5. **Contracts are law.** `contracts/usage.v1.schema.json` and `api/openapi.yaml` are the interfaces. Breaking changes fail CI (OpenAPI diff gate from M2). New usage fields go in `quantities` / `attributes`, never as envelope breaks.
6. **At-least-once + idempotent.** Ingest upserts on `record_id`; conflicting content → DLQ, never overwrite. The rater is a pure function: identical (ledger, PriceBook revision) input → byte-identical charges (property-tested in CI against `contracts/rating-vectors.v1.json`).
7. **Only ledger-svc holds DB credentials. Only psp-connector holds PSP credentials.** The manager, console BFF, and billing-api reach data through ledger-svc's gRPC API.
8. **All timestamps `timestamptz` UTC.** All metric labels bounded: `tenant`, `model`/`sku`, `geo` — never `key_id` or `request_id`.
9. **`TODO(money)` means unshippable.** No milestone exits with one in merged code.

## Where the design lives (read per milestone, not all at once)

Design repo docs, in precedence order (project-plan/ wins on conflict; see its CLAUDE.md):

- Any milestone: `project-plan/milestones.md` (defs, exit criteria, demo scripts) + `project-plan/architecture.md` (data flow).
- M0 (scaffold, policy compiler): `operator-plan/crds.md`, `operator-plan/controllers.md` §1, recon findings in `impl-seed/recon/RECON-DRAFT.md` (R2: Kuadrant module = `github.com/kuadrant/kuadrant-operator`, TRLP at `api/v1alpha1`; pin to the RHCL-matched tag, not latest).
- M1 (capture + ledger): `project-plan/architecture.md` §3 (WASM strategy, ADR-0006), `operator-plan/ledger-schema.md`, `contracts/`, `fake-openai/`.
- M2 (rating + wallet): `operator-plan/controllers.md` §5, `contracts/rating-vectors.v1.json`, `api/openapi.yaml`.
- M3+: `operator-plan/controllers.md` §2/§6–8, `operator-plan/interfaces.md`, `operator-plan/security-threat-model.md`, `operator-plan/runbook.md`.
- Decisions: `operator-plan/adr/README.md` (6 ADRs) and `design_files/open-questions.md`. Never silently resolve an open ADR in code — update the ADR first.

## Definition of done (every milestone)

e2e demo script green in CI (not laptop); docs page updated; `config/samples/` has working CRs; no `TODO(money)`; OpenAPI diff gate (M2+); `billctl` used in every demo script (M2+).

## Commands

`make dev-cluster` (kind + Gateway API + Kuadrant + CNPG + fake-openai) · `make deploy-dev` · `make demo-m0`..`demo-m6` · `make test-e2e`. The dev gateway must be Istio-based (production uses `data-science-gateway-class`, controller `openshift.io/gateway-controller/v1`) — an Envoy Gateway dev environment produces false positives on filter attachment; see `project-plan/prerequisites.md` §5.

## Toolchain

Go 1.22+, operator-sdk/kubebuilder v4 layout, controller-runtime, `shopspring/decimal`, `pgx`, Kuadrant API types per RECON R2, Stripe Go SDK (psp-connector only, M4). Rust (stable, wasm32-wasip1) for the capture filter — proxy-wasm-rust-sdk pinned to Kuadrant's fork rev `6ceedbb`. Repo layout per `operator-plan/README.md` (adjust `cmd/capture-extproc/` per the ADR-0006 outcome).

## Lessons learned (capture filter, 2026-07-31)

- **proxy-wasm dispatch_http_call context lifetime.** The HTTP context is destroyed when the response completes. If you dispatch an async HTTP call and return `Action::Continue`, the context dies before the callback fires (`proxy_on_http_call_response invalid context_id`). Fix: return `Action::Pause` after dispatching, then `resume_http_response()` in `on_http_call_response`.
- **Istio cluster names as HTTP authority.** Istio names clusters `outbound|8080||host.ns.svc.cluster.local` with pipe characters. Go's `net/http` rejects these as malformed Host headers. Extract the hostname portion for the `:authority` pseudo-header.
- **HTTP/2 Content-Length and body modification.** When a WASM filter modifies the request body (e.g., injecting `include_usage`), the Content-Length was already sent in HTTP/2 HEADERS. The size mismatch causes Envoy to return 400. Fix: remove Content-Length in `on_http_request_headers` before body modification so Envoy uses DATA frame lengths.
- **Istio gateway cluster discovery.** Gateway proxies only auto-discover Envoy clusters for services in their HTTPRoutes. For `dispatch_http_call` to an internal service (ledger-svc), configure a Sidecar egress resource on the gateway workload.
- **setup-postgres.sh vs goose migrations.** `setup-postgres.sh` applies migrations via plain psql; ledger-svc applies via goose. These conflict if both run on the same DB (goose doesn't see psql-applied migrations). The demo script resets the schema and lets goose run fresh.
