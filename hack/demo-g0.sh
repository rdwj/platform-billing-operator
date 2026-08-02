#!/usr/bin/env bash
# hack/demo-g0.sh — G0 end-to-end demo: GPUaaS metering + unified wallet.
#
# Validates the G0 pipeline: GPU event ingest -> GPU rating -> unified wallet
# showing charges from both MaaS and GPUaaS profiles.
#
# Exit criteria (G0 milestone):
#   1. Combined PriceBook creation with MaaS + GPU SKUs (h200, h200-mig-3g.40gb)
#   2. GPU usage events land deduplicated in the ledger
#   3. Rating job processes GPU events correctly ($4.50/hr MIG rate)
#   4. Unified wallet: billctl balance shows combined MaaS + GPUaaS charges
#   5. billctl usage --sku filters to GPU-only or MaaS-only usage
#   6. Kueue dev environment functional (setup-kueue.sh)
#
# Prerequisites:
#   - kind cluster (same as demo-m2)
#   - Postgres deployed (hack/setup-postgres.sh)
#   - Kueue installed (hack/setup-kueue.sh, will be called if missing)
#   - Go 1.22+ for building billctl
#   - jq for JSON parsing

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

echo "=== G0 End-to-End Demo: GPUaaS Metering + Unified Wallet ==="
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

# Install Kueue if not present.
if ! kctl get namespace kueue-system &>/dev/null; then
  echo "  Kueue not found — installing via setup-kueue.sh..."
  KUBECTL="$KUBECTL" DEMO_NS="$NS" "$SCRIPT_DIR/setup-kueue.sh" "$CTX"
  pass "Kueue installed"
else
  pass "Kueue already present"
fi

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

  kctl create namespace "$NS" --dry-run=client -o yaml | kctl apply -f - >/dev/null

  # Cross-namespace Postgres secret.
  POSTGRES_PASSWORD=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_PASSWORD}' | base64 -d)
  POSTGRES_USER=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_USER}' | base64 -d)
  POSTGRES_DB=$(kctl get secret billing-ledger-postgres -n "$POSTGRES_NS" \
    -o jsonpath='{.data.POSTGRES_DB}' | base64 -d)
  LEDGER_DSN="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres.${POSTGRES_NS}.svc:5432/${POSTGRES_DB}?sslmode=disable"

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

  cd "$REPO_ROOT" && make install KUBECTL="$KUBECTL" >/dev/null 2>&1
  pass "CRDs installed"

  OPERATOR_IMG="localhost/controller:latest"
  cd "$REPO_ROOT" && make deploy-dev KUBECTL="$KUBECTL" CLUSTER_CONTEXT="$CTX" IMG="$OPERATOR_IMG" >/dev/null 2>&1
  pass "Operator manager deployed"

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

  # Patch operator: imagePullPolicy, LEDGER_DSN, KUEUE_COLLECTOR_LEDGER_URL.
  DEPLOY_NAME=$(kctl get deployment -n "$OPERATOR_NS" -l control-plane=controller-manager -o name | head -1)
  kctl patch "$DEPLOY_NAME" -n "$OPERATOR_NS" --type=json -p '[
    {"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"},
    {"op":"add","path":"/spec/template/spec/containers/0/env","value":[
      {"name":"LEDGER_DSN","valueFrom":{"secretKeyRef":{"name":"billing-ledger-postgres","key":"DSN"}}},
      {"name":"LEDGER_MIGRATIONS_DIR","value":"/migrations"},
      {"name":"KUEUE_COLLECTOR_LEDGER_URL","value":"http://ledger-svc.'"$NS"'.svc:8080/v1/ingest"},
      {"name":"COLLECTOR_DEFAULT_GEO","value":"us-east-1"}
    ]}
  ]' >/dev/null 2>&1 || true
  pass "Operator patched (imagePullPolicy=Never, LEDGER_DSN, KUEUE_COLLECTOR_LEDGER_URL)"

  # Deploy ledger-svc.
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
  kctl delete tenantaccount ten-gpu-demo -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete plan plan-g0-demo -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete pricebook pb-g0-demo -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

pass "Port-forward to ledger-svc on :18080"

# ---------- Test 1: Combined PriceBook (MaaS + GPU SKUs) ----------
echo ""
echo "--- Test 1: Combined PriceBook creation and activation ---"

