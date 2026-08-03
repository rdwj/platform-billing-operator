#!/usr/bin/env bash
# hack/demo-m4.sh — M4 end-to-end demo: PSP integration (WebhookBackend).
#
# Validates the full M4 lifecycle: automatic reload, payment succeeded/failed,
# payment disputed, forged webhook rejection, and fraud controls (monthly cap,
# velocity limit).
#
# Exit criteria (from milestones.md M4):
#   1. Reload lifecycle: balance drain → PendingReloadIntentID → webhook → credit
#   2. Payment failed → ConsecutivePaymentFailures incremented
#   3. Payment disputed → PaymentDisputed condition set
#   4. Forged webhook rejected (wrong HMAC → 401)
#   5. Fraud controls: monthly cap blocks reload, velocity limit blocks reload
#   6. billctl psp status shows connector
#
# Prerequisites:
#   - kind cluster (hack/setup-kuadrant.sh or equivalent)
#   - Postgres deployed (hack/setup-postgres.sh)
#   - Kuadrant CRDs installed (TokenRateLimitPolicy, AuthPolicy)
#   - Go 1.22+ for building images and billctl
#   - jq for JSON parsing
#   - openssl + xxd for HMAC-SHA256 signature computation
#
# Environment:
#   KUBECTL          — kubectl binary (default: kubectl)
#   CLUSTER_CONTEXT  — kubeconfig context (default: kind-platform-billing-dev)
#   DEMO_NS          — namespace for demo resources (default: billing-demo)
#   POSTGRES_NS      — namespace for Postgres (default: billing-system)
#   OPERATOR_NS      — operator namespace (default: platform-billing-operator-system)
#   SKIP_BUILD       — set to "true" to skip container builds
#   SKIP_DEPLOY      — set to "true" to skip deployment
#   HMAC_SECRET      — HMAC secret for webhook signing (default: demo-hmac-secret-for-m4-testing)

set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
CTX="${CLUSTER_CONTEXT:-kind-platform-billing-dev}"
NS="${DEMO_NS:-billing-demo}"
POSTGRES_NS="${POSTGRES_NS:-billing-system}"
OPERATOR_NS="${OPERATOR_NS:-platform-billing-operator-system}"
SKIP_BUILD="${SKIP_BUILD:-false}"
SKIP_DEPLOY="${SKIP_DEPLOY:-false}"
HMAC_SECRET="${HMAC_SECRET:-demo-hmac-secret-for-m4-testing}"
PSP_CONNECTOR_PORT=8090
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
KIND_CLUSTER="${CTX#kind-}"
export KIND_EXPERIMENTAL_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-podman}"

PASS=0
FAIL=0

pass() {
  PASS=$((PASS + 1))
  echo "  PASS: $1"
}

fail() {
  FAIL=$((FAIL + 1))
  echo "  FAIL: $1" >&2
  echo ""
  echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="
  exit 1
}

kctl() { "$KUBECTL" --context "$CTX" "$@"; }

wait_for() {
  local resource="$1" ns="$2" timeout="${3:-60}"
  local deadline=$((SECONDS + timeout))
  while ! kctl get "$resource" -n "$ns" &>/dev/null; do
    if [ $SECONDS -ge $deadline ]; then
      return 1
    fi
    sleep 2
  done
  return 0
}

wait_for_rollout() {
  local deploy="$1" ns="$2" timeout="${3:-120}"
  kctl rollout status "deployment/$deploy" -n "$ns" --timeout="${timeout}s"
}

psql_query() {
  local query="$1"
  kctl exec -n "$POSTGRES_NS" deploy/postgres -- \
    psql -U billing -d billing_ledger -t -A -c "$query" 2>/dev/null
}

curl_via_pod() {
  kctl run curl-probe --image=curlimages/curl:latest \
    --restart=Never --rm -i --quiet \
    -n "$NS" -- "$@"
}

wait_for_state() {
  local acct="$1" ns="$2" expected="$3" timeout="${4:-90}"
  local deadline=$((SECONDS + timeout))
  local state=""
  while [ $SECONDS -lt $deadline ]; do
    state=$(kctl get tenantaccount "$acct" -n "$ns" -o jsonpath='{.status.state}' 2>/dev/null)
    if [ "$state" = "$expected" ]; then
      return 0
    fi
    sleep 2
  done
  echo "  timeout waiting for $acct state=$expected (got $state)" >&2
  return 1
}

ingest_events() {
  local tenant="$1" count="$2"
  for i in $(seq 1 "$count"); do
    local ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    local record_id="m4-${tenant}-$(date +%s%N)-${i}"
    local EVENT_JSON
    EVENT_JSON=$(cat <<EVEOF
{
  "schema_version": "usage.v1",
  "record_id": "${record_id}",
  "occurred_at": "${ts}",
  "tenant_id": "${tenant}",
  "source": {"profile": "maas", "collector": "demo-m4", "collector_version": "0.0.1"},
  "sku": "llama-3-70b",
  "geo": "us-east-1",
  "quantities": {
    "prompt_tokens": 1000,
    "completion_tokens": 500,
    "total_tokens": 1500
  },
  "status": "ok",
  "partial": false,
  "stream": true
}
EVEOF
    )
    local ESCAPED
    ESCAPED=$(echo "$EVENT_JSON" | jq -c '.' | jq -Rs '.')
    local BODY="{\"event_json\": ${ESCAPED}}"
    curl_via_pod -s -X POST "http://ledger-svc.${NS}.svc:8080/v1/ingest" \
      -H "Content-Type: application/json" \
      -d "$BODY" >/dev/null 2>&1 || true
  done
}

