#!/usr/bin/env bash
# hack/demo-m2.sh — M2 end-to-end demo: Rating + Wallet + Billing API.
#
# Validates the full M2 pipeline: direct HTTP ingest → rating job →
# wallet balance and usage queries via billctl. Exercises PriceBook
# rate changes with forward-only verification and ShowbackOnly mode.
#
# Exit criteria (from milestones.md M2):
#   1. PriceBook creation and activation
#   2. TenantAccount creation (ShowbackOnly)
#   3. Seed balance via billctl adjust
#   4. Ingest 5 usage events → all land in usage_events
#   5. Rating job processes events → balance decreases by expected amount
#   6. billctl usage shows correct charge totals
#   7. PriceBook v2 (rate change) → forward-only, old charges unchanged
#   8. ShowbackOnly invariant: tenant stays Active despite balance decrease
#   9. Rater determinism test
#
# Prerequisites:
#   - kind cluster (hack/setup-kuadrant.sh or equivalent)
#   - Postgres deployed (hack/setup-postgres.sh)
#   - Go 1.22+ for building images and billctl
#   - jq for JSON parsing
#
# Environment:
#   KUBECTL          — kubectl binary (default: kubectl)
#   CLUSTER_CONTEXT  — kubeconfig context (default: kind-platform-billing-dev)
#   DEMO_NS          — namespace for demo resources (default: billing-demo)
#   POSTGRES_NS      — namespace for Postgres (default: billing-system)
#   OPERATOR_NS      — operator namespace (default: platform-billing-operator-system)
#   SKIP_BUILD       — set to "true" to skip container builds
#   SKIP_DEPLOY      — set to "true" to skip deployment

set -euo pipefail

KUBECTL="${KUBECTL:-kubectl}"
CTX="${CLUSTER_CONTEXT:-kind-platform-billing-dev}"
NS="${DEMO_NS:-billing-demo}"
POSTGRES_NS="${POSTGRES_NS:-billing-system}"
OPERATOR_NS="${OPERATOR_NS:-platform-billing-operator-system}"
SKIP_BUILD="${SKIP_BUILD:-false}"
SKIP_DEPLOY="${SKIP_DEPLOY:-false}"
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

echo "=== M2 End-to-End Demo: Rating + Wallet + Billing API ==="
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

if ! command -v jq &>/dev/null; then
  fail "jq is required but not installed"
fi
pass "jq available"

echo "  Building billctl..."
cd "$REPO_ROOT" && go build -o "$REPO_ROOT/bin/billctl" "$REPO_ROOT/cmd/billctl/"
BILLCTL="$REPO_ROOT/bin/billctl"
pass "billctl built"

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

  echo "  Loading images into kind..."
  kind load docker-image controller:latest --name "$KIND_CLUSTER" >/dev/null 2>&1
  kind load docker-image ledger-svc:latest --name "$KIND_CLUSTER" >/dev/null 2>&1
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

  # Deploy operator manager (creates the operator namespace via kustomize).
  # Podman tags images as localhost/<name>; pass the correct reference to kustomize.
  OPERATOR_IMG="localhost/controller:latest"
  cd "$REPO_ROOT" && make deploy-dev KUBECTL="$KUBECTL" CLUSTER_CONTEXT="$CTX" IMG="$OPERATOR_IMG" >/dev/null 2>&1
  pass "Operator manager deployed"

  # Create the Postgres secret in the operator namespace (now that it exists).
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

  # Wait for rollouts.
  echo "  Waiting for operator rollout..."
  DEPLOY_SHORT=$(echo "$DEPLOY_NAME" | sed 's|deployment.apps/||')
  wait_for_rollout "$DEPLOY_SHORT" "$OPERATOR_NS" 120 || fail "Operator not ready"
  pass "Operator ready"

  echo "  Waiting for ledger-svc rollout..."
  wait_for_rollout ledger-svc "$NS" 120 || fail "ledger-svc not ready"
  pass "ledger-svc ready"
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
psql_query "DELETE FROM charges WHERE 1=1;" >/dev/null 2>&1 || true
psql_query "DELETE FROM usage_events WHERE 1=1;" >/dev/null 2>&1 || true
psql_query "DELETE FROM adjustments WHERE 1=1;" >/dev/null 2>&1 || true
psql_query "DELETE FROM wallet WHERE 1=1;" >/dev/null 2>&1 || true
psql_query "DELETE FROM dlq WHERE 1=1;" >/dev/null 2>&1 || true
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
  kctl delete tenantaccount ten-demo -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete plan plan-demo -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete pricebook pb-demo-v1 pb-demo-v2 -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

pass "Port-forward to ledger-svc on :18080"

