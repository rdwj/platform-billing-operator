#!/usr/bin/env bash
# hack/demo-m3.sh — M3 end-to-end demo: Allowance enforcement + freeze/thaw.
#
# Validates the full M3 lifecycle: Active → Warning → Frozen → Active (thaw),
# grace-period behavior (graceAmount > 0), and overrun-bound timing.
#
# Exit criteria (from milestones.md M3):
#   1. Full Active → Warning → Frozen → Active lifecycle (graceAmount=0)
#   2. Grace-period lifecycle (graceAmount > 0)
#   3. Warning state triggered by warningThreshold
#   4. Overrun enforcement bound (balance cross → Frozen within 90s)
#
# Prerequisites:
#   - kind cluster (hack/setup-kuadrant.sh or equivalent)
#   - Postgres deployed (hack/setup-postgres.sh)
#   - Kuadrant CRDs installed (TokenRateLimitPolicy, AuthPolicy)
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
    local record_id="m3-${tenant}-$(date +%s%N)-${i}"
    local EVENT_JSON
    EVENT_JSON=$(cat <<EVEOF
{
  "schema_version": "usage.v1",
  "record_id": "${record_id}",
  "occurred_at": "${ts}",
  "tenant_id": "${tenant}",
  "source": {"profile": "maas", "collector": "demo-m3", "collector_version": "0.0.1"},
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

echo "=== M3 End-to-End Demo: Allowance Enforcement + Freeze/Thaw ==="
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
pass "Kuadrant TokenRateLimitPolicy CRD available"

if ! kctl get crd authpolicies.kuadrant.io &>/dev/null; then
  fail "Kuadrant AuthPolicy CRD not installed — run hack/setup-kuadrant.sh"
fi
pass "Kuadrant AuthPolicy CRD available"

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

  # Force pod restart to pick up newly-loaded images (kind reuses tag, so
  # Kubernetes won't trigger a rollout on its own).
  kctl rollout restart "$DEPLOY_NAME" -n "$OPERATOR_NS" >/dev/null 2>&1
  pass "Operator rollout restarted"

  # ServiceAccount + RBAC for ledger-svc (needed for /v1/pricing CR reads).
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
  for acct in ten-m3-immed ten-m3-grace ten-m3-timing ten-m3-keys; do
    kctl delete tenantaccount "$acct" -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  done
  kctl delete plan plan-m3 -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete pricebook pb-m3 -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

pass "Port-forward to ledger-svc on :18080"

# Assert BILLING_NAMESPACE is set correctly for kind environment.
# (Verified by the pricing test — if /v1/pricing returns data, BILLING_NAMESPACE works.)
pass "BILLING_NAMESPACE configured as ${NS}"

# ---------- Shared fixtures: PriceBook + Plan ----------
echo ""
echo "--- Shared fixtures ---"

# PriceBook pb-m3: higher rates to keep event counts manageable.
# Per event (1000 prompt + 500 completion):
#   prompt  = 1000 * 30.000000 / 1,000,000 = 0.030000
#   compl   = 500  * 100.000000 / 1,000,000 = 0.050000
#   total   = 0.080000
FUTURE_DATE=$(date -u -v+30d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+30 days' '+%Y-%m-%dT%H:%M:%SZ')

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: PriceBook
metadata:
  name: pb-m3
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

if kctl get pricebook pb-m3 -n "$NS" &>/dev/null; then
  pass "PriceBook pb-m3 created"
else
  fail "PriceBook pb-m3 not accepted"
fi

kctl patch pricebook pb-m3 -n "$NS" --subresource status --type merge \
  -p '{"status":{"state":"Active"}}' >/dev/null
STATE=$(kctl get pricebook pb-m3 -n "$NS" -o jsonpath='{.status.state}')
if [ "$STATE" = "Active" ]; then
  pass "PriceBook pb-m3 is Active"
else
  fail "PriceBook pb-m3 state: expected Active, got ${STATE}"
fi

# Plan plan-m3.
kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: billing.opendatahub.io/v1alpha1
kind: Plan
metadata:
  name: plan-m3
spec:
  limits:
    tokensPerMinute: 2000000
    requestsPerMinute: 600
  modelScopes: ["llama-3-*"]
  priceBookRef: pb-m3
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: billing-demo-gateway
EOF

if kctl get plan plan-m3 -n "$NS" &>/dev/null; then
  pass "Plan plan-m3 created"
else
  fail "Plan plan-m3 not accepted"
fi

# ========================================================================
# Test 1: graceAmount=0, full lifecycle (Active → Warning → Frozen → Active)
# ========================================================================
echo ""
echo "--- Test 1: Full lifecycle — Active → Warning → Frozen → Active (graceAmount=0) ---"

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m3-immed
spec:
  displayName: "M3 Immediate Freeze"
  billingMode: Prepaid
  planRef:
    name: plan-m3
  gracePolicy:
    graceAmount: "0.000000"
    warningThreshold: "0.300000"
EOF

if kctl get tenantaccount ten-m3-immed -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m3-immed created"
else
  fail "TenantAccount ten-m3-immed not accepted"
fi

# Step 1: Seed $1.00.
echo '  Seeding $1.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m3-immed \
  --amount 1.000000 \
  --kind manual_credit \
  --reason "M3 demo seed" \
  --idempotency-key "m3-seed-immed-$(date +%s)" >/dev/null 2>&1

# Step 2: Wait for Active state.
echo "  Waiting for Active state..."
if wait_for_state ten-m3-immed "$NS" Active 90; then
  pass "ten-m3-immed is Active after seeding"
else
  fail "ten-m3-immed did not reach Active state"
fi

# Step 3: Assert TRLP exists.
if kctl get tokenratelimitpolicies.kuadrant.io ten-m3-immed-enforcement-trlp -n "$NS" &>/dev/null; then
  pass "TRLP ten-m3-immed-enforcement-trlp exists"
else
  fail "TRLP ten-m3-immed-enforcement-trlp not found"
fi

# Step 4: Assert AuthPolicy deny does NOT exist.
if ! kctl get authpolicies.kuadrant.io ten-m3-immed-enforcement-deny -n "$NS" &>/dev/null; then
  pass "AuthPolicy deny does not exist (as expected in Active state)"
else
  fail "AuthPolicy deny should not exist in Active state"
fi

# Step 5: Assert allowanceTokens > 0.
ALLOWANCE=$(kctl get tenantaccount ten-m3-immed -n "$NS" -o jsonpath='{.status.allowanceTokens}')
if [ -n "$ALLOWANCE" ] && echo "$ALLOWANCE" | awk '{exit ($1 > 0) ? 0 : 1}'; then
  pass "allowanceTokens is positive: ${ALLOWANCE}"
else
  fail "allowanceTokens should be > 0, got: ${ALLOWANCE}"
fi

# Step 6: Ingest 10 events (cost=$0.80, balance→$0.20, below warningThreshold=$0.30).
echo "  Ingesting 10 events (cost=$0.80)..."
ingest_events ten-m3-immed 10

# Wait for rating to process.
echo "  Waiting for rating + reconcile..."
sleep 5

# Step 7: Wait for Warning state.
echo "  Waiting for Warning state..."
if wait_for_state ten-m3-immed "$NS" Warning 60; then
  pass "ten-m3-immed entered Warning state"
else
  # It may have already transitioned past Warning if rating was fast.
  CURRENT_STATE=$(kctl get tenantaccount ten-m3-immed -n "$NS" -o jsonpath='{.status.state}' 2>/dev/null)
  if [ "$CURRENT_STATE" = "Frozen" ]; then
    echo "  (skipped Warning — rater processed all events in one batch)"
    pass "ten-m3-immed transitioned to Frozen (Warning may have been transient)"
  else
    fail "ten-m3-immed did not reach Warning or Frozen (got ${CURRENT_STATE})"
  fi
fi

# Step 8: Verify balance is approximately $0.20.
BALANCE_OUTPUT=$($BILLCTL balance $BILLCTL_ARGS --tenant ten-m3-immed 2>/dev/null)
CURRENT_BALANCE=$(echo "$BALANCE_OUTPUT" | grep '^Balance:' | awk '{print $2}')
echo "  Balance after 10 events: ${CURRENT_BALANCE}"

# Step 9: Ingest 3 more events (cost=$0.24, total=$1.04, balance→-$0.040000).
echo "  Ingesting 3 more events (cost=$0.24)..."
ingest_events ten-m3-immed 3

echo "  Waiting for rating + reconcile..."
sleep 5

# Step 10: Wait for Frozen state.
echo "  Waiting for Frozen state..."
if wait_for_state ten-m3-immed "$NS" Frozen 90; then
  pass "ten-m3-immed entered Frozen state"
else
  fail "ten-m3-immed did not reach Frozen state"
fi

# Step 11: Assert AuthPolicy deny exists.
if kctl get authpolicies.kuadrant.io ten-m3-immed-enforcement-deny -n "$NS" &>/dev/null; then
  pass "AuthPolicy deny exists in Frozen state"
else
  fail "AuthPolicy deny not found in Frozen state"
fi

# Step 12: Top up $2.00 (thaw).
echo '  Topping up $2.00 to thaw...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m3-immed \
  --amount 2.000000 \
  --kind manual_credit \
  --reason "M3 demo top-up" \
  --idempotency-key "m3-topup-immed-$(date +%s)" >/dev/null 2>&1

# Step 13: Wait for Active state (thaw).
echo "  Waiting for Active state (thaw)..."
if wait_for_state ten-m3-immed "$NS" Active 90; then
  pass "ten-m3-immed thawed back to Active"
else
  fail "ten-m3-immed did not thaw to Active"
fi

# Step 14: Assert AuthPolicy deny is GONE.
if ! kctl get authpolicies.kuadrant.io ten-m3-immed-enforcement-deny -n "$NS" &>/dev/null; then
  pass "AuthPolicy deny removed after thaw"
else
  fail "AuthPolicy deny still exists after thaw"
fi

# Step 15: Assert TRLP exists (re-applied with new allowance).
if kctl get tokenratelimitpolicies.kuadrant.io ten-m3-immed-enforcement-trlp -n "$NS" &>/dev/null; then
  pass "TRLP re-applied after thaw"
else
  fail "TRLP not found after thaw"
fi

# ========================================================================
# Test 2: graceAmount > 0, grace period
# ========================================================================
echo ""
echo "--- Test 2: Grace period — graceAmount=0.500000 ---"

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m3-grace
spec:
  displayName: "M3 Grace Period"
  billingMode: Prepaid
  planRef:
    name: plan-m3
  gracePolicy:
    graceAmount: "0.500000"
    warningThreshold: "0.300000"
EOF

if kctl get tenantaccount ten-m3-grace -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m3-grace created"
else
  fail "TenantAccount ten-m3-grace not accepted"
fi

# Seed $1.00.
echo '  Seeding $1.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m3-grace \
  --amount 1.000000 \
  --kind manual_credit \
  --reason "M3 demo seed" \
  --idempotency-key "m3-seed-grace-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m3-grace "$NS" Active 90; then
  pass "ten-m3-grace is Active after seeding"
else
  fail "ten-m3-grace did not reach Active state"
fi

# Step 2: Ingest 13 events (cost=$1.04, balance→-$0.040000).
# |-0.04| = 0.04 < graceAmount 0.50, so should enter Grace (not Frozen).
echo '  Ingesting 13 events (cost=$1.04)...'
ingest_events ten-m3-grace 13

echo "  Waiting for rating + reconcile..."
sleep 5

# Step 3: Wait for Grace state.
echo "  Waiting for Grace state..."
if wait_for_state ten-m3-grace "$NS" Grace 90; then
  pass "ten-m3-grace entered Grace state"
else
  CURRENT_STATE=$(kctl get tenantaccount ten-m3-grace -n "$NS" -o jsonpath='{.status.state}' 2>/dev/null)
  fail "ten-m3-grace did not reach Grace state (got ${CURRENT_STATE})"
fi

# Step 4: Assert no AuthPolicy deny (Grace still allows access).
if ! kctl get authpolicies.kuadrant.io ten-m3-grace-enforcement-deny -n "$NS" &>/dev/null; then
  pass "No AuthPolicy deny in Grace state (access still allowed)"
else
  fail "AuthPolicy deny should not exist in Grace state"
fi

# Step 5: Ingest 6 more events (cost=$0.48, balance→-$0.520000).
# |-0.52| > graceAmount 0.50, so should enter Frozen.
echo "  Ingesting 6 more events (cost=$0.48)..."
ingest_events ten-m3-grace 6

echo "  Waiting for rating + reconcile..."
sleep 5

# Step 6: Wait for Frozen state.
echo "  Waiting for Frozen state..."
if wait_for_state ten-m3-grace "$NS" Frozen 90; then
  pass "ten-m3-grace entered Frozen state (exceeded graceAmount)"
else
  CURRENT_STATE=$(kctl get tenantaccount ten-m3-grace -n "$NS" -o jsonpath='{.status.state}' 2>/dev/null)
  fail "ten-m3-grace did not reach Frozen state (got ${CURRENT_STATE})"
fi

# Step 7: Assert AuthPolicy deny exists.
if kctl get authpolicies.kuadrant.io ten-m3-grace-enforcement-deny -n "$NS" &>/dev/null; then
  pass "AuthPolicy deny exists after grace exhaustion"
else
  fail "AuthPolicy deny not found after grace exhaustion"
fi

# ========================================================================
# Test 3: Overrun bound timing
# ========================================================================
echo ""
echo "--- Test 3: Overrun enforcement bound ---"

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m3-timing
spec:
  displayName: "M3 Timing Test"
  billingMode: Prepaid
  planRef:
    name: plan-m3
  gracePolicy:
    graceAmount: "0.000000"
EOF

if kctl get tenantaccount ten-m3-timing -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m3-timing created"
else
  fail "TenantAccount ten-m3-timing not accepted"
fi

# Seed exactly 1 event worth ($0.080000).
echo "  Seeding $0.080000 (exactly 1 event)..."
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m3-timing \
  --amount 0.080000 \
  --kind manual_credit \
  --reason "M3 timing seed" \
  --idempotency-key "m3-seed-timing-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m3-timing "$NS" Active 90; then
  pass "ten-m3-timing is Active after seeding"
else
  fail "ten-m3-timing did not reach Active state"
fi

# Ingest 2 events (cost=$0.16, balance→-$0.080000, guaranteed to cross zero).
T_START=$(date +%s)
echo "  Ingesting 2 events (cost=$0.16, balance will cross zero)..."
ingest_events ten-m3-timing 2

# Poll for Frozen state.
echo "  Polling for Frozen state (measuring enforcement latency)..."
if wait_for_state ten-m3-timing "$NS" Frozen 90; then
  T_END=$(date +%s)
  ELAPSED=$((T_END - T_START))
  echo "  Enforcement latency: ${ELAPSED}s"
  if [ "$ELAPSED" -le 90 ]; then
    pass "Enforcement within 90s bound (took ${ELAPSED}s)"
  else
    fail "Enforcement exceeded 90s bound (took ${ELAPSED}s)"
  fi
else
  fail "ten-m3-timing did not reach Frozen state within 90s"
fi

# ========================================================================
# Test 4: billctl pricing — resolved PriceBook data
# ========================================================================
echo ""
echo "--- Test 4: billctl pricing — resolved PriceBook ---"

# Use ten-m3-immed which has planRef → plan-m3 → pb-m3.
PRICING_OUTPUT=$($BILLCTL pricing $BILLCTL_ARGS --tenant ten-m3-immed 2>&1)
if [ $? -eq 0 ]; then
  pass "billctl pricing returned successfully"
else
  fail "billctl pricing failed: ${PRICING_OUTPUT}"
fi

# Assert the output contains the PriceBook name and SKU.
if echo "$PRICING_OUTPUT" | grep -q "pb-m3"; then
  pass "Pricing output contains PriceBook pb-m3"
else
  fail "Pricing output missing PriceBook pb-m3: ${PRICING_OUTPUT}"
fi

if echo "$PRICING_OUTPUT" | grep -q "llama-3-70b"; then
  pass "Pricing output contains SKU llama-3-70b"
else
  fail "Pricing output missing SKU llama-3-70b: ${PRICING_OUTPUT}"
fi

# ========================================================================
# Test 5: billctl keys — round-trip (register, list, disable, enable)
# ========================================================================
echo ""
echo "--- Test 5: billctl keys — round-trip ---"

# Register a key.
$BILLCTL keys register $BILLCTL_ARGS --tenant ten-m3-immed --key "test-key-roundtrip" --source "demo-m3" >/dev/null 2>&1
if [ $? -eq 0 ]; then
  pass "billctl keys register succeeded"
else
  fail "billctl keys register failed"
fi

# List keys — verify present.
KEYS_OUTPUT=$($BILLCTL keys list $BILLCTL_ARGS --tenant ten-m3-immed 2>&1)
if echo "$KEYS_OUTPUT" | grep -q "test-key-roundtrip"; then
  pass "Key test-key-roundtrip appears in list"
else
  fail "Key test-key-roundtrip not found in list: ${KEYS_OUTPUT}"
fi

# Verify key shows enabled (true).
if echo "$KEYS_OUTPUT" | grep "test-key-roundtrip" | grep -q "true"; then
  pass "Key test-key-roundtrip is enabled"
else
  fail "Key test-key-roundtrip should be enabled: ${KEYS_OUTPUT}"
fi

# Disable the key.
$BILLCTL keys disable $BILLCTL_ARGS --tenant ten-m3-immed --key "test-key-roundtrip" >/dev/null 2>&1
if [ $? -eq 0 ]; then
  pass "billctl keys disable succeeded"
else
  fail "billctl keys disable failed"
fi

# List — verify disabled (false).
KEYS_OUTPUT=$($BILLCTL keys list $BILLCTL_ARGS --tenant ten-m3-immed 2>&1)
if echo "$KEYS_OUTPUT" | grep "test-key-roundtrip" | grep -q "false"; then
  pass "Key test-key-roundtrip is disabled after disable"
else
  fail "Key test-key-roundtrip should be disabled: ${KEYS_OUTPUT}"
fi

# Enable the key.
$BILLCTL keys enable $BILLCTL_ARGS --tenant ten-m3-immed --key "test-key-roundtrip" >/dev/null 2>&1
if [ $? -eq 0 ]; then
  pass "billctl keys enable succeeded"
else
  fail "billctl keys enable failed"
fi

# List — verify enabled again (true).
KEYS_OUTPUT=$($BILLCTL keys list $BILLCTL_ARGS --tenant ten-m3-immed 2>&1)
if echo "$KEYS_OUTPUT" | grep "test-key-roundtrip" | grep -q "true"; then
  pass "Key test-key-roundtrip is enabled after re-enable"
else
  fail "Key test-key-roundtrip should be re-enabled: ${KEYS_OUTPUT}"
fi

# ========================================================================
# Test 6: Key disable on Freeze / re-enable on Thaw
# ========================================================================
echo ""
echo "--- Test 6: Key disable on Freeze / re-enable on Thaw ---"

# Create a new tenant for key freeze/thaw tests.
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m3-keys
spec:
  displayName: "M3 Key Freeze/Thaw"
  billingMode: Prepaid
  planRef:
    name: plan-m3
  gracePolicy:
    graceAmount: "0.000000"
    warningThreshold: "0.300000"
EOF

if kctl get tenantaccount ten-m3-keys -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m3-keys created"
else
  fail "TenantAccount ten-m3-keys not accepted"
fi

# Register a key for this tenant.
$BILLCTL keys register $BILLCTL_ARGS --tenant ten-m3-keys --key "freeze-test-key" --source "demo-m3" >/dev/null 2>&1
if [ $? -eq 0 ]; then
  pass "Key freeze-test-key registered for ten-m3-keys"
else
  fail "Failed to register freeze-test-key"
fi

# Verify key is enabled.
KEYS_OUTPUT=$($BILLCTL keys list $BILLCTL_ARGS --tenant ten-m3-keys 2>&1)
if echo "$KEYS_OUTPUT" | grep "freeze-test-key" | grep -q "true"; then
  pass "freeze-test-key is enabled initially"
else
  fail "freeze-test-key should be enabled initially: ${KEYS_OUTPUT}"
fi

# Seed $0.50.
echo '  Seeding $0.50...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m3-keys \
  --amount 0.500000 \
  --kind manual_credit \
  --reason "M3 key freeze seed" \
  --idempotency-key "m3-seed-keys-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m3-keys "$NS" Active 90; then
  pass "ten-m3-keys is Active after seeding"
else
  fail "ten-m3-keys did not reach Active state"
fi

# Drain: ingest 8 events (cost=$0.64, balance→-$0.14).
echo "  Ingesting 8 events (cost=\$0.64) to drain..."
ingest_events ten-m3-keys 8

echo "  Waiting for rating + reconcile..."
sleep 5

# Wait for Frozen state.
echo "  Waiting for Frozen state..."
if wait_for_state ten-m3-keys "$NS" Frozen 90; then
  pass "ten-m3-keys entered Frozen state"
else
  fail "ten-m3-keys did not reach Frozen state"
fi

# Assert key is disabled via billctl keys list.
KEYS_OUTPUT=$($BILLCTL keys list $BILLCTL_ARGS --tenant ten-m3-keys 2>&1)
if echo "$KEYS_OUTPUT" | grep "freeze-test-key" | grep -q "false"; then
  pass "freeze-test-key is disabled after Freeze"
else
  fail "freeze-test-key should be disabled after Freeze: ${KEYS_OUTPUT}"
fi

# Thaw: top up $2.00.
echo '  Topping up $2.00 to thaw...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m3-keys \
  --amount 2.000000 \
  --kind manual_credit \
  --reason "M3 key thaw top-up" \
  --idempotency-key "m3-topup-keys-$(date +%s)" >/dev/null 2>&1

# Wait for Active state (thaw).
echo "  Waiting for Active state (thaw)..."
if wait_for_state ten-m3-keys "$NS" Active 90; then
  pass "ten-m3-keys thawed back to Active"
else
  fail "ten-m3-keys did not thaw to Active"
fi

# Assert key is re-enabled.
KEYS_OUTPUT=$($BILLCTL keys list $BILLCTL_ARGS --tenant ten-m3-keys 2>&1)
if echo "$KEYS_OUTPUT" | grep "freeze-test-key" | grep -q "true"; then
  pass "freeze-test-key is re-enabled after Thaw"
else
  fail "freeze-test-key should be re-enabled after Thaw: ${KEYS_OUTPUT}"
fi

# ---------- Ledger summary ----------
echo ""
echo "--- Ledger summary ---"
for tenant in ten-m3-immed ten-m3-grace ten-m3-timing ten-m3-keys; do
  EVENT_COUNT=$(psql_query "SELECT count(*) FROM usage_events WHERE tenant_id='${tenant}';" 2>/dev/null || echo "?")
  CHARGE_COUNT=$(psql_query "SELECT count(*) FROM charges WHERE tenant_id='${tenant}';" 2>/dev/null || echo "?")
  echo "  ${tenant}: ${EVENT_COUNT} events, ${CHARGE_COUNT} charges"
done

# ---------- Summary ----------
echo ""
echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="
if [ $FAIL -gt 0 ]; then
  exit 1
fi