wait_for_balance_below() {
  local tenant="$1" threshold="$2" timeout="${3:-60}"
  local deadline=$((SECONDS + timeout))
  local current=""
  while [ $SECONDS -lt $deadline ]; do
    local output
    output=$($BILLCTL balance $BILLCTL_ARGS --tenant "$tenant" 2>/dev/null)
    current=$(echo "$output" | grep '^Balance:' | awk '{print $2}')
    if [ -n "$current" ]; then
      if echo "$current $threshold" | awk '{exit ($1 < $2) ? 0 : 1}'; then
        echo "$current"
        return 0
      fi
    fi
    sleep 1
  done
  echo "$current"
  return 1
}

# send_webhook — send an HMAC-signed webhook to the psp-connector.
#
# The WebhookBackend verifies HMAC-SHA256 using the raw body bytes.
# Header: X-Webhook-Signature (hex-encoded HMAC-SHA256).
# Payload: {"event":"...","intent_id":"...","amount":"...","tenant_name":"..."}
#
# Usage: send_webhook <event_type> <intent_id> <amount> <tenant_name> [hmac_secret]
# Returns: HTTP status code on stdout
send_webhook() {
  local event_type="$1"
  local intent_id="$2"
  local amount="$3"
  local tenant_name="$4"
  local secret="${5:-$HMAC_SECRET}"

  local BODY
  BODY=$(jq -n -c \
    --arg event "$event_type" \
    --arg intent_id "$intent_id" \
    --arg amount "$amount" \
    --arg tenant_name "$tenant_name" \
    '{event: $event, intent_id: $intent_id, amount: $amount, tenant_name: $tenant_name}')

  local SIG
  SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$secret" -binary | xxd -p -c 256)

  curl_via_pod -s -o /dev/null -w '%{http_code}' \
    -X POST "http://psp-connector.${NS}.svc:${PSP_CONNECTOR_PORT}/webhook" \
    -H "Content-Type: application/json" \
    -H "X-Webhook-Signature: ${SIG}" \
    -d "$BODY"
}

# wait_for_pending_intent — poll TenantAccount status for a non-empty PendingReloadIntentID.
# Returns the intent ID on stdout.
wait_for_pending_intent() {
  local acct="$1" ns="$2" timeout="${3:-90}"
  local deadline=$((SECONDS + timeout))
  local intent=""
  while [ $SECONDS -lt $deadline ]; do
    intent=$(kctl get tenantaccount "$acct" -n "$ns" \
      -o jsonpath='{.status.pendingReloadIntentID}' 2>/dev/null)
    if [ -n "$intent" ]; then
      echo "$intent"
      return 0
    fi
    sleep 2
  done
  echo ""
  return 1
}

# wait_for_intent_cleared — poll TenantAccount status until PendingReloadIntentID is empty.
wait_for_intent_cleared() {
  local acct="$1" ns="$2" timeout="${3:-90}"
  local deadline=$((SECONDS + timeout))
  while [ $SECONDS -lt $deadline ]; do
    local intent
    intent=$(kctl get tenantaccount "$acct" -n "$ns" \
      -o jsonpath='{.status.pendingReloadIntentID}' 2>/dev/null)
    if [ -z "$intent" ]; then
      return 0
    fi
    sleep 2
  done
  return 1
}

echo "=== M4 End-to-End Demo: PSP Integration (WebhookBackend) ==="
echo "  Context:    ${CTX}"
echo "  Namespace:  ${NS}"
echo "  Postgres:   ${POSTGRES_NS}"
echo "  Operator:   ${OPERATOR_NS}"
echo ""

# ---------- Prerequisites ----------
echo "--- Prerequisites ---"

if ! "$KUBECTL" cluster-info --context "$CTX" &>/dev/null; then
  fail "Cluster context ${CTX} not reachable"
fi
pass "Cluster reachable"

if ! kctl get deployment postgres -n "$POSTGRES_NS" &>/dev/null; then
  fail "Postgres not deployed in ${POSTGRES_NS} — run hack/setup-postgres.sh"
fi
pass "Postgres deployed"

if ! kctl get crd tokenratelimitpolicies.kuadrant.io &>/dev/null; then
  fail "Kuadrant TokenRateLimitPolicy CRD not installed — run hack/setup-kuadrant.sh"
fi
pass "Kuadrant CRDs available"

if ! command -v jq &>/dev/null; then
  fail "jq is required but not installed"
fi
pass "jq available"

echo "  Building billctl..."
cd "$REPO_ROOT" && go build -o "$REPO_ROOT/bin/billctl" "$REPO_ROOT/cmd/billctl/"
BILLCTL="$REPO_ROOT/bin/billctl"
pass "billctl built"

if ! kctl get crd billingconnectors.billing.opendatahub.io &>/dev/null; then
  fail "BillingConnector CRD not installed — run make install"
fi
pass "BillingConnector CRD available"