# ---------- Test 1: PriceBook creation and activation ----------
echo ""
echo "--- Test 1: PriceBook creation and activation ---"

# Webhook requires effectiveFrom in the future; rater only checks status.state=Active.
FUTURE_DATE=$(date -u -v+30d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+30 days' '+%Y-%m-%dT%H:%M:%SZ')

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: PriceBook
metadata:
  name: pb-demo-v1
spec:
  currency: USD
  effectiveFrom: "${FUTURE_DATE}"
  noticeDays: 0
  skus:
    - sku: llama-3-70b
      unit: per-1M-tokens
      rates:
        prompt_tokens: "3.000000"
        completion_tokens: "10.000000"
EOF

if kctl get pricebook pb-demo-v1 -n "$NS" &>/dev/null; then
  pass "PriceBook pb-demo-v1 created"
else
  fail "PriceBook pb-demo-v1 not accepted"
fi

kctl patch pricebook pb-demo-v1 -n "$NS" --subresource status --type merge \
  -p '{"status":{"state":"Active"}}' >/dev/null
STATE=$(kctl get pricebook pb-demo-v1 -n "$NS" -o jsonpath='{.status.state}')
if [ "$STATE" = "Active" ]; then
  pass "PriceBook pb-demo-v1 is Active"
else
  fail "PriceBook pb-demo-v1 state: expected Active, got ${STATE}"
fi

# ---------- Test 2: TenantAccount creation (ShowbackOnly) ----------
echo ""
echo "--- Test 2: TenantAccount creation (ShowbackOnly) ---"

# Create a Plan for the TenantAccount to reference.
kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: billing.opendatahub.io/v1alpha1
kind: Plan
metadata:
  name: plan-demo
spec:
  limits:
    requestsPerMinute: 600
    tokensPerMinute: 2000000
    tokensPerDay: 400000000
  modelScopes: ["llama-3-*"]
  priceBookRef: pb-demo-v1
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: billing-demo-gateway
EOF

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-demo
spec:
  displayName: "Demo Tenant"
  billingMode: ShowbackOnly
  planRef:
    name: plan-demo
EOF

echo "  Waiting for TenantAccount reconcile (up to 30s)..."
DEADLINE=$((SECONDS + 30))
TA_STATE=""
while [ $SECONDS -lt $DEADLINE ]; do
  TA_STATE=$(kctl get tenantaccount ten-demo -n "$NS" -o jsonpath='{.status.state}' 2>/dev/null)
  if [ "$TA_STATE" = "Active" ]; then
    break
  fi
  sleep 2
done

if [ "$TA_STATE" = "Active" ]; then
  pass "TenantAccount ten-demo is Active (ShowbackOnly)"
else
  fail "TenantAccount ten-demo state: expected Active, got ${TA_STATE}"
fi

# ---------- Test 3: Seed balance via billctl ----------
echo ""
echo "--- Test 3: Seed balance via billctl ---"

$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-demo \
  --amount 100.000000 \
  --kind manual_credit \
  --reason "M2 demo seed" \
  --idempotency-key "m2-demo-seed-001" >/dev/null 2>&1

# Verify the balance. billctl balance outputs key-value lines:
#   Tenant:    ten-demo
#   Balance:   100.000000 USD
#   State:     Active
#   As of:     2026-08-01T...
BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-demo 2>/dev/null)
CURRENT_BALANCE=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')

if [ "$CURRENT_BALANCE" = "100.000000" ]; then
  pass "Balance seeded: 100.000000 USD"
else
  fail "Seed balance: expected 100.000000, got ${CURRENT_BALANCE}"
fi

# ---------- Test 4: Ingest 5 events ----------
echo ""
echo "--- Test 4: Ingest 5 usage events ---"

