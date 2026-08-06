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

# Deploy the operator from the published bundle.
operator-sdk run bundle quay.io/wjackson/platform-billing-operator-bundle:v0.0.1
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

M1 adds usage metering to the billing stack. A Rust WASM capture filter sits in the Istio gateway's Envoy filter chain, intercepts inference responses (streaming and non-streaming), extracts token usage, and POSTs events to ledger-svc. The ledger upserts idempotently into Postgres. The full data flow is documented in the [Architecture](architecture.md) overview.

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

## TenantAccount (wallet lifecycle)

A TenantAccount represents a billing tenant. It references a Plan, specifies a billing mode, and manages a prepaid wallet. The TenantAccount controller requires a Postgres ledger database -- run `make setup-postgres` (described in the M1 prerequisites above) before creating TenantAccount CRs.

Three billing modes are available:

- **ShowbackOnly** -- usage is rated and visible but no wallet or PSP enforcement applies.
- **Prepaid** -- tenant pre-funds a wallet; usage debits the balance.
- **Invoiced** -- usage accumulates and is invoiced on a billing cycle.

Start with the simplest mode (see `config/samples/billing_v1alpha1_tenantaccount_showback.yaml`):

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-acme
  namespace: platform-billing
spec:
  displayName: Acme Labs
  planRef: {name: basic}
  billingMode: ShowbackOnly
```

For Prepaid tenants, add a reload policy and grace policy (see `config/samples/billing_v1alpha1_tenantaccount.yaml`):

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-helix
  namespace: platform-billing
spec:
  displayName: Helix Biotech
  planRef: {name: priority}
  billingMode: Prepaid
  pspCustomerSecretRef: {name: helix-stripe}
  billingConnectorRef: {name: stripe-connector}
  reloadPolicy:
    enabled: true
    thresholdAmount: "5000"
    reloadAmount: "25000"
    monthlyCapAmount: "60000"
  gracePolicy:
    graceAmount: "50.000000"
    warningThreshold: "100.000000"
```

The TenantAccount controller manages a state machine for Prepaid tenants: **Active** -> **Warning** (balance drops below `warningThreshold`) -> **Grace** (balance exhausted, grace amount in effect) -> **Frozen** (grace exhausted). When a tenant reaches Frozen, the operator applies an AuthPolicy deny at the gateway. A payment or top-up restores the tenant to Active.

### Top up a wallet

For Prepaid tenants, the wallet starts at zero. Add funds with `billctl adjust`:

```bash
billctl adjust \
  --tenant ten-helix \
  --amount 25000.000000 \
  --kind manual_credit \
  --reason "Initial wallet load" \
  --idempotency-key "init-helix-001"
```

Or create an adjustment directly via the ledger API.

## BillingConnector (payment processing)

A BillingConnector configures integration with a payment service provider. The operator supports Stripe (see `config/samples/billing_v1alpha1_billingconnector.yaml`):

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: BillingConnector
metadata:
  name: stripe-connector
spec:
  type: stripe
  credentialsSecretRef:
    name: stripe-credentials
```

The referenced Secret must contain two keys: `secretKey` (Stripe API secret key) and `webhookSecret` (Stripe webhook signing secret). The `psp-connector` binary handles all PSP communication and runs as a separate Deployment, isolating PSP credentials from the operator process.

## BudgetPolicy (spending caps)

A BudgetPolicy defines spending caps with threshold-based actions for a TenantAccount (see `config/samples/billing_v1alpha1_budgetpolicy.yaml`):

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: BudgetPolicy
metadata:
  name: helix-projects
  namespace: platform-billing
spec:
  tenantRef: {name: ten-helix}
  selector:
    keyLabels: {project: prod-rag-pipeline}
  caps:
    - amount: "30000"
      period: Monthly
      thresholds:
        - {pct: 80, action: Notify}
        - {pct: 100, action: Throttle}
```

Three threshold actions are available:

- **Notify** -- sends a webhook and emits a Kubernetes Event.
- **Throttle** -- reduces the tenant's tokens-per-minute limit at the gateway.
- **Freeze** -- applies an AuthPolicy deny, blocking the tenant until the cap resets or is raised.

## BillingConsole (console deployment)

The billing console is deployed via a BillingConsole CR, not bundled in the operator binary. The CR is cluster-scoped and enforces singleton semantics -- the name must be `default` (see `config/samples/billing_v1alpha1_billingconsole.yaml`):

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: BillingConsole
metadata:
  name: default
spec:
  enabled: true
  image: controller:latest
  replicas: 1
```

On OpenShift, a Route is auto-created. Retrieve the console URL from the status:

```bash
kubectl get billingconsole default -o jsonpath='{.status.url}'
```

The console supports three auth modes: **OpenShiftOAuth**, **OIDC**, and **Brokered**. See the [API Reference](api-reference.md) for full configuration details.

### Console RBAC

The console uses RBAC-driven persona selection. Provider and Consumer roles are determined at login via SubjectAccessReview. Two ClusterRoles are provided: `billing-provider` and `billing-consumer`. Bind them to your users:

```bash
kubectl create clusterrolebinding my-provider \
  --clusterrole=billing-provider \
  --user=admin@example.com
```

## Reservation (GPU committed-use)

Reservations support the GPUaaS metering profile for committed-use GPU capacity. This requires a GPU-enabled cluster with the Kueue integration configured (see `config/samples/billing_v1alpha1_reservation_guaranteed.yaml`):

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: Reservation
metadata:
  name: reservation-guaranteed-sample
  namespace: platform-billing-operator-system
spec:
  tenantRef: acme-corp
  sku: h200
  count: 4
  geo: us-east
  window:
    start: "2026-09-01T00:00:00Z"
    end: "2026-09-30T23:59:59Z"
  class: Guaranteed
  monthlyPrice: "12000.000000"
```

Two reservation classes are available: **Guaranteed** (capacity is reserved and billed for the full window) and **Preemptible** (capacity may be reclaimed when Guaranteed demand exceeds supply, billed at a discounted fixed monthly rate).

## What's next

- [API Reference](api-reference.md) -- full CRD field details for all seven resource types.
- [Architecture](architecture.md) -- how the operator components, capture filter, ledger, and console fit together.