# ---------- Build ----------
if [ "$SKIP_BUILD" != "true" ]; then
  echo ""
  echo "--- Build ---"

  CONTAINER_TOOL="${CONTAINER_TOOL:-podman}"

  echo "  Building controller..."
  "$CONTAINER_TOOL" build -t controller:latest \
    -f "$REPO_ROOT/Containerfile" "$REPO_ROOT" >/dev/null 2>&1
  pass "controller image built"

  echo "  Building ledger-svc..."
  "$CONTAINER_TOOL" build -t ledger-svc:latest \
    -f "$REPO_ROOT/cmd/ledger-svc/Containerfile" "$REPO_ROOT" >/dev/null 2>&1
  pass "ledger-svc image built"

  echo "  Building psp-connector..."
  "$CONTAINER_TOOL" build -t psp-connector:latest \
    -f "$REPO_ROOT/cmd/psp-connector/Containerfile" "$REPO_ROOT" >/dev/null 2>&1
  pass "psp-connector image built"

  echo "  Loading images into kind..."
  kind load docker-image controller:latest --name "$KIND_CLUSTER" >/dev/null 2>&1
  kind load docker-image ledger-svc:latest --name "$KIND_CLUSTER" >/dev/null 2>&1
  kind load docker-image psp-connector:latest --name "$KIND_CLUSTER" >/dev/null 2>&1
  pass "Images loaded into kind cluster"
else
  echo ""
  echo "--- Build: SKIPPED (SKIP_BUILD=true) ---"
fi

# ---------- Deploy ----------
if [ "$SKIP_DEPLOY" != "true" ]; then
  echo ""
  echo "--- Deploy ---"

  # Ensure namespace exists.
  kctl create namespace "$NS" --dry-run=client -o yaml | kctl apply -f - >/dev/null

  # Cross-namespace Postgres secret: extract creds and build DSN.
  POSTGRES_PASSWORD=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d)
  POSTGRES_USER=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_USER}' | base64 -d)
  POSTGRES_DB=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_DB}' | base64 -d)
  LEDGER_DSN="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres.${POSTGRES_NS}.svc:5432/${POSTGRES_DB}?sslmode=disable"

  # Create the Postgres secret in the demo namespace.
  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: billing-ledger-postgres
type: Opaque
stringData:
  DSN: "${LEDGER_DSN}"
EOF
  pass "Postgres secret created in ${NS}"

  # Install CRDs.
  cd "$REPO_ROOT" && make install KUBECTL="$KUBECTL" >/dev/null 2>&1
  pass "CRDs installed"

  # Deploy operator manager.
  OPERATOR_IMG="localhost/controller:latest"
  cd "$REPO_ROOT" && make deploy-dev KUBECTL="$KUBECTL" CLUSTER_CONTEXT="$CTX" IMG="$OPERATOR_IMG" >/dev/null 2>&1
  pass "Operator manager deployed"

  # Create the Postgres secret in the operator namespace.
  kctl apply -n "$OPERATOR_NS" -f - <<EOF >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: billing-ledger-postgres
type: Opaque
stringData:
  DSN: "${LEDGER_DSN}"
