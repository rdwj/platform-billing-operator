# Session Summary — M3 Phase 5 (2026-08-02)

## What landed

**M3 Phase 5 — E2E demo + real-cluster deploy.** Commits: ca8014e (operator), 593ec3d (planning).

- `billctl keys register` subcommand added (was missing — needed for demo round-trip tests)
- `hack/demo-m3.sh` extended from 31 to 63 assertions: pricing resolution (3 tests), keys round-trip (7 tests), key disable on Freeze + re-enable on Thaw (12 tests), plus deploy infrastructure (ServiceAccount + RBAC, BILLING_NAMESPACE, rollout restart)
- 63/63 on kind (`make demo-m3`)
- Deployed full M1+M2+M3 stack to `agent-security-dev-3`:
  - Operator: BuildConfig binary build → controller ImageStream, cross-namespace image-puller RoleBinding
  - Ledger-svc: rebuilt with Phase 4 code, BILLING_NAMESPACE=billing-system, ServiceAccount with CR read RBAC
  - Both components on migration v9 (api_keys table)
- 17/17 exit criteria verified on real cluster (enforcement latency 20s)
- M3 marked DONE in EPICS.md

## Findings

1. **Kind image rollout requires explicit restart.** When rebuilding a container with the same tag and loading into kind, Kubernetes won't trigger a new rollout because the tag string didn't change. The demo script now does `kctl rollout restart` after deploy. This caused the initial Test 6 failure (operator was running Phase 3 code, migration v8, no KeyManager wiring).

2. **OpenShift cross-namespace image pull.** The operator in platform-billing-operator-system needs `system:image-puller` role in billing-system to pull images from the billing-system ImageStream.

3. **zsh vs bash word splitting.** zsh doesn't word-split unquoted variables. The demo script uses `#!/usr/bin/env bash` so `$BILLCTL_ARGS` works, but interactive testing in zsh needs explicit `--api-url "$URL"` flag passing.

4. **Stale Pending state on dirty baseline.** If the database has charges but the TenantAccount starts in Pending state, it won't transition to Frozen — the state machine requires Active → Frozen, not Pending → Frozen. Clean baseline must include both TRUNCATE and CR recreation.

## Cluster state (agent-security-dev-3)

- `billing-system`: Postgres (DC), ledger-svc (Deployment, v9 migrations), wasm-server, loadgen
- `platform-billing-operator-system`: controller-manager (Deployment, v9 migrations, leader elected)
- BuildConfigs: controller, ledger-svc, loadgen (all in billing-system)
- RBAC: ledger-svc SA has Role for PriceBook/Plan/TenantAccount CR reads
- Test artifacts: pb-m3-test, plan-m3-test, ten-m3-test CRs remain in billing-system (can be cleaned up)
