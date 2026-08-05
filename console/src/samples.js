export function planYaml(sel) {
  return `apiVersion: kuadrant.io/v1alpha1
kind: TokenRateLimitPolicy
metadata:
  name: plan-${sel.slug}
  namespace: maas-gateway
spec:
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: maas-default-gateway
  limits:
    ${sel.slug}-tpm:
      rates:
        - limit: ${sel.tpmN}        # tokens / minute
          window: 1m
      when:
        - predicate: auth.identity.tier == "${sel.slug}"
      counters:
        - expression: auth.identity.tenant_id
    ${sel.slug}-daily:
      rates:
        - limit: ${sel.dayN}   # tokens / day
          window: 24h
      when:
        - predicate: auth.identity.tier == "${sel.slug}"
      counters:
        - expression: auth.identity.tenant_id
---
# Companion CRs generated with this plan:
#  RateLimitPolicy   — ${sel.rpm} req/min, concurrency ${sel.conc}
#  AuthPolicy        — API key → {tier, tenant_id,
#                      allowed_models, geo_scope}
#  BalanceAllowance  — rolling 5m token grant from
#                      prepaid balance (custom controller)`;
}

export const jsonUsage = `"usage": {
  "prompt_tokens": 1841,
  "completion_tokens": 512,
  "total_tokens": 2353,
  "prompt_tokens_details":     { "cached_tokens": 1024 },
  "completion_tokens_details": { "reasoning_tokens": 128 }
}`;

export const jsonEvent = `POST /internal/v1/usage-events
{
  "request_id": "req_9f3a2c",        // idempotency key
  "tenant_id":  "ten_helix",
  "key_id":     "key_prod_rag",
  "model":      "llama-4-maverick-fp8",
  "geo":        "us-east-1",
  "prompt_tokens":     1841,
  "completion_tokens": 512,
  "reasoning_tokens":  128,
  "cached_tokens":     1024,
  "status": "ok",     // ok | client_disconnect | error
  "stream": true,
  "latency_ms": 2140,
  "price_version": "v2",
  "ts": "2026-07-28T09:41:22Z"
}`;

export const jsonWebhook = `{
  "type": "balance.low",
  "tenant_id": "ten_osaka",
  "balance": 942.10,
  "threshold": 1000.00,
  "auto_reload": { "state": "retrying", "attempt": 2 },
  "ts": "2026-07-28T06:00:04Z"
}`;
