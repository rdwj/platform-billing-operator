# CRD API Reference

API group: `billing.opendatahub.io/v1alpha1`

This document covers the 7 Custom Resource Definitions shipped by the platform-billing-operator. All money fields throughout the API are **decimal strings** (e.g. `"2.50"`, `"25000.000000"`), validated by the pattern `^\d+(\.\d{1,6})?$`. Money is never a float.

## Table of Contents

- [PriceBook](#pricebook)
- [Plan](#plan)
- [TenantAccount](#tenantaccount)
- [BillingConnector](#billingconnector)
- [BudgetPolicy](#budgetpolicy)
- [BillingConsole](#billingconsole)
- [Reservation](#reservation)

---

## PriceBook

Defines the catalog of SKU rates, volume discounts, and committed-use tiers that govern how tenant usage is rated and invoiced.

**Scope:** Namespaced

### Spec Fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `currency` | string | Yes | -- | ISO 4217 currency code. Enum: `USD`, `EUR`, `GBP`, `JPY`, `CAD`, `AUD`, `CHF`. |
| `effectiveFrom` | metav1.Time | Yes | -- | Timestamp when this PriceBook becomes Active. Must be >= now + noticeDays at creation. |
| `noticeDays` | int32 | Yes | -- | Minimum notice period (days) tenants receive before price changes take effect. Min: 0. |
| `supersedes` | string | No | -- | Name of the prior PriceBook this one replaces. |
| `providerFaultPolicy` | string | No | `zero_charge` | Charging on upstream model error. Enum: `zero_charge`, `bill_partial`. |
| `clientDisconnectPolicy` | string | No | `bill_partial` | Charging on client disconnect mid-stream. Enum: `bill_partial`, `zero_charge`. |
| `skus` | []SKU | Yes | -- | SKU pricing entries. MinItems: 1. |
| `volumeTiers` | []VolumeTier | No | -- | Volume-based discount tiers applied to aggregate usage. |
| `committedUse` | []CommittedUseTier | No | -- | Committed-use pricing tiers for sustained throughput. |

**SKU**

| Field | Type | Required | Description |
|---|---|---|---|
| `sku` | string | Yes | Model or product identifier (e.g. `gpt-4o`, `llama-3-70b`, `h200-mig-3g.40gb`). |
| `unit` | string | Yes | Pricing unit (e.g. `per-1M-tokens`, `per-gpu-second`). |
| `geo` | string | No | Geographic region. Empty string = default for all regions. |
| `rates` | map[string]string | Yes | Maps usage dimensions (e.g. `prompt_tokens`, `completion_tokens`, `cached_tokens`, `reasoning_tokens`, `requests`, `gpu_seconds`) to decimal-string rates. |
| `passThrough` | PassThroughConfig | No | Pass-through pricing derived from upstream costs plus markup. |

**PassThroughConfig**

| Field | Type | Required | Description |
|---|---|---|---|
| `provider` | string | Yes | Upstream provider name (e.g. `openai`, `anthropic`). |
| `markupPct` | string | Yes | Markup percentage as a decimal string (e.g. `"15.0"` for 15%). Pattern: `^\d+(\.\d{1,6})?$`. |
| `costSecretRef` | LocalObjectReference | Yes | Secret containing the provider's cost data. |

**VolumeTier**

| Field | Type | Required | Description |
|---|---|---|---|
| `thresholdTokens` | int64 | Yes | Monthly token count at which this tier activates. Min: 0. |
| `discountPct` | string | Yes | Discount percentage as a decimal string. Pattern: `^\d+(\.\d{1,6})?$`. |

**CommittedUseTier**

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | Yes | Human-readable tier name (e.g. `starter`, `growth`, `enterprise`). |
| `tokensPerSecond` | int64 | Yes | Committed throughput in tokens per second. Min: 1. |
| `monthlyPrice` | string | Yes | Fixed monthly price as a decimal string. Pattern: `^\d+(\.\d{1,6})?$`. |

### Status Fields

| Field | Type | Description |
|---|---|---|
| `state` | string | Current lifecycle state. Enum: `Draft`, `Announced`, `Active`, `Superseded`, `Retired`. |
| `activeTenants` | int32 | Count of TenantAccounts currently using this PriceBook. |
| `grandfatheredTenants` | []string | Tenant names pinned to this PriceBook via `priceBookPin` (not auto-migrated). |
| `observedGeneration` | int64 | Most recent generation observed by the controller. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions. |

### Print Columns

| Name | Type | JSONPath |
|---|---|---|
| State | string | `.status.state` |
| Effective | date | `.spec.effectiveFrom` |
| Currency | string | `.spec.currency` |
| Age | date | `.metadata.creationTimestamp` |

### Example

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
        reasoning_tokens: "10.00"
        cached_tokens: "0.625"
    - sku: h200-mig-3g.40gb
      unit: per-gpu-second
      rates:
        gpu_seconds: "0.001250"
  volumeTiers:
    - thresholdTokens: 1000000000
      discountPct: "8"
```

---

## Plan

Defines a billing plan with rate limits and model scopes that compiles down to Kuadrant TokenRateLimitPolicy, RateLimitPolicy, and AuthPolicy CRs.

**Scope:** Namespaced

### Spec Fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `limits` | PlanLimits | Yes | -- | Rate limit thresholds for this plan. |
| `modelScopes` | []string | No | -- | Glob patterns for model matching. Compiled into AuthPolicy CEL predicates. |
| `geoScopes` | []string | No | `["*"]` | Geographic scopes. `["*"]` means all regions. |
| `queueClass` | string | No | -- | Informational annotation passed to the serving layer. |
| `targetRef` | LocalPolicyTargetReferenceWithSectionName | Yes | -- | Gateway API resource this Plan attaches to (standard policy attachment pattern). |
| `priceBookRef` | string | No | -- | Name of the PriceBook to use for rating. |

**PlanLimits**

| Field | Type | Required | Description |
|---|---|---|---|
| `tokensPerMinute` | *int64 | No | Token rate limit per minute. Min: 0. |
| `tokensPerDay` | *int64 | No | Token rate limit per day. Min: 0. |
| `requestsPerMinute` | *int32 | No | Request rate limit per minute. Min: 0. |
| `maxConcurrency` | *int32 | No | Concurrent request limit. Not compilable to Kuadrant (stored only; sets `UnsupportedField` condition). Min: 0. |
| `gpuQuota` | *int32 | No | Max GPUs for Kueue workloads. When set, KueueBackend manages a per-tenant ClusterQueue with this nominalQuota for `nvidia.com/gpu`. Min: 0. |

### Status Fields

| Field | Type | Description |
|---|---|---|
| `compiledPolicies` | []CompiledPolicyRef | Managed Kuadrant CRs produced by the PolicyCompiler. |
| `observedGeneration` | int64 | Most recent generation observed by the controller. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions. |

**CompiledPolicyRef**

| Field | Type | Description |
|---|---|---|
| `group` | string | API group of the compiled CR. |
| `kind` | string | Kind of the compiled CR. |
| `name` | string | Name of the compiled CR. |
| `namespace` | string | Namespace of the compiled CR. |
| `upToDate` | bool | Whether the compiled CR matches the desired state. |

### Status Conditions

| Condition | Description |
|---|---|
| `Compiled` | All Kuadrant CRs have been created or updated successfully. |
| `Drifted` | A managed Kuadrant CR has been modified externally and no longer matches the desired state. |
| `AdoptionConflict` | A pre-existing Kuadrant CR conflicts with the name the compiler would generate. |
| `KuadrantNotInstalled` | Required Kuadrant CRDs are not present on the cluster. |
| `GatewayNotFound` | The gateway referenced by `targetRef` does not exist. |
| `UnsupportedField` | A spec field (e.g. `maxConcurrency`) cannot be compiled to a Kuadrant policy. |

### Print Columns

| Name | Type | JSONPath | Priority |
|---|---|---|---|
| TargetKind | string | `.spec.targetRef.kind` | 1 |
| TargetName | string | `.spec.targetRef.name` | 1 |
| Age | date | `.metadata.creationTimestamp` | 0 |

### Example

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: Plan
metadata:
  name: priority
spec:
  limits:
    requestsPerMinute: 600
    tokensPerMinute: 2000000
    tokensPerDay: 400000000
    maxConcurrency: 64
  modelScopes: ["llama-3-*", "granite-*"]
  geoScopes: ["*"]
  queueClass: priority
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: billing-demo-gateway
  priceBookRef: pb-standard
```

---

## TenantAccount

Represents a billing tenant with a wallet, billing mode, and lifecycle state.

**Scope:** Namespaced

### Spec Fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `displayName` | string | Yes | -- | Human-readable tenant name. |
| `planRef` | LocalObjectReference | Yes | -- | Plan CR that governs this tenant's rate limits. |
| `priceBookPin` | string | No | -- | Pins tenant to a specific PriceBook by name, overriding the default "follow Active" behavior (grandfathering). |
| `billingMode` | string | Yes | -- | How usage is billed. Enum: `ShowbackOnly`, `Prepaid`, `Invoiced`. |
| `pspCustomerSecretRef` | *LocalObjectReference | No | -- | Secret containing PSP customer data (keys: `customerId`, `defaultPaymentMethodId`). |
| `billingConnectorRef` | *LocalObjectReference | No | -- | BillingConnector providing payment processing. Required for auto-reload. |
| `reloadPolicy` | *ReloadPolicy | No | -- | Automatic wallet reload configuration (Prepaid mode only). |
| `gracePolicy` | *GracePolicy | No | -- | Behavior when balance drops to zero. |
| `gpuFreezeMode` | string | No | `Drain` | GPU workload handling on balance depletion. Enum: `Drain` (running workloads finish, no new ones admitted), `Preempt` (running workloads evicted). |

**ReloadPolicy**

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | bool | Yes | Whether automatic reload is active. |
| `thresholdAmount` | string | Yes | Balance level that triggers a reload. Pattern: `^\d+(\.\d{1,6})?$`. |
| `reloadAmount` | string | Yes | Amount to add on each reload. Pattern: `^\d+(\.\d{1,6})?$`. |
| `monthlyCapAmount` | string | Yes | Maximum reload spend per calendar month. Pattern: `^\d+(\.\d{1,6})?$`. |
| `maxReloadsPerDay` | int32 | No | Maximum reload attempts in a rolling 24-hour window. 0 = no limit. Min: 0. |

**GracePolicy**

| Field | Type | Required | Description |
|---|---|---|---|
| `graceAmount` | string | Yes | Dollar amount below zero allowed before enforcement fires. Default: `"0.000000"` (freeze on next request after crossing zero; the request that crossed zero still completes). Pattern: `^\d+(\.\d{1,6})?$`. |
| `warningThreshold` | string | No | Balance level at which Warning state is entered (advisory only, no enforcement). Pattern: `^\d+(\.\d{1,6})?$`. |

### Status Fields

| Field | Type | Description |
|---|---|---|
| `state` | string | Current lifecycle state. Enum: `Pending`, `Active`, `Warning`, `Grace`, `Dunning`, `Frozen`, `Suspended`, `Closed`. |
| `balance` | string | Current wallet balance (decimal string). Mirrored from ledger. NOT authoritative -- the ledger is the source of truth. |
| `balanceObservedAt` | *metav1.Time | Timestamp when balance was last computed. |
| `runwayDays` | string | Estimated days of service remaining at current spend rate (decimal string). |
| `mtdSpend` | string | Month-to-date spend (decimal string). |
| `allowanceTokens` | string | Token allowance computed from balance and pricing, used for enforcement (decimal string). |
| `consecutivePaymentFailures` | int32 | Consecutive payment failures for event-gated Dunning to Frozen escalation (threshold M=3). Reset on success. |
| `pendingReloadIntentID` | string | PSP intent ID of an in-flight reload payment. Prevents duplicate PaymentIntent creation. |
| `paymentMethodChangedAt` | *metav1.Time | When the tenant's payment method was last changed. Used for cooldown enforcement. |
| `lastPSPCustomerSecretRef` | string | Name of the last-observed PSPCustomerSecretRef, used to detect payment method changes. |
| `observedGeneration` | int64 | Most recent generation observed by the controller. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions. |

### Status Conditions

| Condition | Description |
|---|---|
| `BalanceHealthy` | Wallet balance is above warning/grace thresholds. |
| `PaymentMethodValid` | The referenced PSP payment method is valid and chargeable. |
| `BalanceStale` | Balance mirror has not been refreshed within the expected window. |
| `PaymentCorroborationFailed` | A payment charge attempt failed PSP-side corroboration. |
| `PaymentDisputed` | A tenant has disputed a charge with their PSP. |
| `ReconcileDrift` | The ledger balance and the status balance have diverged beyond tolerance. |

### Print Columns

| Name | Type | JSONPath |
|---|---|---|
| State | string | `.status.state` |
| Mode | string | `.spec.billingMode` |
| Balance | string | `.status.balance` |
| Age | date | `.metadata.creationTimestamp` |

### Example (Prepaid)

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

### Example (ShowbackOnly)

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

---

## BillingConnector

Configures integration with a payment service provider (Stripe, generic webhook) for the psp-connector binary.

**Scope:** Namespaced

### Spec Fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `type` | string | Yes | -- | Payment service provider type. Enum: `stripe`, `webhook`. |
| `credentialsSecretRef` | LocalObjectReference | Yes | -- | Secret containing PSP credentials. For Stripe: keys `secretKey`, `webhookSecret`. For webhook: key `hmacSecret`. |
| `webhookIngress` | *WebhookIngressConfig | No | -- | Publicly reachable endpoint for PSP webhook delivery. |
| `sync` | *SyncConfig | No | -- | Polling intervals for usage and invoice sync. |
| `taxMode` | string | No | `none` | Tax computation delegation. Enum: `psp`, `none`. |

**WebhookIngressConfig**

| Field | Type | Required | Description |
|---|---|---|---|
| `host` | string | Yes | Publicly reachable hostname for webhook delivery. |
| `tlsSecretRef` | *LocalObjectReference | No | Secret containing TLS certificate and key for the webhook endpoint. |

**SyncConfig**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `usageRecordsInterval` | string | No | `5m` | Interval for pushing usage records to the PSP. |
| `invoiceInterval` | string | No | `1h` | Interval for syncing invoice state with the PSP. |

### Status Fields

| Field | Type | Description |
|---|---|---|
| `connected` | bool | Whether the connector has successfully authenticated with the PSP. |
| `lastSync` | *LastSyncStatus | Result of the most recent sync operation. |
| `webhookStats` | *WebhookStatsStatus | Webhook delivery statistics. |
| `observedGeneration` | int64 | Most recent generation observed by the controller. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions. |

**LastSyncStatus**

| Field | Type | Description |
|---|---|---|
| `at` | *metav1.Time | Timestamp of the last sync. |
| `pushedRecords` | int64 | Records pushed in the last sync. |
| `failed` | int64 | Records that failed to push. |

**WebhookStatsStatus**

| Field | Type | Description |
|---|---|---|
| `received24h` | int64 | Webhooks received in the last 24 hours. |
| `signatureFailures` | int64 | Webhook signature verification failures in the last 24 hours. |

### Status Conditions

| Condition | Description |
|---|---|
| `CredentialsValid` | The PSP credentials in the referenced Secret are valid. |
| `WebhookReachable` | The webhook endpoint is reachable and accepting deliveries. |
| `SyncHealthy` | Usage record and invoice sync is completing without errors. |

### Print Columns

| Name | Type | JSONPath |
|---|---|---|
| Type | string | `.spec.type` |
| Connected | boolean | `.status.connected` |
| Age | date | `.metadata.creationTimestamp` |

### Example

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: BillingConnector
metadata:
  name: billingconnector-sample
spec:
  type: stripe
  credentialsSecretRef:
    name: stripe-credentials
```

---

## BudgetPolicy

Defines spending caps and threshold-based actions for a TenantAccount.

**Scope:** Namespaced

### Spec Fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `tenantRef` | LocalObjectReference | Yes | -- | TenantAccount this budget applies to. |
| `selector` | *BudgetSelector | No | -- | Scopes the budget to a subset of the tenant's API keys. |
| `caps` | []BudgetCap | Yes | -- | Spending caps with thresholds. MinItems: 1. |

**BudgetSelector**

| Field | Type | Required | Description |
|---|---|---|---|
| `keyLabels` | map[string]string | No | Matches API keys carrying these labels. |

**BudgetCap**

| Field | Type | Required | Description |
|---|---|---|---|
| `amount` | string | Yes | Cap amount as a decimal string. Pattern: `^\d+(\.\d{1,6})?$`. |
| `period` | string | Yes | Time window. Enum: `Monthly`, `Weekly`, `Daily`. |
| `thresholds` | []BudgetThreshold | Yes | Percentage trigger points within this cap. MinItems: 1. |

**BudgetThreshold**

| Field | Type | Required | Description |
|---|---|---|---|
| `pct` | int32 | Yes | Percentage of the cap at which this threshold fires. Min: 1, Max: 100. |
| `action` | string | Yes | Enforcement action. Notify always fires alongside Throttle or Freeze. Enum: `Notify`, `Throttle`, `Freeze`. |

### Status Fields

| Field | Type | Description |
|---|---|---|
| `currentSpend` | string | Current period spend (decimal string). Reflects the last-evaluated cap's spend. |
| `capPeriodEnds` | []CapPeriodEnd | Period boundary for each cap independently, allowing caps with different periods to coexist. |
| `firedThresholds` | []FiredThreshold | Thresholds that have fired in the current period. Reset on period rollover per cap. |
| `observedGeneration` | int64 | Most recent generation observed by the controller. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions. |

**FiredThreshold**

| Field | Type | Description |
|---|---|---|
| `capIndex` | int32 | Which cap in the spec this threshold belongs to. |
| `pct` | int32 | The threshold percentage that fired. |
| `firedAt` | metav1.Time | Timestamp when the threshold fired. |

**CapPeriodEnd**

| Field | Type | Description |
|---|---|---|
| `capIndex` | int32 | Which cap in the spec this entry belongs to. |
| `periodEnd` | metav1.Time | End of the current evaluation period for this cap (UTC). |

### Status Conditions

| Condition | Description |
|---|---|
| `Evaluating` | The budget is being evaluated against current spend data. |
| `WithinBudget` | All caps are within their thresholds. |
| `ThresholdFired` | One or more thresholds have been triggered. |

### Print Columns

| Name | Type | JSONPath |
|---|---|---|
| Tenant | string | `.spec.tenantRef.name` |
| Spend | string | `.status.currentSpend` |
| Age | date | `.metadata.creationTimestamp` |

### Example

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

---

## BillingConsole

Configures operator-managed deployment of the billing console UI. Cluster-scoped; the controller enforces singleton semantics (name: `default`).

**Scope:** Cluster

### Spec Fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | *bool | No | `true` | Whether the console Deployment is created. |
| `image` | string | No | `controller:latest` | Console container image reference. |
| `replicas` | *int32 | No | `1` | Number of console pod replicas. |
| `route` | *ConsoleRouteConfig | No | -- | OpenShift Route configuration for the console. |
| `auth` | *ConsoleAuthConfig | No | -- | Authentication configuration. |
| `tenantPortal` | *TenantPortalConfig | No | -- | Tenant-facing portal configuration. |

**ConsoleRouteConfig**

| Field | Type | Required | Description |
|---|---|---|---|
| `host` | string | No | Desired hostname for the console Route. Empty = auto-generated by OpenShift. |
| `tlsSecretRef` | *LocalObjectReference | No | Secret containing TLS cert/key for the Route. Unset = OpenShift generates a certificate. |

**ConsoleAuthConfig**

| Field | Type | Required | Description |
|---|---|---|---|
| `mode` | string | No | Authentication mechanism. Enum: `OpenShiftOAuth`, `OIDC`, `Brokered`. |
| `oidc` | *OIDCConfig | No | OIDC authentication configuration. |
| `broker` | *BrokerConfig | No | Brokered (federated IdP) authentication configuration. |
| `roles` | *ConsoleRoles | No | Maps IdP groups to console roles. |

**OIDCConfig**

| Field | Type | Required | Description |
|---|---|---|---|
| `issuerURL` | string | Yes | OIDC issuer URL. |
| `clientSecretRef` | LocalObjectReference | Yes | Secret containing OIDC client credentials. Expected keys: `client-id`, `client-secret`. |
| `groupsClaim` | string | No | JWT claim containing group memberships. Default: `groups`. |

**BrokerConfig**

| Field | Type | Required | Description |
|---|---|---|---|
| `deploy` | bool | No | Whether the operator deploys an embedded identity broker. |
| `existingIssuerURL` | string | No | Points to an externally managed identity broker. |

**ConsoleRoles**

| Field | Type | Required | Description |
|---|---|---|---|
| `admin` | []string | No | Groups with full access including walkthroughs and PriceBook editing. |
| `operator` | []string | No | Groups that can manage tenants, dunning, and metering health. |
| `viewer` | []string | No | Groups with read-only access. |

**TenantPortalConfig**

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `enabled` | *bool | No | `true` | Whether the tenant portal is served at `/portal`. |

### Status Fields

| Field | Type | Description |
|---|---|---|
| `url` | string | Externally reachable URL of the console. |
| `observedGeneration` | int64 | Most recent generation observed by the controller. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions. |

### Status Conditions

| Condition | Description |
|---|---|
| `Deployed` | The console Deployment and Service are running. |
| `RouteAdmitted` | The OpenShift Route has been admitted by the router. |
| `AuthConfigured` | The selected authentication mechanism is configured and operational. |

### Print Columns

| Name | Type | JSONPath |
|---|---|---|
| URL | string | `.status.url` |
| Deployed | string | `.status.conditions[?(@.type=="Deployed")].status` |
| Age | date | `.metadata.creationTimestamp` |

### Example

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: BillingConsole
metadata:
  name: default
spec:
  enabled: true
  image: controller:latest
  replicas: 1
  route:
    host: ""
  # auth:
  #   mode: OpenShiftOAuth
  #   roles:
  #     admin: ["billing-admins"]
  #     operator: ["billing-ops"]
  #     viewer: ["finance-ro"]
```

---

## Reservation

Represents a calendar-committed GPU reservation with a time window, capacity class (Guaranteed or Preemptible), and fixed monthly pricing.

**Scope:** Namespaced

### Spec Fields

| Field | Type | Required | Default | Description |
|---|---|---|---|---|
| `tenantRef` | string | Yes | -- | Name of the TenantAccount this reservation belongs to. |
| `sku` | string | Yes | -- | GPU product identifier (e.g. `h200`, `h200-mig-3g.40gb`). MinLength: 1. |
| `count` | int32 | Yes | -- | Number of GPUs reserved. Min: 1. |
| `geo` | string | Yes | -- | Geographic region for this reservation. |
| `window` | ReservationWindow | Yes | -- | Start and end times of the reservation. |
| `class` | string | Yes | -- | Reservation tier. `Guaranteed`: capacity protected from preemption, billed regardless of utilization. `Preemptible`: capacity may be reclaimed when Guaranteed demand exceeds supply. Enum: `Guaranteed`, `Preemptible`. |
| `monthlyPrice` | string | Yes | -- | Fixed monthly price as a decimal string. Pattern: `^\d+(\.\d{1,6})?$`. |

**ReservationWindow**

| Field | Type | Required | Description |
|---|---|---|---|
| `start` | metav1.Time | Yes | Beginning of the reservation window. |
| `end` | metav1.Time | Yes | End of the reservation window. |

### Status Fields

| Field | Type | Description |
|---|---|---|
| `state` | string | Current lifecycle state. Enum: `Requested`, `Confirmed`, `Active`, `Expired`, `Cancelled`. |
| `cancelledAt` | *metav1.Time | When the reservation was cancelled. |
| `capacityCheck` | *CapacityCheckResult | Result of the most recent capacity feasibility check. |
| `observedGeneration` | int64 | Most recent generation observed by the controller. |
| `conditions` | []metav1.Condition | Standard Kubernetes conditions. |

**CapacityCheckResult**

| Field | Type | Description |
|---|---|---|
| `available` | bool | Whether sufficient capacity was found. |
| `pool` | string | Capacity pool that satisfied the request. |

### Status Conditions

| Condition | Description |
|---|---|
| `CapacityConfirmed` | Sufficient GPU capacity exists to fulfill the reservation. |
| `Charged` | The reservation's monthly price has been charged to the tenant's wallet. |
| `KueueSynced` | The corresponding Kueue AdmissionCheck or ClusterQueue has been created or updated. |

### Print Columns

| Name | Type | JSONPath |
|---|---|---|
| State | string | `.status.state` |
| Class | string | `.spec.class` |
| SKU | string | `.spec.sku` |
| Count | integer | `.spec.count` |
| Age | date | `.metadata.creationTimestamp` |

### Example (Guaranteed)

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

### Example (Preemptible)

```yaml
apiVersion: billing.opendatahub.io/v1alpha1
kind: Reservation
metadata:
  name: reservation-preemptible-sample
  namespace: platform-billing-operator-system
spec:
  tenantRef: acme-corp
  sku: h200-mig-3g.40gb
  count: 2
  geo: us-east
  window:
    start: "2026-09-01T00:00:00Z"
    end: "2026-09-07T23:59:59Z"
  class: Preemptible
  monthlyPrice: "2400.000000"
```
