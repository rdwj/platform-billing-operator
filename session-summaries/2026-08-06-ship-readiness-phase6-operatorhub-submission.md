# Session Summary: Ship-Readiness Phase 6 — OperatorHub Submission

**Date:** 2026-08-06
**Epic:** ship-readiness
**Phase:** 6 (OperatorHub Submission)
**Commit:** `4a4f680`

## What landed

OperatorHub submission complete. The operator is published as a PR to the community-operators registry, with all required metadata, a publicly pullable image, and a public source repo.

### Infrastructure changes

- **GitHub repo made public** — `rdwj/platform-billing-operator` changed from private to public.
- **Image references updated** — All references changed from `quay.io/rdwj/` to `quay.io/wjackson/` (Makefile, config/manager/kustomization.yaml, CSV, README.md, docs/getting-started.md).
- **Operator image built and pushed** — `quay.io/wjackson/platform-billing-operator:v0.0.1`, built locally with `--platform linux/amd64` (ec2-dev-2 unreachable). Public on quay.io.

### CSV metadata enrichment

- **Annotations added:** categories (AI/Machine Learning), containerImage, support, repository, description (135-char tile summary).
- **spec.description:** Full markdown — project overview, 9-item feature list, prerequisites, quick start link to getting-started.md.
- **spec.icon:** SVG icon (meter gauge + data-flow dots + dollar sign, two-tone blue). Source at `hack/icon.svg`, base64-encoded in CSV.
- **spec.maintainers:** Wes Jackson, wjackson@redhat.com.
- **spec.links:** GitHub repo, documentation directory.
- **spec.keywords:** billing, metering, finops, openshift-ai, kuadrant, usage, prepaid, wallet.
- **spec.relatedImages:** operator manager + console images for disconnected installs.
- **BillingConsole alm-example:** image updated from `controller:latest` to `quay.io/wjackson/platform-billing-console:v0.0.1`.
- **annotations.yaml:** Added default channel annotation (alpha).

### Console screenshots

Captured from live dev-3 cluster (`billing-console-billing-system.apps.cluster-khsm8.khsm8.sandbox780.opentlc.com`):

- `docs/screenshots/provider-dashboard.png` — Revenue overview: KPIs, trailing 12-week revenue chart, token mix, top tenants, metering health, budget alerts.
- `docs/screenshots/consumer-usage.png` — Tenant portal overview: prepaid balance, spend MTD, daily token chart, budgets, recent activity.
- `docs/screenshots/consumer-usage-reports.png` — Usage & reports page with FOCUS 1.2 export.

### Bundle validation

All three validator suites pass:
- `operator-sdk bundle validate ./bundle` (standard)
- `operator-sdk bundle validate ./bundle --select-optional name=operatorhub`
- `operator-sdk bundle validate ./bundle --select-optional name=good-practices`

### community-operators PR

- Fork: `rdwj/community-operators`
- Branch: `platform-billing-operator-v0.0.1`
- PR: https://github.com/k8s-operatorhub/community-operators/pull/8947
- 15 files: 7 CRDs, CSV, 2 RBAC ClusterRoles, 2 Services, annotations.yaml, ci.yaml (semver-mode)
- DCO-signed commit per contribution requirements

## Known remaining items

1. **Console image** — `quay.io/wjackson/platform-billing-console:v0.0.1` is referenced in CSV relatedImages and alm-examples but does not yet exist on quay.io. OperatorHub CI may flag this.
2. **PR review** — Monitor PR #8947 for reviewer feedback. Common asks: `crds-have-resources` scorecard (known Phase 4 issue), icon format, metadata tweaks.
3. **Admin/day-2 guide** — Deferred from Phase 5, not blocking alpha.

## What's next

Monitor the community-operators PR. Build and push the console image. Address reviewer feedback as it arrives. If the PR merges, the ship-readiness epic is complete.