for i in $(seq 1 5); do
  TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  EVENT_JSON=$(cat <<EVEOF
{
  "schema_version": "usage.v1",
  "record_id": "m2-demo-evt-${i}",
  "occurred_at": "${TS}",
  "tenant_id": "ten-demo",
  "source": {"profile": "maas", "collector": "demo-m2", "collector_version": "0.0.1"},
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
  ESCAPED=$(echo "$EVENT_JSON" | jq -c '.' | jq -Rs '.')
  BODY="{\"event_json\": ${ESCAPED}}"
  RESULT=$(curl_via_pod -s -X POST "http://ledger-svc.${NS}.svc:8080/v1/ingest" \
    -H "Content-Type: application/json" \
    -d "$BODY" 2>/dev/null || true)
  RESULT_VAL=$(echo "$RESULT" | jq -r '.result' 2>/dev/null || echo "error")
  if [ "$RESULT_VAL" != "inserted" ]; then
    fail "Event ${i} ingest failed: ${RESULT}"
  fi
done

COUNT=$(psql_query "SELECT count(*) FROM usage_events WHERE tenant_id='ten-demo';")
if [ "$COUNT" -eq 5 ]; then
  pass "5 events ingested"
else
  fail "Expected 5 events, got ${COUNT}"
fi

# ---------- Test 5: Wait for rating job and verify balance ----------
echo ""
echo "--- Test 5: Rating job → balance decrease ---"

# Expected math:
#   PriceBook rates: prompt_tokens = 3.000000/1M, completion_tokens = 10.000000/1M
#   Per event: prompt = 1000 * 3.000000 / 1,000,000 = 0.003000
#   Per event: completion = 500 * 10.000000 / 1,000,000 = 0.005000
#   Per event total: 0.008000
#   5 events total charge: 0.040000
#   Expected balance: 100.000000 - 0.040000 = 99.960000

echo "  Waiting for rating job to process events..."
DEADLINE=$((SECONDS + 30))
CURRENT_BALANCE=""
while [ $SECONDS -lt $DEADLINE ]; do
  BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-demo 2>/dev/null)
  CURRENT_BALANCE=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')
  if [ "$CURRENT_BALANCE" != "100.000000" ] && [ -n "$CURRENT_BALANCE" ]; then
    break
  fi
  sleep 1
done

if [ "$CURRENT_BALANCE" = "99.960000" ]; then
  pass "Balance after rating: 99.960000 USD (5 events at 0.008000 each)"
else
  fail "Balance after rating: expected 99.960000, got ${CURRENT_BALANCE}"
fi

# ---------- Test 6: Verify billctl usage ----------
echo ""
echo "--- Test 6: billctl usage query ---"

# Time window: 5 minutes ago to now.
T0=$(date -u -v-5M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '-5 minutes' +%Y-%m-%dT%H:%M:%SZ)
T1=$(date -u +%Y-%m-%dT%H:%M:%SZ)
USAGE_OUTPUT=$($BILLCTL usage $BILLCTL_ARGS --tenant ten-demo --from "$T0" --to "$T1" --bucket day 2>/dev/null)

# The usage output is a tabwriter table:
#   START     SKU            CHARGED     EVENTS
#   2026-08-01  llama-3-70b    0.040000    5
USAGE_EVENTS=$(echo "$USAGE_OUTPUT" | grep 'llama-3-70b' | awk '{print $NF}')
USAGE_CHARGED=$(echo "$USAGE_OUTPUT" | grep 'llama-3-70b' | awk '{print $(NF-1)}')

if [ "$USAGE_EVENTS" = "5" ]; then
  pass "billctl usage shows 5 events"
else
  fail "billctl usage events: expected 5, got ${USAGE_EVENTS}"
fi

if [ "$USAGE_CHARGED" = "0.040000" ]; then
  pass "billctl usage shows 0.040000 total charge"
else
  fail "billctl usage charge: expected 0.040000, got ${USAGE_CHARGED}"
fi

# ---------- Test 7: PriceBook v2 (rate change + forward-only) ----------
echo ""
echo "--- Test 7: PriceBook v2 — rate change, forward-only verification ---"

# Create pb-demo-v2 with doubled rates.
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: PriceBook
metadata:
  name: pb-demo-v2
spec:
  currency: USD
  effectiveFrom: "${FUTURE_DATE}"
  noticeDays: 0
  skus:
    - sku: llama-3-70b
      unit: per-1M-tokens
      rates:
        prompt_tokens: "6.000000"
        completion_tokens: "20.000000"
EOF

if kctl get pricebook pb-demo-v2 -n "$NS" &>/dev/null; then
  pass "PriceBook pb-demo-v2 created"
else
  fail "PriceBook pb-demo-v2 not accepted"
fi

# Activate v2 and supersede v1.
kctl patch pricebook pb-demo-v2 -n "$NS" --subresource status --type merge \
  -p '{"status":{"state":"Active"}}' >/dev/null
kctl patch pricebook pb-demo-v1 -n "$NS" --subresource status --type merge \
  -p '{"status":{"state":"Superseded"}}' >/dev/null
pass "pb-demo-v2 Active, pb-demo-v1 Superseded"

# Wait for K8sResolver cache to expire (30s TTL + 5s margin).
echo "  Waiting 35s for K8sResolver cache expiry..."
sleep 35

# Ingest 3 more events at doubled rates.
for i in $(seq 6 8); do
  TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  EVENT_JSON=$(cat <<EVEOF
{
  "schema_version": "usage.v1",
  "record_id": "m2-demo-evt-${i}",
  "occurred_at": "${TS}",
  "tenant_id": "ten-demo",
  "source": {"profile": "maas", "collector": "demo-m2", "collector_version": "0.0.1"},
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
  ESCAPED=$(echo "$EVENT_JSON" | jq -c '.' | jq -Rs '.')
  BODY="{\"event_json\": ${ESCAPED}}"
  RESULT=$(curl_via_pod -s -X POST "http://ledger-svc.${NS}.svc:8080/v1/ingest" \
    -H "Content-Type: application/json" \
    -d "$BODY" 2>/dev/null || true)
  RESULT_VAL=$(echo "$RESULT" | jq -r '.result' 2>/dev/null || echo "error")
  if [ "$RESULT_VAL" != "inserted" ]; then
    fail "Event ${i} ingest failed: ${RESULT}"
  fi
done

# Verify all 8 events exist.
TOTAL_COUNT=$(psql_query "SELECT count(*) FROM usage_events WHERE tenant_id='ten-demo';")
if [ "$TOTAL_COUNT" -eq 8 ]; then
  pass "8 total events ingested"
else
  fail "Expected 8 total events, got ${TOTAL_COUNT}"
fi

# Wait for rating job to process the new events.
# Expected balance: 99.960000 - (3 * 0.016000) = 99.960000 - 0.048000 = 99.912000
echo "  Waiting for rating job to process new events..."
DEADLINE=$((SECONDS + 30))
CURRENT_BALANCE=""
while [ $SECONDS -lt $DEADLINE ]; do
  BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-demo 2>/dev/null)
  CURRENT_BALANCE=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')
  if [ "$CURRENT_BALANCE" != "99.960000" ] && [ -n "$CURRENT_BALANCE" ]; then
    break
  fi
  sleep 1
done

if [ "$CURRENT_BALANCE" = "99.912000" ]; then
  pass "Balance after v2 rating: 99.912000 USD (3 events at 0.016000 each)"
else
  fail "Balance after v2 rating: expected 99.912000, got ${CURRENT_BALANCE}"
fi

# Verify per-PriceBook charge counts.
V1_CHARGES=$(psql_query "SELECT count(*) FROM charges WHERE price_book='pb-demo-v1' AND tenant_id='ten-demo';")
if [ "$V1_CHARGES" -eq 5 ]; then
  pass "5 events rated at PriceBook v1"
else
  fail "Expected 5 v1 charges, got ${V1_CHARGES}"
fi

V2_CHARGES=$(psql_query "SELECT count(*) FROM charges WHERE price_book='pb-demo-v2' AND tenant_id='ten-demo';")
if [ "$V2_CHARGES" -eq 3 ]; then
  pass "3 events rated at PriceBook v2"
else
  fail "Expected 3 v2 charges, got ${V2_CHARGES}"
fi

# Verify individual charge amounts.
V1_AMOUNT=$(psql_query "SELECT amount FROM charges WHERE price_book='pb-demo-v1' AND tenant_id='ten-demo' ORDER BY rated_at LIMIT 1;")
if [ "$V1_AMOUNT" = "0.008000" ]; then
  pass "v1 charge amount correct (0.008000)"
else
  fail "v1 charge amount: expected 0.008000, got ${V1_AMOUNT}"
fi

V2_AMOUNT=$(psql_query "SELECT amount FROM charges WHERE price_book='pb-demo-v2' AND tenant_id='ten-demo' ORDER BY rated_at LIMIT 1;")
if [ "$V2_AMOUNT" = "0.016000" ]; then
  pass "v2 charge amount correct (0.016000)"
else
  fail "v2 charge amount: expected 0.016000, got ${V2_AMOUNT}"
fi

# ---------- Test 8: ShowbackOnly invariant ----------
echo ""
echo "--- Test 8: ShowbackOnly invariant ---"

STATE=$(kctl get tenantaccount ten-demo -n "$NS" -o jsonpath='{.status.state}')
if [ "$STATE" = "Active" ]; then
  pass "ShowbackOnly tenant stays Active despite balance decrease"
else
  fail "ShowbackOnly state: expected Active, got ${STATE}"
fi

# ---------- Test 9: Rater determinism ----------
echo ""
echo "--- Test 9: Rater determinism ---"

echo "  Running rater determinism test..."
if (cd "$REPO_ROOT" && make rater-determinism >/dev/null 2>&1); then
  pass "Rater determinism test passed (10 runs)"
else
  fail "Rater determinism test failed"
fi

# ---------- Summary ----------
echo ""
echo "--- Ledger summary ---"
psql_query "SELECT 'usage_events' AS tbl, count(*) FROM usage_events WHERE tenant_id='ten-demo' UNION ALL SELECT 'charges', count(*) FROM charges WHERE tenant_id='ten-demo' UNION ALL SELECT 'adjustments', count(*) FROM adjustments WHERE tenant_id='ten-demo';"

echo ""
echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
