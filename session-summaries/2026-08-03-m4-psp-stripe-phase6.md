# Session Summary — M4 Phase 6: Fraud Controls (Q5)

**Date:** 2026-08-03
**Commit:** 82806e1
**Epic:** m4-psp-stripe

## What landed

Three fail-closed fraud guards added to `maybeReload` in the TenantAccount controller, preventing abuse of card-funded wallets:

1. **MonthlyCapAmount enforcement.** Queries `payment_credit` adjustments from the 1st of the current UTC month via new `QueryAdjustmentTotal` store method. Rejects reload if `monthlyTotal + reloadAmount > monthlyCapAmount`.

2. **24-hour reload velocity limit.** New `MaxReloadsPerDay` field on `ReloadPolicy` (CRD). Queries `payment_credit` count in the last 24h via new `QueryAdjustmentCount` store method. Rejects when count meets the limit.

3. **Payment method cooldown.** New `PaymentMethodChangedAt` and `LastPSPCustomerSecretRef` status fields. Controller detects changes to `spec.pspCustomerSecretRef` on each reconcile and sets a 24h cooldown. Initial setup (no previous ref) does not trigger cooldown.

All three guards fail closed per principle #2: if a ledger query errors, the reload is rejected rather than allowed.

## Files changed

- `api/v1alpha1/tenantaccount_types.go` — 3 new fields (MaxReloadsPerDay, PaymentMethodChangedAt, LastPSPCustomerSecretRef)
- `internal/ledger/store.go` — QueryAdjustmentTotal, QueryAdjustmentCount
- `internal/controller/tenantaccount_controller.go` — PSP secret ref change detection + 3 fraud guards in maybeReload
- `internal/controller/tenantaccount_controller_test.go` — 8 new envtest tests, stub extensions

## Coverage

- Controller: 54.8% → 57.5%
- PSP: 83.8% (unchanged)
- `make money-safety`: clean
- `make test`: 81 tests, all pass
- `make lint`: 4 pre-existing issues only (no new)

## Decisions made

- `MaxReloadsPerDay` validation uses `Minimum=0` (0 means unlimited) rather than `Minimum=1` with a separate "no limit" sentinel.
- `LastPSPCustomerSecretRef` status field approach chosen over condition-based tracking for change detection. Simpler, self-contained in the controller.
- Test helpers hardcode `payment_credit` kind (renamed from `setAdjustmentTotal` to `setPaymentCreditTotal`) to satisfy unparam lint.

## What's next

Phase 7: E2E demo + real-cluster deploy on `agent-security-dev-3`. Full lifecycle demo using Stripe CLI webhook replay. Verify all M4 exit criteria.
