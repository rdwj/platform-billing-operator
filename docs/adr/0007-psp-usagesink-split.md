# ADR-0007: PSPBackend / UsageSinkBackend Interface Split

## Status

Accepted (2026-08-06)

## Context

The operator already has a `PSPBackend` interface for payment service providers (Stripe, generic webhook). PSPBackend handles payment operations: creating payment intents, verifying webhook signatures, and querying payment status.

We need to integrate with external metering and usage platforms so operators can forward billing-grade usage events to systems like OpenMeter, Amberflo, Orb, or Lago. These platforms serve a fundamentally different purpose than PSPs: they aggregate and query usage data, not process payments.

Research into two candidate platforms informed this design:

- **OpenMeter**: CloudEvents-based ingestion API, Go SDK, self-hosted option, full OpenAPI spec. Selected as the first implementation target.
- **Monetize360**: No public billing API, enterprise-sales-only, wants to own the billing platform rather than receive events. Deprioritized indefinitely. Revisit if they publish API documentation or a partnership provides access.

The question is whether to extend PSPBackend to cover usage sinks, or introduce a separate interface.

## Decision

### 1. Two separate interfaces

`PSPBackend` and `UsageSinkBackend` have zero method overlap:

| PSPBackend | UsageSinkBackend |
|---|---|
| CreatePaymentIntent | IngestEvents |
| VerifyWebhookSignature | HealthCheck |
| GetPaymentStatus | ConfigureMeters |
| | SyncSubject |
| | QueryUsage |

A unified interface would force every implementation to stub half its methods. Two focused interfaces keep each implementation testable and the dispatch logic clean.

Each interface has its own registry: `BackendRegistry` for PSP, `SinkRegistry` for usage sinks.

### 2. Dispatch model

The `BillingConnector` CR's `spec.type` field routes to exactly one interface category:

- `stripe`, `webhook` -> `PSPBackend` (registered in `BackendRegistry`)
- `openmeter` (and future metering platforms) -> `UsageSinkBackend` (registered in `SinkRegistry`)

One type per CR. A tenant needing both Stripe for payments and OpenMeter for metering creates two separate `BillingConnector` CRs. This keeps the reconciler straightforward and avoids ambiguity about which operations a given connector supports.

### 3. CRD extension

`BillingConnectorSpec` gains:

- `ConnectorTypeOpenMeter = "openmeter"` constant
- `UsageSink *UsageSinkConfig` field (parallel to the existing `WebhookIngress` field)
- `UsageSinkConfig` struct with: endpoint, batchSize, flushInterval, meters
- `MeterConfig` struct for auto-configuring meters on reconcile

The `credentialsSecretRef` field is shared across all types. For OpenMeter, it expects an `apiKey` key (may be empty for self-hosted instances without authentication).

### 4. Open question resolutions

**Event filtering:** All events are forwarded to every registered usage sink. No per-sink filtering in Phase 3. If a real use case for filtering surfaces, it can be added to `UsageSinkConfig` later without interface changes.

**Retry and dead-letter queue:** Individual backends are pure transformers (map internal events to platform API calls). Retry logic, backoff, and dead-letter queue handling belong in a shared dispatcher layer that wraps the backends. This keeps backend implementations simple and testable. The dispatcher is Phase 5 scope.

**Reconciliation flow:** `QueryUsage` enables cross-checking our ledger against the external platform. The initial implementation will be manual, invoked via `billctl reconcile`. Automatic periodic reconciliation is deferred.

### 5. Monetize360 is a different integration category

Monetize360's RevOS has a documented billing API (accounts, contracts, services, payments
via slug-based workflow execution). However, it does not accept raw usage events. RevOS is
a billing system of record, not a metering sink.

The natural integration pattern is entity synchronization: our operator projects its
accounts, services, and payments into RevOS so commercial operations teams can use
Monetize360's UI for billing workflows and revenue reporting. This would require a third
interface category (working name: `BillingPlatformBackend`) with methods like SyncAccount,
SyncService, RecordPayment.

This is lower priority than OpenMeter (gated on enterprise tenant access) and would need
its own ADR when prioritized. The key point for this ADR: RevOS does not change the
PSPBackend/UsageSinkBackend split. It validates the decision to keep interfaces
category-specific rather than building one fat unified interface, since we now have
evidence of three distinct integration categories.

## Consequences

**Positive:**

- Clean separation of concerns between payment and metering integrations
- Each backend implementation is focused and independently testable
- Adding new metering platforms requires only a new `UsageSinkBackend` implementation and a `ConnectorType` constant
- The CRD extension follows the established pattern (`UsageSink` parallels `WebhookIngress`)

**Negative:**

- Two registries to manage in the reconciler (deletion must clean up both); a third category (BillingPlatformBackend for Monetize360-style integrations) would add a third
- If a future PSP offers both payment and metering APIs (e.g., Stripe Billing has usage records), that platform would need two separate BillingConnector CRs rather than one unified connector
- The dispatcher layer (retry, batching, DLQ) still needs to be built in Phase 5