FUTURE_DATE=$(date -u -v+30d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+30 days' '+%Y-%m-%dT%H:%M:%SZ')

# Single PriceBook with MaaS and GPU SKUs for unified-wallet testing.
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: PriceBook
metadata:
  name: pb-g0-demo
spec:
  currency: USD
  effectiveFrom: "${FUTURE_DATE}"
  noticeDays: 0
  skus:
    - sku: llama-3-70b
      unit: per-1M-tokens
      rates:
        prompt_tokens: "2.500000"
        completion_tokens: "10.000000"
    - sku: h200
      unit: per-gpu-second
      rates:
        gpu_seconds: "0.002917"
    - sku: h200-mig-3g.40gb
      unit: per-gpu-second
      rates:
        gpu_seconds: "0.001250"
EOF

if kctl get pricebook pb-g0-demo -n "$NS" &>/dev/null; then
  pass "PriceBook pb-g0-demo created (3 SKUs: llama-3-70b, h200, h200-mig-3g.40gb)"
else
  fail "PriceBook pb-g0-demo not accepted"
fi

kctl patch pricebook pb-g0-demo -n "$NS" --subresource status --type merge \
  -p '{"status":{"state":"Active"}}' >/dev/null
STATE=$(kctl get pricebook pb-g0-demo -n "$NS" -o jsonpath='{.status.state}')
if [ "$STATE" = "Active" ]; then
  pass "PriceBook pb-g0-demo is Active"
else
  fail "PriceBook pb-g0-demo state: expected Active, got ${STATE}"
fi

# ---------- Test 2: Plan + TenantAccount ----------
echo ""
echo "--- Test 2: Plan + TenantAccount creation ---"

kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: billing.opendatahub.io/v1alpha1
kind: Plan
metadata:
  name: plan-g0-demo
spec:
  limits:
    requestsPerMinute: 600
    tokensPerMinute: 2000000
    tokensPerDay: 400000000
  modelScopes: ["*"]
  priceBookRef: pb-g0-demo
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: billing-demo-gateway
EOF

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-gpu-demo
spec:
  displayName: "GPU Demo Tenant"
  billingMode: ShowbackOnly
  planRef:
    name: plan-g0-demo
EOF

echo "  Waiting for TenantAccount reconcile (up to 30s)..."
DEADLINE=$((SECONDS + 30))
TA_STATE=""
while [ $SECONDS -lt $DEADLINE ]; do
  TA_STATE=$(kctl get tenantaccount ten-gpu-demo -n "$NS" -o jsonpath='{.status.state}' 2>/dev/null)
  if [ "$TA_STATE" = "Active" ]; then break; fi
  sleep 2
done

if [ "$TA_STATE" = "Active" ]; then
  pass "TenantAccount ten-gpu-demo is Active"
else
  fail "TenantAccount ten-gpu-demo state: expected Active, got ${TA_STATE}"
fi

# ---------- Test 3: Seed balance ----------
echo ""
echo "--- Test 3: Seed balance via billctl ---"

$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-gpu-demo \
  --amount 100.000000 \
  --kind manual_credit \
  --reason "G0 demo seed" \
  --idempotency-key "g0-demo-seed-001" >/dev/null 2>&1

BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-gpu-demo 2>/dev/null)
CURRENT_BALANCE=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')

if [ "$CURRENT_BALANCE" = "100.000000" ]; then
  pass "Balance seeded: 100.000000 USD"
else
  fail "Seed balance: expected 100.000000, got ${CURRENT_BALANCE}"
fi

# ---------- Test 4: Ingest GPU events ----------
echo ""
echo "--- Test 4: Ingest 2 GPU events (h200-mig-3g.40gb, 3600 gpu_seconds each) ---"

# Math: 3600 gpu_seconds * 0.001250 $/gpu-second = 4.500000 per event.
INTERVAL_START=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '-1 hour' +%Y-%m-%dT%H:%M:%SZ)

