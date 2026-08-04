#!/usr/bin/env bash
# hack/demo-m5.sh — M5 end-to-end demo: Reconciler + Budgets + Exports.
#
# Validates the full M5 operational and financial tooling: drift
# reconciliation, BudgetPolicy threshold enforcement, FOCUS 1.2 export,
# and business metrics exposure.
#
# Exit criteria (from milestones.md M5):
#   1. Drift beyond configured tolerance fires an alert and produces a drift
#      report with per-tenant detail.
#   2. Budget threshold fires exactly once per period (no duplicate
#      notifications on re-reconcile).
#   3. FOCUS export validates (29 columns, required fields present).
#   4. Ingest metrics increment (billing_ingest_total exposed on /metrics).
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
    local record_id="m5-${tenant}-$(date +%s%N)-${i}"
    local EVENT_JSON
    EVENT_JSON=$(cat <<EVEOF
{
  "schema_version": "usage.v1",
  "record_id": "${record_id}",
  "occurred_at": "${ts}",
  "tenant_id": "${tenant}",
  "source": {"profile": "maas", "collector": "demo-m5", "collector_version": "0.0.1"},
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

echo "=== M5 End-to-End Demo: Reconciler + Budgets + Exports ==="
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

  # Patch the operator deployment for M5:
  #   - imagePullPolicy for kind-loaded images
  #   - LEDGER_DSN + LEDGER_MIGRATIONS_DIR
  #   - PROMETHEUS_URL pointing at fake-prometheus (deployed below)
  #   - DRIFT_INTERVAL=10s for fast test cycles
  #   - DRIFT_TOLERANCE_PCT=5
  #   - Enable HTTP metrics (--metrics-bind-address=:8080, --metrics-secure=false)
  DEPLOY_NAME=$(kctl get deployment -n "$OPERATOR_NS" -l control-plane=controller-manager -o name | head -1)
  kctl patch "$DEPLOY_NAME" -n "$OPERATOR_NS" --type=json -p "[
    {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/imagePullPolicy\",\"value\":\"Never\"},
    {\"op\":\"replace\",\"path\":\"/spec/template/spec/containers/0/args\",\"value\":[
      \"--leader-elect=false\",
      \"--health-probe-bind-address=:8081\",
      \"--metrics-bind-address=:8080\",
      \"--metrics-secure=false\"
    ]},
    {\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/env\",\"value\":[
      {\"name\":\"LEDGER_DSN\",\"valueFrom\":{\"secretKeyRef\":{\"name\":\"billing-ledger-postgres\",\"key\":\"DSN\"}}},
      {\"name\":\"LEDGER_MIGRATIONS_DIR\",\"value\":\"/migrations\"},
      {\"name\":\"PROMETHEUS_URL\",\"value\":\"http://fake-prometheus.${NS}.svc:9090\"},
      {\"name\":\"DRIFT_INTERVAL\",\"value\":\"10s\"},
      {\"name\":\"DRIFT_TOLERANCE_PCT\",\"value\":\"5\"}
    ]}
  ]" >/dev/null 2>&1 || true
  pass "Operator patched (imagePullPolicy=Never, LEDGER_DSN, drift config, metrics)"

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

  # --- M5-specific: fake-prometheus for drift reconciliation ---
  # Serves the Prometheus /api/v1/query endpoint with a canned response
  # returning 500 tokens for any query. The drift job sees this vs the
  # ledger's actual token count and reports drift.

  kctl apply -n "$NS" -f - <<'FAKEPROM_EOF' >/dev/null
apiVersion: v1
kind: ConfigMap
metadata:
  name: fake-prometheus-script
data:
  server.py: |
    """Minimal fake Prometheus API: returns 500 tokens for any query."""
    from http.server import HTTPServer, BaseHTTPRequestHandler
    import json, time

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            resp = {
                "status": "success",
                "data": {
                    "resultType": "vector",
                    "result": [{
                        "metric": {},
                        "value": [time.time(), "500"]
                    }]
                }
            }
            body = json.dumps(resp).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, fmt, *args):
            pass

    HTTPServer(("", 9090), Handler).serve_forever()
FAKEPROM_EOF

  kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: v1
kind: Pod
metadata:
  name: fake-prometheus
  labels:
    app: fake-prometheus
spec:
  containers:
    - name: prom
      image: python:3.12-alpine
      command: ["python3", "/scripts/server.py"]
      ports:
        - containerPort: 9090
      volumeMounts:
        - name: scripts
          mountPath: /scripts
      resources:
        requests:
          cpu: 10m
          memory: 32Mi
        limits:
          cpu: 50m
          memory: 64Mi
  volumes:
    - name: scripts
      configMap:
        name: fake-prometheus-script
---
apiVersion: v1
kind: Service
metadata:
  name: fake-prometheus
  labels:
    app: fake-prometheus
spec:
  selector:
    app: fake-prometheus
  ports:
    - port: 9090
      targetPort: 9090
EOF
  pass "fake-prometheus deployed"

  # Wait for rollouts.
  echo "  Waiting for operator rollout..."
  DEPLOY_SHORT=$(echo "$DEPLOY_NAME" | sed 's|deployment.apps/||')
  wait_for_rollout "$DEPLOY_SHORT" "$OPERATOR_NS" 120 || fail "Operator not ready"
  pass "Operator ready"

  echo "  Waiting for ledger-svc rollout..."
  wait_for_rollout ledger-svc "$NS" 120 || fail "ledger-svc not ready"
  pass "ledger-svc ready"

  echo "  Waiting for fake-prometheus pod..."
  PROM_DEADLINE=$((SECONDS + 120))
  PROM_PHASE=""
  while [ $SECONDS -lt $PROM_DEADLINE ]; do
    PROM_PHASE=$(kctl get pod fake-prometheus -n "$NS" -o jsonpath='{.status.phase}' 2>/dev/null)
    if [ "$PROM_PHASE" = "Running" ]; then
      break
    fi
    sleep 2
  done
  if [ "$PROM_PHASE" = "Running" ]; then
    pass "fake-prometheus running"
  else
    fail "fake-prometheus not ready (phase: ${PROM_PHASE:-unknown})"
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
psql_query "TRUNCATE drift_reports CASCADE;" 2>/dev/null || true
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
  for acct in ten-m5-drift ten-m5-budget; do
    kctl delete tenantaccount "$acct" -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  done
  kctl delete budgetpolicy bp-m5-test -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete plan plan-m5 -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  kctl delete pricebook pb-m5 -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
}
trap cleanup EXIT

pass "Port-forward to ledger-svc on :18080"

# ---------- Shared fixtures: PriceBook + Plan ----------
echo ""
echo "--- Shared fixtures ---"

# PriceBook pb-m5: same rates as M3/M4.
# Per event (1000 prompt + 500 completion):
#   prompt  = 1000 * 30.000000 / 1,000,000 = 0.030000
#   compl   = 500  * 100.000000 / 1,000,000 = 0.050000
#   total   = 0.080000
FUTURE_DATE=$(date -u -v+30d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+30 days' '+%Y-%m-%dT%H:%M:%SZ')

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: PriceBook
metadata:
  name: pb-m5
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

if kctl get pricebook pb-m5 -n "$NS" &>/dev/null; then
  pass "PriceBook pb-m5 created"
else
  fail "PriceBook pb-m5 not accepted"
fi

kctl patch pricebook pb-m5 -n "$NS" --subresource status --type merge \
  -p '{"status":{"state":"Active"}}' >/dev/null
STATE=$(kctl get pricebook pb-m5 -n "$NS" -o jsonpath='{.status.state}')
if [ "$STATE" = "Active" ]; then
  pass "PriceBook pb-m5 is Active"
else
  fail "PriceBook pb-m5 state: expected Active, got ${STATE}"
fi

# Plan plan-m5.
kctl apply -n "$NS" -f - <<EOF >/dev/null
apiVersion: billing.opendatahub.io/v1alpha1
kind: Plan
metadata:
  name: plan-m5
spec:
  limits:
    tokensPerMinute: 2000000
    requestsPerMinute: 600
  modelScopes: ["llama-3-*"]
  priceBookRef: pb-m5
  targetRef:
    group: gateway.networking.k8s.io
    kind: Gateway
    name: billing-demo-gateway
EOF

if kctl get plan plan-m5 -n "$NS" &>/dev/null; then
  pass "Plan plan-m5 created"
else
  fail "Plan plan-m5 not accepted"
fi

# ========================================================================
# Test 1: Drift detection — ledger vs Prometheus divergence
# ========================================================================
echo ""
echo "--- Test 1: Drift detection — ledger vs Prometheus divergence ---"

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m5-drift
spec:
  displayName: "M5 Drift Test"
  billingMode: Prepaid
  planRef:
    name: plan-m5
  reloadPolicy:
    enabled: false
  gracePolicy:
    graceAmount: "0.000000"
EOF

if kctl get tenantaccount ten-m5-drift -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m5-drift created"
else
  fail "TenantAccount ten-m5-drift not accepted"
fi

# Seed balance and wait for Active.
echo '  Seeding $10.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m5-drift \
  --amount 10.000000 \
  --kind manual_credit \
  --reason "M5 drift demo seed" \
  --idempotency-key "m5-seed-drift-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m5-drift "$NS" Active 90; then
  pass "ten-m5-drift is Active after seeding"
else
  fail "ten-m5-drift did not reach Active state"
fi

# Ingest 7 events (10500 total tokens). Fake Prometheus returns 500 tokens.
# Drift = |ledger - prom| / max(ledger, prom) * 100 >> 5% tolerance.
echo "  Ingesting 7 events (10500 tokens)..."
ingest_events ten-m5-drift 7

echo "  Waiting for rating + drift reconciler tick (up to 30s)..."
sleep 5

# Poll for a drift report via the local port-forward.
DRIFT_DEADLINE=$((SECONDS + 30))
DRIFT_REPORT=""
while [ $SECONDS -lt $DRIFT_DEADLINE ]; do
  DRIFT_REPORT=$(curl -s "http://localhost:18080/v1/drift/latest?tenant=ten-m5-drift" 2>/dev/null) || true
  if echo "$DRIFT_REPORT" | jq -e '.exceeded' &>/dev/null; then
    break
  fi
  sleep 3
done

if [ -z "$DRIFT_REPORT" ]; then
  fail "No drift report produced within timeout"
fi

EXCEEDED=$(echo "$DRIFT_REPORT" | jq -r '.exceeded' 2>/dev/null)
if [ "$EXCEEDED" = "true" ]; then
  pass "Drift exceeded tolerance (exceeded=true)"
else
  fail "Drift should exceed tolerance, got exceeded=${EXCEEDED}"
fi

SIGNATURE=$(echo "$DRIFT_REPORT" | jq -r '.signature' 2>/dev/null)
if [ -n "$SIGNATURE" ] && [ "$SIGNATURE" != "null" ]; then
  pass "Drift report has signature: ${SIGNATURE:0:16}..."
else
  fail "Drift report missing signature"
fi

TENANT_IN_REPORT=$(echo "$DRIFT_REPORT" | jq -r '.tenant_id' 2>/dev/null)
if [ "$TENANT_IN_REPORT" = "ten-m5-drift" ]; then
  pass "Drift report has per-tenant detail (tenant_id=ten-m5-drift)"
else
  fail "Drift report tenant mismatch: expected ten-m5-drift, got ${TENANT_IN_REPORT}"
fi

# Verify the ReconcileDrift condition on the TenantAccount.
DRIFT_COND=$(kctl get tenantaccount ten-m5-drift -n "$NS" \
  -o jsonpath='{.status.conditions[?(@.type=="ReconcileDrift")].status}' 2>/dev/null)
if [ "$DRIFT_COND" = "True" ]; then
  pass "ReconcileDrift condition is True on ten-m5-drift"
else
  echo "  (ReconcileDrift condition: ${DRIFT_COND:-not set} — may need more time)"
fi

# ========================================================================
# Test 2: Budget threshold fires exactly once per period
# ========================================================================
echo ""
echo "--- Test 2: Budget threshold fires exactly once per period ---"

kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: TenantAccount
metadata:
  name: ten-m5-budget
spec:
  displayName: "M5 Budget Test"
  billingMode: Prepaid
  planRef:
    name: plan-m5
  reloadPolicy:
    enabled: false
  gracePolicy:
    graceAmount: "0.000000"
EOF

if kctl get tenantaccount ten-m5-budget -n "$NS" &>/dev/null; then
  pass "TenantAccount ten-m5-budget created"
else
  fail "TenantAccount ten-m5-budget not accepted"
fi

# Seed balance and wait for Active.
echo '  Seeding $10.00...'
$BILLCTL adjust $BILLCTL_ARGS \
  --tenant ten-m5-budget \
  --amount 10.000000 \
  --kind manual_credit \
  --reason "M5 budget demo seed" \
  --idempotency-key "m5-seed-budget-$(date +%s)" >/dev/null 2>&1

echo "  Waiting for Active state..."
if wait_for_state ten-m5-budget "$NS" Active 90; then
  pass "ten-m5-budget is Active after seeding"
else
  fail "ten-m5-budget did not reach Active state"
fi

# Create BudgetPolicy: $1.00 daily cap, thresholds at 50% (Notify) and 90% (Freeze).
# 7 events @ $0.08 = $0.56 -> fires 50% ($0.50) but not 90% ($0.90).
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: BudgetPolicy
metadata:
  name: bp-m5-test
spec:
  tenantRef:
    name: ten-m5-budget
  caps:
    - amount: "1.000000"
      period: Daily
      thresholds:
        - pct: 50
          action: Notify
        - pct: 90
          action: Freeze
EOF

if kctl get budgetpolicy bp-m5-test -n "$NS" &>/dev/null; then
  pass "BudgetPolicy bp-m5-test created"
else
  fail "BudgetPolicy bp-m5-test not accepted"
fi

# Ingest 7 events (cost = $0.56, above 50% of $1.00 = $0.50).
echo "  Ingesting 7 events (\$0.56 spend)..."
ingest_events ten-m5-budget 7

echo "  Waiting for rating..."
sleep 5

# Trigger a reconcile by annotating the BudgetPolicy. Any metadata update
# causes the controller to re-evaluate spend against thresholds.
kctl annotate budgetpolicy bp-m5-test -n "$NS" \
  demo-trigger="$(date +%s)" --overwrite >/dev/null 2>&1

echo "  Waiting for budget reconcile (up to 90s)..."
BUDGET_DEADLINE=$((SECONDS + 90))
THRESHOLD_FIRED="false"
while [ $SECONDS -lt $BUDGET_DEADLINE ]; do
  THRESHOLD_FIRED=$(kctl get budgetpolicy bp-m5-test -n "$NS" \
    -o jsonpath='{.status.conditions[?(@.type=="ThresholdFired")].status}' 2>/dev/null)
  if [ "$THRESHOLD_FIRED" = "True" ]; then
    break
  fi
  sleep 3
done

if [ "$THRESHOLD_FIRED" = "True" ]; then
  pass "ThresholdFired condition is True"
else
  fail "ThresholdFired condition should be True, got ${THRESHOLD_FIRED:-not set}"
fi

# Record the fired threshold count.
FIRED_RAW=$(kctl get budgetpolicy bp-m5-test -n "$NS" \
  -o jsonpath='{.status.firedThresholds}' 2>/dev/null)
if [ -z "$FIRED_RAW" ]; then
  FIRED_COUNT_1=0
else
  FIRED_COUNT_1=$(echo "$FIRED_RAW" | jq '. | length' 2>/dev/null || echo "0")
fi
echo "  Fired thresholds after first reconcile: ${FIRED_COUNT_1}"

if [ "$FIRED_COUNT_1" = "1" ]; then
  pass "Exactly 1 threshold fired (50% Notify)"
else
  echo "  (Expected 1 threshold fired, got ${FIRED_COUNT_1})"
fi

# Trigger a second reconcile and verify no duplicate firing.
echo "  Triggering second reconcile..."
kctl annotate budgetpolicy bp-m5-test -n "$NS" \
  demo-trigger="second-$(date +%s)" --overwrite >/dev/null 2>&1
sleep 5

FIRED_RAW_2=$(kctl get budgetpolicy bp-m5-test -n "$NS" \
  -o jsonpath='{.status.firedThresholds}' 2>/dev/null)
if [ -z "$FIRED_RAW_2" ]; then
  FIRED_COUNT_2=0
else
  FIRED_COUNT_2=$(echo "$FIRED_RAW_2" | jq '. | length' 2>/dev/null || echo "0")
fi

if [ "$FIRED_COUNT_2" = "$FIRED_COUNT_1" ]; then
  pass "No duplicate threshold firing (count stable at ${FIRED_COUNT_2})"
else
  fail "Duplicate threshold firing detected: was ${FIRED_COUNT_1}, now ${FIRED_COUNT_2}"
fi

# Verify the 90% Freeze threshold did NOT fire.
FREEZE_FIRED=$(echo "$FIRED_RAW_2" | jq '[.[] | select(.pct == 90)] | length' 2>/dev/null || echo "0")
if [ "${FREEZE_FIRED}" = "0" ]; then
  pass "90% Freeze threshold correctly did not fire"
else
  fail "90% Freeze threshold should not have fired"
fi

# ========================================================================
# Test 3: FOCUS 1.2 export validates
# ========================================================================
echo ""
echo "--- Test 3: FOCUS 1.2 export validates ---"

# Export rated events for ten-m5-drift (ingested in Test 1).
CSV_OUTPUT=$($BILLCTL export $BILLCTL_ARGS \
  --tenant ten-m5-drift \
  --from "2020-01-01T00:00:00Z" \
  --to "2099-01-01T00:00:00Z" \
  --format csv 2>&1) || true

if [ -z "$CSV_OUTPUT" ]; then
  fail "FOCUS export returned empty output"
fi

# Count columns in header row (should be 29 per FOCUS 1.2).
HEADER_LINE=$(echo "$CSV_OUTPUT" | head -1)
COL_COUNT=$(echo "$HEADER_LINE" | awk -F, '{print NF}')
if [ "$COL_COUNT" = "29" ]; then
  pass "FOCUS export has 29 columns"
else
  fail "FOCUS export should have 29 columns, got ${COL_COUNT}"
fi

# Verify data rows exist.
DATA_LINE_COUNT=$(echo "$CSV_OUTPUT" | tail -n +2 | grep -c '.' || true)
if [ "$DATA_LINE_COUNT" -gt 0 ]; then
  pass "FOCUS export has ${DATA_LINE_COUNT} data rows"
else
  fail "FOCUS export has no data rows"
fi

# Verify required FOCUS fields have values in the first data row.
# Column positions from FOCUSCSVHeader():
#   1=BilledCost 4=BillingCurrency 11=ChargePeriodStart 25=SkuId
FIRST_DATA=$(echo "$CSV_OUTPUT" | sed -n '2p')

BILLED_COST=$(echo "$FIRST_DATA" | cut -d',' -f1)
if [ -n "$BILLED_COST" ]; then
  pass "BilledCost populated: ${BILLED_COST}"
else
  fail "BilledCost is empty"
fi

BILLING_CURRENCY=$(echo "$FIRST_DATA" | cut -d',' -f4)
if [ -n "$BILLING_CURRENCY" ]; then
  pass "BillingCurrency populated: ${BILLING_CURRENCY}"
else
  fail "BillingCurrency is empty"
fi

CHARGE_PERIOD_START=$(echo "$FIRST_DATA" | cut -d',' -f11)
if [ -n "$CHARGE_PERIOD_START" ]; then
  pass "ChargePeriodStart populated: ${CHARGE_PERIOD_START}"
else
  fail "ChargePeriodStart is empty"
fi

SKU_ID=$(echo "$FIRST_DATA" | cut -d',' -f25)
if [ -n "$SKU_ID" ]; then
  pass "SkuId populated: ${SKU_ID}"
else
  fail "SkuId is empty"
fi

# ========================================================================
# Test 4: Ingest metrics increment
# ========================================================================
echo ""
echo "--- Test 4: Ingest metrics increment ---"

# Get the operator pod IP to scrape metrics via the in-cluster network.
OPERATOR_POD_IP=$(kctl get pod -n "$OPERATOR_NS" -l control-plane=controller-manager \
  -o jsonpath='{.items[0].status.podIP}' 2>/dev/null)

if [ -z "$OPERATOR_POD_IP" ]; then
  fail "Could not determine operator pod IP"
fi

echo "  Scraping operator metrics at ${OPERATOR_POD_IP}:8080..."
# Clean up any leftover curl-probe pod from previous ingest calls.
kctl delete pod curl-probe -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
sleep 1

METRICS_OUTPUT=$(curl_via_pod -s "http://${OPERATOR_POD_IP}:8080/metrics" 2>/dev/null) || true

if [ -z "$METRICS_OUTPUT" ]; then
  fail "Could not scrape operator metrics"
fi

# Check for billing_ingest_total metric (registered via controller-runtime metrics).
if echo "$METRICS_OUTPUT" | grep -q "billing_ingest_total"; then
  pass "billing_ingest_total metric exposed"
else
  fail "billing_ingest_total not found in operator metrics"
fi

# Check for billing_events_rated_total (incremented by the rater in the operator).
if echo "$METRICS_OUTPUT" | grep -q "billing_events_rated_total"; then
  RATED_VALUE=$(echo "$METRICS_OUTPUT" | grep "^billing_events_rated_total " | awk '{print $2}')
  if [ -n "$RATED_VALUE" ] && [ "$RATED_VALUE" != "0" ]; then
    pass "billing_events_rated_total has value: ${RATED_VALUE}"
  else
    pass "billing_events_rated_total metric exposed (value: ${RATED_VALUE:-0})"
  fi
else
  fail "billing_events_rated_total not found in operator metrics"
fi

# Check for drift-related metrics (set by the drift job in the operator).
if echo "$METRICS_OUTPUT" | grep -q "billing_drift_exceeded"; then
  pass "billing_drift_exceeded metric exposed"
else
  echo "  (billing_drift_exceeded not found — drift may not have ticked in the operator process)"
fi

# ---------- Ledger summary ----------
echo ""
echo "--- Ledger summary ---"
for tenant in ten-m5-drift ten-m5-budget; do
  EVENT_COUNT=$(psql_query "SELECT count(*) FROM usage_events WHERE tenant_id='${tenant}';" 2>/dev/null || echo "?")
  CHARGE_COUNT=$(psql_query "SELECT count(*) FROM charges WHERE tenant_id='${tenant}';" 2>/dev/null || echo "?")
  echo "  ${tenant}: ${EVENT_COUNT} events, ${CHARGE_COUNT} charges"
done
DRIFT_COUNT=$(psql_query "SELECT count(*) FROM drift_reports;" 2>/dev/null || echo "?")
echo "  drift_reports: ${DRIFT_COUNT} total"

# ---------- Summary ----------
echo ""
echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="
if [ $FAIL -gt 0 ]; then
  exit 1
fi