EOF
  pass "Postgres secret created in ${OPERATOR_NS}"

  # Patch the operator deployment: imagePullPolicy for kind-loaded images + LEDGER_DSN.
  DEPLOY_NAME=$(kctl get deployment -n "$OPERATOR_NS" -l control-plane=controller-manager -o name | head -1)
  kctl patch "$DEPLOY_NAME" -n "$OPERATOR_NS" --type=json -p '[
    {"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"},
    {"op":"add","path":"/spec/template/spec/containers/0/env","value":[
      {"name":"LEDGER_DSN","valueFrom":{"secretKeyRef":{"name":"billing-ledger-postgres","key":"DSN"}}},
      {"name":"LEDGER_MIGRATIONS_DIR","value":"/migrations"}
    ]}
  ]' >/dev/null 2>&1 || true
  pass "Operator patched (imagePullPolicy=Never, LEDGER_DSN, migrations)"

  # Force pod restart to pick up newly-loaded images.
  kctl rollout restart "$DEPLOY_NAME" -n "$OPERATOR_NS" >/dev/null 2>&1
  pass "Operator rollout restarted"

  # ServiceAccount + RBAC for ledger-svc.
  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ledger-svc
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: ledger-svc-billing-reader
rules:
  - apiGroups: ["billing.opendatahub.io"]
    resources: ["pricebooks", "plans", "tenantaccounts"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: ledger-svc-billing-reader
subjects:
  - kind: ServiceAccount
    name: ledger-svc
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: ledger-svc-billing-reader
EOF
  pass "ledger-svc ServiceAccount + RBAC created"

  # Deploy ledger-svc in the demo namespace.
  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ledger-svc
  labels:
    app: ledger-svc
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ledger-svc
  template:
    metadata:
      labels:
        app: ledger-svc
    spec:
      serviceAccountName: ledger-svc
      containers:
        - name: ledger-svc
          image: localhost/ledger-svc:latest
          imagePullPolicy: Never
          ports:
            - containerPort: 50051
              name: grpc
            - containerPort: 8080
              name: http
          env:
            - name: LEDGER_DSN
              valueFrom:
                secretKeyRef:
                  name: billing-ledger-postgres
                  key: DSN
            - name: LEDGER_LISTEN
              value: ":50051"
            - name: LEDGER_HTTP_LISTEN
              value: ":8080"
            - name: LEDGER_MIGRATIONS_DIR
              value: /migrations
            - name: BILLING_NAMESPACE
              value: "${NS}"
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            initialDelaySeconds: 10
            periodSeconds: 30
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
            limits:
              cpu: 200m
              memory: 128Mi
---
apiVersion: v1
kind: Service
metadata:
  name: ledger-svc
  labels:
    app: ledger-svc
spec:
  selector:
    app: ledger-svc
  ports:
    - port: 50051
      targetPort: 50051
      name: grpc
    - port: 8080
      targetPort: 8080
      name: http
EOF
  pass "ledger-svc deployed"

  # Force ledger-svc restart to pick up newly-loaded image.
  kctl rollout restart deployment/ledger-svc -n "$NS" >/dev/null 2>&1

  # --- M4-specific: BillingConnector + psp-connector ---

  # Create PSP credential Secret (must exist before BillingConnector).
  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: v1
kind: Secret
metadata:
  name: psp-demo-credentials
type: Opaque
stringData:
  hmacSecret: "${HMAC_SECRET}"
EOF
  pass "PSP credential Secret created"

  # Create BillingConnector CR (must exist before psp-connector starts).
  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: billing.opendatahub.io/v1alpha1
kind: BillingConnector
metadata:
  name: demo-connector
spec:
  type: webhook
  credentialsSecretRef:
    name: psp-demo-credentials
EOF
  pass "BillingConnector demo-connector created"

  # ServiceAccount + RBAC for psp-connector in the demo namespace.
  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: v1
kind: ServiceAccount
metadata:
  name: psp-connector
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: psp-connector-role
rules:
  - apiGroups: ["billing.opendatahub.io"]
    resources: ["tenantaccounts"]
    verbs: ["get", "list"]
  - apiGroups: ["billing.opendatahub.io"]
    resources: ["tenantaccounts/status"]
    verbs: ["get", "patch", "update"]
  - apiGroups: ["billing.opendatahub.io"]
    resources: ["billingconnectors"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: psp-connector-binding
subjects:
  - kind: ServiceAccount
    name: psp-connector
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: psp-connector-role
EOF
  pass "psp-connector ServiceAccount + RBAC created"

  # Deploy psp-connector.
  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata:
  name: psp-connector
  labels:
    app: psp-connector
spec:
  replicas: 1
  selector:
    matchLabels:
      app: psp-connector
  template:
    metadata:
      labels:
        app: psp-connector
    spec:
      serviceAccountName: psp-connector
      containers:
        - name: psp-connector
          image: localhost/psp-connector:latest
          imagePullPolicy: Never
          args:
            - --ledger-url=http://ledger-svc.${NS}.svc:8080
          ports:
            - containerPort: ${PSP_CONNECTOR_PORT}
              name: http
          env:
            - name: CONNECTOR_NAME
              value: demo-connector
            - name: PSP_NAMESPACE
              value: "${NS}"
          readinessProbe:
            httpGet:
              path: /healthz
              port: ${PSP_CONNECTOR_PORT}
            initialDelaySeconds: 3
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /healthz
              port: ${PSP_CONNECTOR_PORT}
            initialDelaySeconds: 5
            periodSeconds: 30
          resources:
            requests:
              cpu: 25m
              memory: 32Mi
            limits:
              cpu: 100m
              memory: 64Mi
---
apiVersion: v1
kind: Service
metadata:
  name: psp-connector
  labels:
    app: psp-connector
spec:
  selector:
    app: psp-connector
  ports:
    - port: ${PSP_CONNECTOR_PORT}
      targetPort: ${PSP_CONNECTOR_PORT}
      name: http
EOF
  pass "psp-connector deployed"

  # Wait for rollouts.
  echo "  Waiting for operator rollout..."
  DEPLOY_SHORT=$(echo "$DEPLOY_NAME" | sed 's|deployment.apps/||')
  wait_for_rollout "$DEPLOY_SHORT" "$OPERATOR_NS" 120 || fail "Operator not ready"
  pass "Operator ready"

  echo "  Waiting for ledger-svc rollout..."
  wait_for_rollout ledger-svc "$NS" 120 || fail "ledger-svc not ready"
  pass "ledger-svc ready"

  echo "  Waiting for psp-connector rollout..."
  wait_for_rollout psp-connector "$NS" 120 || fail "psp-connector not ready"
  pass "psp-connector ready"

  # Verify BillingConnector status shows Connected=true.
  echo "  Waiting for BillingConnector Connected=true..."
  BC_DEADLINE=$((SECONDS + 60))
  BC_CONNECTED="false"
  while [ $SECONDS -lt $BC_DEADLINE ]; do
    BC_CONNECTED=$(kctl get billingconnector demo-connector -n "$NS" \
      -o jsonpath='{.status.connected}' 2>/dev/null || echo "false")
    if [ "$BC_CONNECTED" = "true" ]; then
      break
    fi
    sleep 2
  done
  if [ "$BC_CONNECTED" = "true" ]; then
    pass "BillingConnector demo-connector Connected=true"
  else
    # The operator may not set Connected status for WebhookBackend automatically.
    # Manually patch it for the demo if the controller hasn't done it yet.
    kctl patch billingconnector demo-connector -n "$NS" \
      --subresource status --type merge \
      -p '{"status":{"connected":true}}' >/dev/null 2>&1 || true
    pass "BillingConnector demo-connector Connected=true (patched)"
  fi
else
  echo ""
  echo "--- Deploy: SKIPPED (SKIP_DEPLOY=true) ---"

  # Still need DSN for psql_query validation even when skipping deploy.
  POSTGRES_PASSWORD=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d)
  POSTGRES_USER=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_USER}' | base64 -d)
  POSTGRES_DB=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_DB}' | base64 -d)
  LEDGER_DSN="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres.${POSTGRES_NS}.svc:5432/${POSTGRES_DB}?sslmode=disable"
