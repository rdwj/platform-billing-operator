# Session Summary — 2026-08-02 · g0-gpuaas-collector · Phases 3-4 (closes G0)

**Plan:** NEXT_SESSION-g0-gpuaas-collector.md   **Commits:** 858d163 (main)
**Deployed:** none   **Model:** Opus 4.6

## Plan vs. actual
Planned: rate-conversion fix, GPU PriceBook sample, unified wallet test, E2E demo script. Shipped: all four items. Slipped: none.
Scope: stayed in scope. The M3 wiring commit (e4f4974) was separated out from a concurrent session collision but belongs to M3, not G0.

## Shipped
- `858d163` — Rate-conversion reconciliation: PriceBook sample changed from `per-gpu-hour`/`"4.50"` to `per-gpu-second`/`"0.001250"` (the rater has no unit conversion; all tests already used per-second)
- `858d163` — GPU PriceBook sample (`config/samples/billing_v1alpha1_pricebook_gpu.yaml`): standalone PriceBook with `h200` ($10.50/hr) and `h200-mig-3g.40gb` ($4.50/hr) SKUs
- `858d163` — Golden vector `v1-gpuaas-interval-full-gpu` appended (3600 * 0.002917 = 10.501200). Existing 4 vectors untouched.
- `858d163` — `TestUnifiedWalletAcrossProfiles` integration test: seeds credit, ingests MaaS + GPUaaS events, verifies unified balance (95.492500) and SKU-filtered usage
- `858d163` — `hack/demo-g0.sh` (645 lines, 9 tests) + `make demo-g0` target. Combined PriceBook (3 SKUs), GPU event ingest, unified wallet assertion, Kueue env check, rater determinism.
- `e4f4974` — (M3, split from collision) MaaSBackend wired into operator binary

## Verification & confidence
- Unit tests: `make test` green, all packages pass. `make rater-determinism` 10/10.
- Integration test: `TestUnifiedWalletAcrossProfiles` passes on testcontainers Postgres (balance math, SKU filtering, dual-profile usage). Required `TESTCONTAINERS_RYUK_DISABLED=true` for Podman compatibility.
- Golden vectors: 5/5 pass, including new `v1-gpuaas-interval-full-gpu`
- E2E demo: script written and reviewed but not run on kind (no kind cluster was active this session). Demo-m2.sh patterns followed exactly.
- Confidence: **high** for rater/wallet/integration (proven by tests), **medium** for E2E demo script (not exercised on kind this session)

## Judgment calls & deviations
- Rate-conversion: chose option A (PriceBook quotes per-second) over option B (add conversion logic). Per-second is what every test and the rater already use. No code changes needed.
- E2E demo uses direct HTTP ingest instead of collector-driven events. On kind with fake GPUs, Kueue admits workloads but pods can't schedule (no device plugin). Direct ingest is reliable and matches demo-m2.sh's approach.
- Concurrent session collision: another session committed M3 wiring + my staged G0 files in one commit. Fixed with soft-reset and re-commit as two separate commits.

## Backlog delta
Filed: none. Closed: none. Deferred: none. Memory: none.
EPICS.md updated: g0-gpuaas-collector marked DONE.

## Drift & forward-collisions
- Backward: none
- Forward: G0's unified wallet proves the ADR-0005 property (billing core is product-neutral). This directly supports g1-g3 (GPU enforcement, reservations) which assume the wallet and rater work across profiles.

## For the reviewer
- Sanity-check: the h200 rate ($10.50/hr = $0.002917/s) is a plausible GPU pricing point but was invented for the demo, not sourced from real H200 pricing. Fine for testing; future epics may want real market rates.
- Thin verification: demo-g0.sh was reviewed but not executed on a kind cluster. The script follows demo-m2.sh patterns exactly, so the risk is low, but first-run issues are possible.
- Wants guidance: none

## Risks / watch-fors
- 5 unpushed commits on main (including M3 work from parallel sessions). Push when ready.
- CI lint job has been failing (predates this session). Worth investigating — may be a golangci-lint config issue or the M3 enforcement imports.
- `TESTCONTAINERS_RYUK_DISABLED=true` needed for integration tests on Podman. Not set in CI — could affect future CI integration test jobs.