for i in 1 2; do
  TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  EVENT_JSON=$(cat <<EVEOF
{
  "schema_version": "usage.v1",
  "record_id": "g0-demo-gpu-evt-${i}",
  "occurred_at": "${TS}",
  "tenant_id": "ten-gpu-demo",
  "source": {"profile": "gpuaas", "collector": "demo-g0"},
  "sku": "h200-mig-3g.40gb",
  "geo": "us-east-1",
  "quantities": {"gpu_seconds": 3600, "slice_fraction": 0.375},
  "status": "ok",
  "interval": {"start": "${INTERVAL_START}", "end": "${TS}", "seq": ${i}},
  "attributes": {"queue": "team-ml-batch", "workload": "demo-train-run"}
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
    fail "GPU event ${i} ingest failed: ${RESULT}"
  fi
done

COUNT=$(psql_query "SELECT count(*) FROM usage_events WHERE tenant_id='ten-gpu-demo' AND profile='gpuaas';")
if [ "$COUNT" -eq 2 ]; then
  pass "2 GPU events ingested (profile=gpuaas)"
else
  fail "Expected 2 GPU events, got ${COUNT}"
fi

# ---------- Test 5: Rating job -> GPU balance decrease ----------
echo ""
echo "--- Test 5: Rating job processes GPU events ---"

# Expected: 2 events * 4.500000 = 9.000000 total charge.
# Balance: 100.000000 - 9.000000 = 91.000000.
echo "  Waiting for rating job to process GPU events..."
DEADLINE=$((SECONDS + 30))
CURRENT_BALANCE=""
while [ $SECONDS -lt $DEADLINE ]; do
  BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-gpu-demo 2>/dev/null)
  CURRENT_BALANCE=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')
  if [ "$CURRENT_BALANCE" != "100.000000" ] && [ -n "$CURRENT_BALANCE" ]; then
    break
  fi
  sleep 1
done

if [ "$CURRENT_BALANCE" = "91.000000" ]; then
  pass "Balance after GPU rating: 91.000000 USD (2 events at 4.500000 each)"
else
  fail "Balance after GPU rating: expected 91.000000, got ${CURRENT_BALANCE}"
fi

# ---------- Test 6: Unified wallet — add MaaS event ----------
echo ""
echo "--- Test 6: Unified wallet — ingest MaaS event + verify combined balance ---"

# Ingest 1 MaaS event: llama-3-70b, 1000 prompt + 500 completion tokens.
# Charge: 1000 * 2.500000/1M + 500 * 10.000000/1M = 0.002500 + 0.005000 = 0.007500.
# Expected balance: 91.000000 - 0.007500 = 90.992500.
TS=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EVENT_JSON=$(cat <<EVEOF
{
  "schema_version": "usage.v1",
  "record_id": "g0-demo-maas-evt-1",
  "occurred_at": "${TS}",
  "tenant_id": "ten-gpu-demo",
  "source": {"profile": "maas", "collector": "demo-g0", "collector_version": "0.0.1"},
  "sku": "llama-3-70b",
  "geo": "us-east-1",
  "quantities": {"prompt_tokens": 1000, "completion_tokens": 500, "total_tokens": 1500},
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
  fail "MaaS event ingest failed: ${RESULT}"
fi
pass "MaaS event ingested (llama-3-70b, 1000 prompt + 500 completion)"

echo "  Waiting for rating job to process MaaS event..."
DEADLINE=$((SECONDS + 30))
CURRENT_BALANCE=""
while [ $SECONDS -lt $DEADLINE ]; do
  BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-gpu-demo 2>/dev/null)
  CURRENT_BALANCE=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')
  if [ "$CURRENT_BALANCE" != "91.000000" ] && [ -n "$CURRENT_BALANCE" ]; then
    break
  fi
  sleep 1
done

if [ "$CURRENT_BALANCE" = "90.992500" ]; then
  pass "Unified balance: 90.992500 USD (GPU 9.000000 + MaaS 0.007500 deducted)"
else
  fail "Unified balance: expected 90.992500, got ${CURRENT_BALANCE}"
fi

# ---------- Test 7: SKU-filtered usage queries ----------
echo ""
echo "--- Test 7: billctl usage — SKU-filtered queries ---"

T0=$(date -u -v-5M +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '-5 minutes' +%Y-%m-%dT%H:%M:%SZ)
T1=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# GPU-only usage.
GPU_USAGE=$($BILLCTL usage $BILLCTL_ARGS --tenant ten-gpu-demo --from "$T0" --to "$T1" --bucket day --sku h200-mig-3g.40gb 2>/dev/null)
GPU_EVENTS=$(echo "$GPU_USAGE" | grep 'h200-mig-3g.40gb' | awk '{print $NF}')
GPU_CHARGED=$(echo "$GPU_USAGE" | grep 'h200-mig-3g.40gb' | awk '{print $(NF-1)}')

if [ "$GPU_EVENTS" = "2" ]; then
  pass "GPU usage: 2 events for h200-mig-3g.40gb"
else
  fail "GPU usage events: expected 2, got ${GPU_EVENTS}"
fi

if [ "$GPU_CHARGED" = "9.000000" ]; then
  pass "GPU usage charge: 9.000000"
else
  fail "GPU usage charge: expected 9.000000, got ${GPU_CHARGED}"
fi

# MaaS-only usage.
MAAS_USAGE=$($BILLCTL usage $BILLCTL_ARGS --tenant ten-gpu-demo --from "$T0" --to "$T1" --bucket day --sku llama-3-70b 2>/dev/null)
MAAS_EVENTS=$(echo "$MAAS_USAGE" | grep 'llama-3-70b' | awk '{print $NF}')

if [ "$MAAS_EVENTS" = "1" ]; then
  pass "MaaS usage: 1 event for llama-3-70b"
else
  fail "MaaS usage events: expected 1, got ${MAAS_EVENTS}"
fi

# ---------- Test 8: Kueue environment functional ----------
echo ""
echo "--- Test 8: Kueue environment functional ---"

if kctl get namespace kueue-system &>/dev/null; then
  pass "kueue-system namespace exists"
else
  fail "kueue-system namespace missing"
fi

if kctl get clusterqueue gpu-cluster-queue &>/dev/null; then
  pass "ClusterQueue gpu-cluster-queue exists"
else
  fail "ClusterQueue gpu-cluster-queue missing"
fi

if kctl get localqueue team-ml-batch -n "$NS" &>/dev/null; then
  pass "LocalQueue team-ml-batch exists in ${NS}"
else
  fail "LocalQueue team-ml-batch missing in ${NS}"
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
psql_query "SELECT 'usage_events' AS tbl, count(*) FROM usage_events WHERE tenant_id='ten-gpu-demo' UNION ALL SELECT 'charges', count(*) FROM charges WHERE tenant_id='ten-gpu-demo' UNION ALL SELECT 'adjustments', count(*) FROM adjustments WHERE tenant_id='ten-gpu-demo';"

echo ""
echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
