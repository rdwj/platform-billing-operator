# Session Summary: Ship-Readiness Phase 5 — Documentation

**Date:** 2026-08-06
**Epic:** ship-readiness
**Phase:** 5 (Documentation)
**Commit:** `4a7eb1e`

## What landed

Three new docs and two updated files — the minimum a stranger needs to install, configure, and try the operator.

### New files

- **docs/api-reference.md** (716 lines) — CRD API reference for all 7 CRDs. Field tables with types, required/optional, defaults, and validation patterns. Nested type tables broken out. Status conditions, print columns, and YAML examples from config/samples/. Money-is-never-float convention noted at the top.

- **docs/architecture.md** (157 lines) — Operator-facing architecture overview. Mermaid component diagram with subgraphs for Gateway, Control Plane, Data Plane, Storage, Console, and Kuadrant Policy Objects. Nine-step data flow walkthrough. One-paragraph descriptions for each major component. Seven design principles. Integration points with Kuadrant, RHOAI, Prometheus, CloudNativePG, and Gateway API.

### Updated files

- **docs/getting-started.md** (324 lines, was 165) — Extended from PriceBook+Plan only to full lifecycle. New sections: TenantAccount (three billing modes, wallet state machine, `billctl adjust`), BillingConnector (Stripe, credential Secret format, psp-connector isolation), BudgetPolicy (spending caps, Notify/Throttle/Freeze actions), BillingConsole (singleton CR, Route auto-creation, auth modes, RBAC persona selection with ClusterRole binding), Reservation (GPU committed-use, Guaranteed vs Preemptible). OLM install updated to validated `quay.io/rdwj` registry path. M1 section cross-reference updated to local architecture doc.

- **README.md** — CRD table expanded from 2 to 7 CRDs (with Scope column). Architecture blurb added. OLM install snippet added. Links to all three docs.

### Bonus fix

- `api/v1alpha1/pricebook_types.go`: SKU.Unit doc comment corrected from "per-gpu-hour" to "per-gpu-second" (matches all samples and docs).

## Review findings (6 issues caught and fixed)

1. Preemptible reservation described as "billed only for actual usage" — fixed to "discounted fixed monthly rate" (monthlyPrice is required for both classes)
2. `billctl wallet top-up` command doesn't exist — replaced with actual `billctl adjust` with correct flags
3. BillingConsole image reference didn't match Go type default — fixed to `controller:latest`
4. Capture filter protocol was "gRPC" in 3 places — fixed to "HTTP POST" (Rust source uses `dispatch_http_call`)
5. ledger-svc described as gRPC-only — fixed to describe both gRPC and HTTP APIs
6. PSP Connector claimed Adyen/Zuora support — fixed to "Stripe and generic webhook" (only types in the enum)

## What's next

Phase 6: OperatorHub submission. Create operator metadata (icon, description, maintainers, maturity level), add console screenshots, submit PR to `k8s-operatorhub/community-operators`, address review feedback.