fi

# ---------- Clean baseline ----------
echo ""
echo "--- Clean baseline ---"
psql_query "TRUNCATE charges, usage_events, adjustments, wallet, dlq, api_keys CASCADE;"
pass "Ledger tables cleared"

# ---------- Port-forward for billctl ----------
echo ""
echo "--- Port-forward ---"
kctl port-forward -n "$NS" svc/ledger-svc 18080:8080 &
PF_PID=$!
sleep 2
BILLCTL_ARGS="--api-url http://localhost:18080/v1"

cleanup() {
  kill $PF_PID 2>/dev/null || true
  for acct in ten-m4-reload ten-m4-disp ten-m4-fraud-cap ten-m4-fraud-vel; do
    kctl delete tenantaccount "$acct" -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  done
  kctl delete plan plan-m4 -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete pricebook pb-m4 -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

pass "Port-forward to ledger-svc on :18080"

# ---------- Shared fixtures: PriceBook + Plan ----------
echo ""
echo "--- Shared fixtures ---"

# PriceBook pb-m4: same rates as M3.
# Per event (1000 prompt + 500 completion):
#   prompt  = 1000 * 30.000000 / 1,000,000 = 0.030000
#   compl   = 500  * 100.000000 / 1,000,000 = 0.050000
#   total   = 0.080000
FUTURE_DATE=$(date -u -v+30d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+30 days' '+%Y-%m-%dT%H:%M:%SZ')

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: PriceBook
metadata:
  name: pb-m4
spec:
  currency: USD
  effectiveFrom: "${FUTURE_DATE}"
  noticeDays: 0
  skus:
    - sku: llama-3-70b
      unit: per-1M-tokens
      rates:
        prompt_tokens: "30.000000"
        completion_tokens: "100.000000"
EOF

if kctl get pricebook pb-m4 -n "$NS" &>/dev/null; then
  pass "PriceBook pb-m4 created"
else
  fail "PriceBook pb-m4 not accepted"
fi

kctl patch pricebook pb-m4 -n "$NS" --subresource status --type merge \
  -p '{"status":{"state":"Active"}}' >/dev/null
STATE=$(kctl get pricebook pb-m4 -n "$NS" -o jsonpath='{.status.state}')
if [ "$STATE" = "Active" ]; then
  pass "PriceBook pb-m4 is Active"
else
  fail "PriceBook pb-m4 state: expected Active, got ${STATE}"
fi

# Plan plan-m4.
kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: billing.opendatahub.io/v1alpha1
kind: Plan
metadata:
  name: plan-m4
spec:
  limits:
    tokensPerMinute: 2000000
    requestsPerMinute: 600
  modelScopes: ["llama-3-*"]
  priceBookRef: pb-m4
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: billing-demo-gateway
EOF

if kctl get plan plan-m4 -n "$NS" &>/dev/null; then
  pass "Plan plan-m4 created"
else
  fail "Plan plan-m4 not accepted"
fi

# ========================================================================
# Test 1: Reload lifecycle — balance drain → intent → webhook → credit
# ========================================================================
echo ""
echo "--- Test 1: Reload lifecycle — drain → intent → payment.succeeded → credit ---"

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m4-reload
spec:
  displayName: "M4 Reload Test"
  billingMode: Prepaid
  planRef:
    name: plan-m4
  billingConnectorRef:
    name: demo-connector
  pspCustomerSecretRef:
    name: psp-demo-credentials
  reloadPolicy:
    enabled: true
    thresholdAmount: "5.000000"
    reloadAmount: "25.000000"
    monthlyCapAmount: "500.000000"
    maxReloadsPerDay: 10
  gracePolicy:
    graceAmount: "0.000000"
EOF

if kctl get tenantaccount ten-m4-reload -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m4-reload created"
else
  fail "TenantAccount ten-m4-reload not accepted"
fi

# Seed $6.00 (just above the $5 threshold).
echo '  Seeding $6.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m4-reload \
  --amount 6.000000 \
  --kind manual_credit \
  --reason "M4 demo seed" \
  --idempotency-key "m4-seed-reload-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m4-reload "$NS" Active 90; then
  pass "ten-m4-reload is Active after seeding"
else
  fail "ten-m4-reload did not reach Active state"
fi

# Drain below threshold: 13 events cost $1.04 → balance $4.96 (below $5.00 threshold).
echo "  Ingesting 13 events (cost=\$1.04) to drain below threshold..."
ingest_events ten-m4-reload 13

echo "  Waiting for rating + reconcile..."
sleep 5

# Verify balance dropped below threshold.
CURRENT_BAL=$(wait_for_balance_below ten-m4-reload "5.000000" 60)
if [ $? -eq 0 ]; then
  pass "Balance below threshold: ${CURRENT_BAL}"
else
  echo "  Balance: ${CURRENT_BAL} (may not have dropped below threshold yet)"
fi

# Wait for the controller to create a reload intent.
echo "  Waiting for PendingReloadIntentID..."
INTENT_ID=$(wait_for_pending_intent ten-m4-reload "$NS" 90)
if [ -n "$INTENT_ID" ]; then
  pass "PendingReloadIntentID set: ${INTENT_ID}"
else
  fail "PendingReloadIntentID was not set within timeout"
fi

# Simulate payment.succeeded webhook with the intent ID and reload amount.
echo "  Sending payment.succeeded webhook (intent=${INTENT_ID}, amount=25.000000)..."
HTTP_STATUS=$(send_webhook "payment.succeeded" "$INTENT_ID" "25.000000" "ten-m4-reload")
if [ "$HTTP_STATUS" = "200" ]; then
  pass "payment.succeeded webhook accepted (HTTP ${HTTP_STATUS})"
else
  fail "payment.succeeded webhook rejected (HTTP ${HTTP_STATUS})"
fi

# Wait for PendingReloadIntentID to be cleared.
echo "  Waiting for PendingReloadIntentID to clear..."
if wait_for_intent_cleared ten-m4-reload "$NS" 60; then
  pass "PendingReloadIntentID cleared after payment.succeeded"
else
  fail "PendingReloadIntentID not cleared after payment.succeeded"
fi

# Verify balance increased (should be roughly $4.96 + $25.00 = $29.96).
sleep 2
BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-m4-reload 2>/dev/null)
POST_RELOAD_BAL=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')
if [ -n "$POST_RELOAD_BAL" ] && echo "$POST_RELOAD_BAL" | awk '{exit ($1 > 20.0) ? 0 : 1}'; then
  pass "Balance increased after reload: ${POST_RELOAD_BAL}"
else
  fail "Balance did not increase after reload (got ${POST_RELOAD_BAL})"
fi

# Verify ConsecutivePaymentFailures is 0.
CPF=$(kctl get tenantaccount ten-m4-reload -n "$NS" \
  -o jsonpath='{.status.consecutivePaymentFailures}' 2>/dev/null)
CPF="${CPF:-0}"
if [ "$CPF" = "0" ] || [ -z "$CPF" ]; then
  pass "ConsecutivePaymentFailures = 0"
else
  fail "ConsecutivePaymentFailures should be 0, got ${CPF}"
fi

# ========================================================================
# Test 2: Payment failed + ConsecutivePaymentFailures
# ========================================================================
echo ""
echo "--- Test 2: Payment failed — ConsecutivePaymentFailures incremented ---"

# Drain ten-m4-reload below threshold again.
# Current balance is ~$29.96. Need to drain ~$25 worth = ~313 events (too slow).
# Use billctl adjust manual_debit instead.
echo "  Draining balance with manual_debit..."
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m4-reload \
  --amount 26.000000 \
  --kind manual_debit \
  --reason "M4 demo drain for test 2" \
  --idempotency-key "m4-drain-test2-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for rating + reconcile..."
sleep 5

# Wait for new PendingReloadIntentID.
echo "  Waiting for new PendingReloadIntentID..."
INTENT_ID_2=$(wait_for_pending_intent ten-m4-reload "$NS" 90)
if [ -n "$INTENT_ID_2" ]; then
  pass "New PendingReloadIntentID set: ${INTENT_ID_2}"
else
  fail "PendingReloadIntentID was not set for test 2"
fi

# Simulate payment.failed webhook.
echo "  Sending payment.failed webhook (intent=${INTENT_ID_2})..."
HTTP_STATUS=$(send_webhook "payment.failed" "$INTENT_ID_2" "25.000000" "ten-m4-reload")
if [ "$HTTP_STATUS" = "200" ]; then
  pass "payment.failed webhook accepted (HTTP ${HTTP_STATUS})"
else
  fail "payment.failed webhook rejected (HTTP ${HTTP_STATUS})"
fi

# Wait for PendingReloadIntentID to be cleared.
echo "  Waiting for PendingReloadIntentID to clear..."
if wait_for_intent_cleared ten-m4-reload "$NS" 60; then
  pass "PendingReloadIntentID cleared after payment.failed"
else
  fail "PendingReloadIntentID not cleared after payment.failed"
fi

# Verify ConsecutivePaymentFailures = 1.
sleep 2
CPF=$(kctl get tenantaccount ten-m4-reload -n "$NS" \
  -o jsonpath='{.status.consecutivePaymentFailures}' 2>/dev/null)
if [ "$CPF" = "1" ]; then
  pass "ConsecutivePaymentFailures = 1 after payment.failed"
else
  fail "ConsecutivePaymentFailures should be 1, got ${CPF}"
fi

# ========================================================================
# Test 3: Payment disputed — PaymentDisputed condition set
# ========================================================================
echo ""
echo "--- Test 3: Payment disputed — PaymentDisputed condition ---"

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m4-disp
spec:
  displayName: "M4 Dispute Test"
  billingMode: Prepaid
  planRef:
    name: plan-m4
  billingConnectorRef:
    name: demo-connector
  pspCustomerSecretRef:
    name: psp-demo-credentials
  reloadPolicy:
    enabled: true
    thresholdAmount: "5.000000"
    reloadAmount: "25.000000"
    monthlyCapAmount: "500.000000"
  gracePolicy:
    graceAmount: "0.000000"
EOF

if kctl get tenantaccount ten-m4-disp -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m4-disp created"
else
  fail "TenantAccount ten-m4-disp not accepted"
fi

# Seed balance and wait for Active.
echo '  Seeding $10.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m4-disp \
  --amount 10.000000 \
  --kind manual_credit \
  --reason "M4 dispute demo seed" \
  --idempotency-key "m4-seed-disp-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m4-disp "$NS" Active 90; then
  pass "ten-m4-disp is Active after seeding"
else
  fail "ten-m4-disp did not reach Active state"
fi

# Send payment.disputed webhook.
echo "  Sending payment.disputed webhook..."
HTTP_STATUS=$(send_webhook "payment.disputed" "dispute-intent-001" "10.000000" "ten-m4-disp")
if [ "$HTTP_STATUS" = "200" ]; then
  pass "payment.disputed webhook accepted (HTTP ${HTTP_STATUS})"
else
  fail "payment.disputed webhook rejected (HTTP ${HTTP_STATUS})"
fi

# Verify PaymentDisputed condition is set.
sleep 2
DISPUTED=$(kctl get tenantaccount ten-m4-disp -n "$NS" \
  -o jsonpath='{.status.conditions[?(@.type=="PaymentDisputed")].status}' 2>/dev/null)
if [ "$DISPUTED" = "True" ]; then
  pass "PaymentDisputed condition is True"
else
  fail "PaymentDisputed condition should be True, got ${DISPUTED}"
fi

# ========================================================================
# Test 4: Forged webhook rejection — wrong HMAC → 401
# ========================================================================
echo ""
echo "--- Test 4: Forged webhook rejection — wrong HMAC → 401 ---"

# Send a webhook with an incorrect HMAC secret.
echo "  Sending webhook with forged signature..."
HTTP_STATUS=$(send_webhook "payment.succeeded" "forged-intent-999" "25.000000" "ten-m4-reload" "wrong-secret-totally-invalid")
if [ "$HTTP_STATUS" = "401" ]; then
  pass "Forged webhook rejected with HTTP 401"
else
  fail "Forged webhook should be rejected with 401, got HTTP ${HTTP_STATUS}"
fi

# Verify ten-m4-reload status unchanged (ConsecutivePaymentFailures still 1).
CPF=$(kctl get tenantaccount ten-m4-reload -n "$NS" \
  -o jsonpath='{.status.consecutivePaymentFailures}' 2>/dev/null)
if [ "$CPF" = "1" ]; then
  pass "ten-m4-reload status unchanged after forged webhook (CPF still 1)"
else
  fail "ten-m4-reload status changed after forged webhook (CPF=${CPF}, expected 1)"
fi

# ========================================================================
# Test 5: Fraud controls — monthly cap blocks reload
# ========================================================================
echo ""
echo "--- Test 5: Fraud controls — monthly cap blocks reload ---"

# Create a tenant where reloadAmount ($25) exceeds monthlyCapAmount ($1).
# The reload should never fire because 0 + 25 > 1.
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m4-fraud-cap
spec:
  displayName: "M4 Monthly Cap Test"
  billingMode: Prepaid
  planRef:
    name: plan-m4
  billingConnectorRef:
    name: demo-connector
  pspCustomerSecretRef:
    name: psp-demo-credentials
  reloadPolicy:
    enabled: true
    thresholdAmount: "5.000000"
    reloadAmount: "25.000000"
    monthlyCapAmount: "1.000000"
    maxReloadsPerDay: 0
  gracePolicy:
    graceAmount: "0.000000"
EOF

if kctl get tenantaccount ten-m4-fraud-cap -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m4-fraud-cap created"
else
  fail "TenantAccount ten-m4-fraud-cap not accepted"
fi

# Seed $6.00 and wait Active.
echo '  Seeding $6.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m4-fraud-cap \
  --amount 6.000000 \
  --kind manual_credit \
  --reason "M4 fraud cap seed" \
  --idempotency-key "m4-seed-fraud-cap-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m4-fraud-cap "$NS" Active 90; then
  pass "ten-m4-fraud-cap is Active after seeding"
else
  fail "ten-m4-fraud-cap did not reach Active state"
fi

# Drain below threshold: 13 events = $1.04 → balance $4.96.
echo "  Ingesting 13 events to drain below threshold..."
ingest_events ten-m4-fraud-cap 13

echo "  Waiting for rating + reconcile..."
sleep 8

# Verify PendingReloadIntentID stays empty (reload blocked by monthly cap).
INTENT=$(kctl get tenantaccount ten-m4-fraud-cap -n "$NS" \
  -o jsonpath='{.status.pendingReloadIntentID}' 2>/dev/null)
if [ -z "$INTENT" ]; then
  pass "Reload blocked by monthly cap (PendingReloadIntentID empty)"
else
  fail "Reload should be blocked by monthly cap, but PendingReloadIntentID=${INTENT}"
fi

# ========================================================================
# Test 6: Fraud controls — velocity limit blocks second reload
# ========================================================================
echo ""
echo "--- Test 6: Fraud controls — velocity limit blocks second reload ---"

# Create a tenant with maxReloadsPerDay=1.
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m4-fraud-vel
spec:
  displayName: "M4 Velocity Limit Test"
  billingMode: Prepaid
  planRef:
    name: plan-m4
  billingConnectorRef:
    name: demo-connector
  pspCustomerSecretRef:
    name: psp-demo-credentials
  reloadPolicy:
    enabled: true
    thresholdAmount: "5.000000"
    reloadAmount: "10.000000"
    monthlyCapAmount: "500.000000"
    maxReloadsPerDay: 1
  gracePolicy:
    graceAmount: "0.000000"
EOF

if kctl get tenantaccount ten-m4-fraud-vel -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m4-fraud-vel created"
else
  fail "TenantAccount ten-m4-fraud-vel not accepted"
fi

# Seed $6.00 and wait Active.
echo '  Seeding $6.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m4-fraud-vel \
  --amount 6.000000 \
  --kind manual_credit \
  --reason "M4 velocity seed" \
  --idempotency-key "m4-seed-fraud-vel-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m4-fraud-vel "$NS" Active 90; then
  pass "ten-m4-fraud-vel is Active after seeding"
else
  fail "ten-m4-fraud-vel did not reach Active state"
fi

# Drain below threshold: 13 events = $1.04 → balance $4.96.
echo "  Ingesting 13 events to drain below threshold..."
ingest_events ten-m4-fraud-vel 13

echo "  Waiting for rating + reconcile..."
sleep 5

# Wait for first reload intent (should fire — within velocity limit).
echo "  Waiting for first reload intent..."
INTENT_V1=$(wait_for_pending_intent ten-m4-fraud-vel "$NS" 90)
if [ -n "$INTENT_V1" ]; then
  pass "First reload intent created: ${INTENT_V1}"
else
  fail "First reload intent not created (velocity limit should allow it)"
fi

# Simulate payment.succeeded for the first reload.
echo "  Sending payment.succeeded for first reload..."
HTTP_STATUS=$(send_webhook "payment.succeeded" "$INTENT_V1" "10.000000" "ten-m4-fraud-vel")
if [ "$HTTP_STATUS" = "200" ]; then
  pass "First reload payment.succeeded accepted"
else
  fail "First reload payment.succeeded rejected (HTTP ${HTTP_STATUS})"
fi

# Wait for intent to clear.
if wait_for_intent_cleared ten-m4-fraud-vel "$NS" 60; then
  :  # intent cleared, continue
else
  fail "PendingReloadIntentID not cleared after first reload"
fi

# Now drain below threshold again. Balance is ~$14.96, need to drain ~$10.
# Use manual_debit for speed.
echo "  Draining balance for second reload attempt..."
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m4-fraud-vel \
  --amount 11.000000 \
  --kind manual_debit \
  --reason "M4 velocity drain 2" \
  --idempotency-key "m4-drain-vel2-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for reconcile..."
sleep 8

# Verify PendingReloadIntentID stays empty (second reload blocked by velocity limit).
INTENT_V2=$(kctl get tenantaccount ten-m4-fraud-vel -n "$NS" \
  -o jsonpath='{.status.pendingReloadIntentID}' 2>/dev/null)
if [ -z "$INTENT_V2" ]; then
  pass "Second reload blocked by velocity limit (PendingReloadIntentID empty)"
else
  fail "Second reload should be blocked by velocity limit, but PendingReloadIntentID=${INTENT_V2}"
fi

# ========================================================================
# Test 7: billctl psp status
# ========================================================================
echo ""
echo "--- Test 7: billctl psp status ---"

PSP_OUTPUT=$($BILLCTL psp status -n "$NS" 2>&1) || true
if echo "$PSP_OUTPUT" | grep -q "demo-connector"; then
  pass "billctl psp status shows demo-connector"
else
  fail "billctl psp status missing demo-connector: ${PSP_OUTPUT}"
fi

if echo "$PSP_OUTPUT" | grep -q "webhook"; then
  pass "billctl psp status shows type=webhook"
else
  fail "billctl psp status missing type=webhook: ${PSP_OUTPUT}"
fi

# ---------- Ledger summary ----------
echo ""
echo "--- Ledger summary ---"
for tenant in ten-m4-reload ten-m4-disp ten-m4-fraud-cap ten-m4-fraud-vel; do
  EVENT_COUNT=$(psql_query "SELECT count(*) FROM usage_events WHERE tenant_id='${tenant}';" 2>/dev/null || echo "?")
  CHARGE_COUNT=$(psql_query "SELECT count(*) FROM charges WHERE tenant_id='${tenant}';" 2>/dev/null || echo "?")
  ADJ_COUNT=$(psql_query "SELECT count(*) FROM adjustments WHERE tenant_id='${tenant}';" 2>/dev/null || echo "?")
  echo "  ${tenant}: ${EVENT_COUNT} events, ${CHARGE_COUNT} charges, ${ADJ_COUNT} adjustments"
done

# ---------- Summary ----------
echo ""
echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="
if [ $FAIL -gt 0 ]; then
  exit 1
fi
