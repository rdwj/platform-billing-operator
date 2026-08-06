# Session Summary: G2 Phase 4 — Reservations CRUD + Cancellation

**Date:** 2026-08-05
**Epic:** G2 GPU Reservations
**Phase:** 4 — billctl reservations + billing-api
**Commit:** 6d454d7

## What landed

Reservations HTTP endpoints, billctl CLI, and cancellation with pro-rated
charging. Full CRUD for GPU reservations through both API and CLI.

### CRD changes

- Added `Cancelled` state constant (terminal, alongside Expired)
- Added `CancelledAt *metav1.Time` to ReservationStatus
- Controller treats Cancelled as terminal (no transitions out)
- Capacity calculator excludes Cancelled from capacity counts
- Webhook enum updated via kubebuilder marker

### HTTP handler (`/v1/reservations`)

- GET list with optional `?tenant=` filter
- GET by name
- POST create (uses GenerateName)
- POST `/{name}/cancel` (Status().Patch, 409 for wrong states)
- `NewServer` signature changed from `client.Reader` to `client.Client`
- 14 handler tests via fake k8s client

### billctl reservations

- `list [--tenant T]` — tabwriter output, date-only formatting
- `get NAME` — key-value detail view
- `create` — 8 required flags (tenant, sku, count, geo, class, start, end, monthly-price)
- `cancel NAME`

### Charging job cancellation

- `isChargeable` includes Cancelled state (so computePeriodCharge can run)
- `computePeriodCharge` adds third clamp: `periodEnd = min(monthEnd, windowEnd, cancelledAt)`
- Months after cancellation produce zero charge (cancelledAt < periodStart)
- 5 new tests, 92% coverage

### RBAC

- ledger-svc: `reservations` get/list/create/patch + `reservations/status` get/patch

## Review findings caught and fixed

1. **Billing-correctness bug:** `isChargeable` initially excluded Cancelled, making
   the pro-rata clamping dead code. Fixed by including Cancelled in chargeable states.
2. **CLI response envelope mismatch:** CLI decoded list as raw array, handler wraps
   in `{"reservations": [...]}`. Fixed with envelope struct.
3. **CLI window fields flat vs nested:** CLI had flat Start/End, handler nests under
   Window. Fixed to match handler's nested structure.
4. **Unused --name flag:** Removed from CLI (handler always uses GenerateName).
5. **Tests used impossible state:** Tick test used Active+CancelledAt (never occurs
   in real cancel flow). Updated tests to use Cancelled state.

## Test results

- `make test`: all pass, zero regressions
- httpapi coverage: 11.8% -> 19.6%
- charging coverage: 89.2% -> 92.0%
- controller coverage: 66.2% -> 66.3%
- 35 new tests total

## Key carry-forward for Phase 5 (Console)

- Console phase depends on M7 establishing real-data patterns (M7 Phase 4+)
- `/v1/reservations` endpoint is live and returns: name, tenant, sku, count, geo,
  class, window {start, end}, monthlyPrice, state, cancelledAt, capacityCheck
- Capacity calendar needs reservation window data + class for color-coding
- Utilization-vs-reservation overlay needs both `/v1/reservations` and `/v1/usage`

## Kueue CQ cleanup on cancel

Not implemented in this phase. When a reservation is cancelled, the controller
should re-run compileKueueState to release CQ quota. This is a Phase 2 controller
concern that will need attention when console screens drive cancellation at scale.
