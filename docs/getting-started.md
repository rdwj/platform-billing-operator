# Getting Started

The platform-billing-operator compiles high-level billing intent (Plans and PriceBooks) into Kuadrant enforcement policies on a Gateway API gateway. A Plan CR specifies token and request rate limits, model-scope access controls, and a PriceBook reference. The operator produces the corresponding TokenRateLimitPolicy, RateLimitPolicy, and AuthPolicy CRs via server-side apply, and re-stamps them if external changes introduce drift.

## Prerequisites

- Kubernetes 1.30+ (kind, OpenShift, or any conformant cluster)
- [Kuadrant operator](https://kuadrant.io) installed (provides TokenRateLimitPolicy, RateLimitPolicy, AuthPolicy CRDs)
- A Gateway API gateway (Istio or Envoy Gateway) with a Gateway CR that Plans can target
- cert-manager (for webhook TLS in non-OLM deployments)
- [operator-sdk](https://sdk.operatorframework.io/) (for OLM deployment)

## Install via OLM

```bash
# Install OLM if not already present.
operator-sdk olm install

# Build the operator and bundle images.
make docker-build IMG=<registry>/platform-billing-operator:v0.0.1
make bundle IMG=<registry>/platform-billing-operator:v0.0.1
make bundle-build BUNDLE_IMG=<registry>/platform-billing-operator-bundle:v0.0.1

# Push images (or kind load for local clusters).
make docker-push IMG=<registry>/platform-billing-operator:v0.0.1
make bundle-push BUNDLE_IMG=<registry>/platform-billing-operator-bundle:v0.0.1

# Deploy.
operator-sdk run bundle <registry>/platform-billing-operator-bundle:v0.0.1
```

## Install via kustomize (development)

```bash
make deploy IMG=<registry>/platform-billing-operator:v0.0.1
```

## Local development cluster

`make dev-cluster` creates a kind cluster with the full Kuadrant stack (cert-manager, Istio, Kuadrant operator) and a demo gateway:

```bash
make dev-cluster
```

The cluster context is `kind-platform-billing-dev`. A Gateway named `billing-demo-gateway` is created in the `billing-demo` namespace.

## Create a PriceBook

A PriceBook defines SKU pricing. The `effectiveFrom` date must be in the future at creation time. Once that date passes, SKUs become immutable.

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: PriceBook
metadata:
  name: pb-standard
spec:
  currency: USD
  effectiveFrom: "2026-09-01T00:00:00Z"
  noticeDays: 30
  skus:
    - sku: llama-3-70b
      unit: per-1M-tokens
      rates:
        prompt_tokens: "2.50"
        completion_tokens: "10.00"
```

## Create a Plan

A Plan specifies rate limits and model-scope access. It targets a Gateway and references a PriceBook for rating.

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: Plan
metadata:
  name: priority
spec:
  limits:
    tokensPerMinute: 2000000
    tokensPerDay: 400000000
    requestsPerMinute: 600
  modelScopes: ["llama-3-*", "granite-*"]
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: billing-demo-gateway
  priceBookRef: pb-standard
```

## What the operator produces

When a Plan is reconciled, the operator creates these Kuadrant CRs in the same namespace:

| Source field | Compiled CR | Naming |
|---|---|---|
| `limits.tokensPerMinute`, `limits.tokensPerDay` | `TokenRateLimitPolicy` | `<plan>-trlp` |
| `limits.requestsPerMinute` | `RateLimitPolicy` | `<plan>-rlp` |
| `modelScopes` | `AuthPolicy` | `<plan>-auth` |

Each compiled CR carries a `billing.opendatahub.io/managed-by: <plan-name>` label and an ownerReference back to the Plan. If a compiled CR is modified externally, the operator detects the drift and re-stamps it on the next reconcile cycle.

## Status conditions

| Condition | Meaning |
|---|---|
| `Compiled` | The Plan was successfully compiled into enforcement CRs. |
| `Drifted` | A managed CR was modified externally; the operator re-stamped it. |
| `UnsupportedField` | The Plan includes fields (e.g. `maxConcurrency`) that cannot be compiled to Kuadrant CRs. |
| `KuadrantNotInstalled` | Kuadrant CRDs are not registered in the cluster. |
| `GatewayNotFound` | The targeted Gateway does not exist. |
| `AdoptionConflict` | A CR with the expected name exists but is not managed by this Plan. |

## Run the demo

```bash
make demo-m0
```

This runs the M0 end-to-end validation: creates a PriceBook and Plan, verifies compiled CRs, tests drift detection and re-stamp, verifies cleanup on delete, checks unsupported field conditions, and validates PriceBook immutability.

## M1: Metering pipeline

M1 adds usage metering to the billing stack. A Rust WASM capture filter sits in the Istio gateway's Envoy filter chain, intercepts inference responses (streaming and non-streaming), extracts token usage, and POSTs events to ledger-svc. The ledger upserts idempotently into Postgres. The full data flow is documented in the planning repo (`planning/project-plan/architecture.md`).

### Additional prerequisites

- Postgres 16 (deployed automatically by the setup script below)
- Rust toolchain with the `wasm32-wasip1` target:
  ```bash
  rustup target add wasm32-wasip1
  ```

### Set up the ledger database

```bash
make setup-postgres
```

This deploys Postgres 16 into the `billing-system` namespace with the `billing_ledger` database. Migrations are applied by ledger-svc on startup via [goose](https://github.com/pressly/goose).

### Build the capture filter

```bash
make capture-filter
```

This compiles the Rust WASM filter to `bin/capture_filter.wasm`. The filter runs inside Envoy via the WasmPlugin CRD.

### Run the M1 demo

```bash
make demo-m1
```

The demo deploys fake-openai (a fault-injectable OpenAI-compatible upstream), ledger-svc, and the capture filter into the kind cluster, then validates all six M1 exit criteria:

1. Streaming request produces a correct ledger row with token splits
2. Non-streaming request produces a correct ledger row
3. Duplicate events are deduplicated on `record_id`
4. Client disconnect produces a `client_disconnect` status row
5. Upstream 5xx produces an `upstream_error` status row
6. Removing the filter does not disrupt serving (fail-open)

Expected output: 24 checks, all PASS.
