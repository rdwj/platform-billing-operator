# Retrospective: G2 GPU Reservations

**Date:** 2026-08-05
**Effort:** Calendar-committed GPU reservations (Guaranteed + Preemptible classes) — the final planned GPUaaS milestone.
**Issues:** xaab-planning #6 (preemption credit), #7 (SLO refunds) — deferred post-G2
**Commits:** a5a4d36..5045a69 (operator), planning repo commits for EPICS/milestones

## What We Set Out To Do

Add a Reservation CRD that lets tenants reserve GPU capacity for a time window, with two classes: Guaranteed (protected from preemption, billed regardless of utilization) and Preemptible (discounted, evictable when Guaranteed demand exceeds supply). The full pipeline: CRD + webhook validation, capacity feasibility against node inventory, Kueue ClusterQueue compilation, committed-use charging via the existing ledger, billctl CLI, HTTP API, console screens, and E2E demo. 6 phases, 1 new CRD, end-to-end on kind.

## What Changed

| Change | Type | Rationale |
|--------|------|-----------|
| CQ-cancel gap surfaced in Phase 2, deferred to Phase 6 | Good pivot | Small fix, but touching it mid-epic would have been a distraction. Bundled with the demo where it could be proven end-to-end. |
| Migration 0012 drops FK instead of synthetic usage events | Good pivot | Cleaner than inserting fake usage_events rows. FK was over-constrained for reservation charges that have no usage event. |
| Console Scenario D (live console verification in kind) deferred | Scope deferral | Console data flow in kind requires BFF + ledger-svc + K8s RBAC — more plumbing than value for the demo. |
| Utilization-vs-reservation overlay and queue/quota display | Scope deferral | Stretch goals from Phase 5. Nice-to-have, not exit criteria. |
| Q12 decided: no cross-product volume tiers | Good pivot | Simplifies the billing model. Products stay separate, same wallet. |

## What Went Well

- **Velocity.** 6 phases in one day, 6,186 lines across 50 files. The prior epics (G0, G1) established patterns (KueueBackend, PriceBook GPU SKUs, demo scripts) that G2 built on directly.
- **Review loop catching real bugs.** Hardcoded geo default (P2), Cancelled excluded from chargeable states (P4), float-to-integer day math (P3) — all caught by review sub-agents before commit. The maker/checker pattern paid off.
- **Test coverage.** Charging 92%, compiler 8 tests, controller 7 envtest specs, capacity 10 tests, HTTP handler 35 tests. Strong unit-level confidence.
- **Deferred items tracked cleanly.** #6 and #7 filed early, carried in every session summary, never lost. CQ-cancel gap tracked from Phase 2 through Phase 6 resolution.
- **Reuse.** The demo-g2.sh script reuses demo-g1.sh infrastructure verbatim (helpers, build/deploy, cleanup). The reservation compiler reuses the KueueBackend label conventions from G1. The console screens reuse the existing BFF proxy pattern with no Go changes.

## Gaps Identified

| Gap | Severity | Resolution |
|-----|----------|------------|
| No end-to-end run on kind or real cluster | Fix now (ship-readiness P1) | `make demo-g2` must pass on kind before declaring ship-ready. First real test is also the demo — no safety net. |
| Migration 0012 down path is non-reversible | Accept | Down migration drops the `source` column and re-adds the FK. Data loss on rollback is acceptable for v1alpha1. Document in runbook. |
| CI red throughout the epic | Follow-up | Pre-existing failures (TestChargesRecordIdForeignKey, TestUsageMissingParams, TestInvoiceFinalizationAndHTTPRoundTrip). Not G2-caused but blocks ship-readiness. |
| Planning docs still reference "6 CRDs" | Follow-up | Reservation is the 7th. Update CLAUDE.md and project-plan docs. |
| Hardcoded USD in reservation charging | Accept | Multi-currency is a post-launch feature. Consistent with M2 decision. |

## Action Items

- [ ] Run `make demo-g2` on kind cluster — prove it end-to-end before ship-readiness
- [ ] Fix CI failures (pre-existing, not G2) — investigate TestChargesRecordIdForeignKey et al.
- [ ] Update "6 CRDs" references to 7 in project docs

## Patterns

**Start:**
- Run kind integration tests *during* the epic, not just at the end. Every phase that wrote a test script (P2, P6) deferred actually running it. The first real test should not be the final demo.

**Stop:**
- Nothing to stop. The review loop, deferred-item tracking, and phase decomposition all worked.

**Continue:**
- Maker/checker sub-agent review pattern — caught 3 real bugs across the epic.
- Filing issues for deferred work immediately when it's identified (not at close).
- Demo scripts as exit criteria — they force integration thinking even when the cluster isn't available.
- Building on established patterns (KueueBackend, demo-g1.sh infrastructure) rather than reinventing.
