#!/usr/bin/env bash
# hack/test-g2-capacity.sh — G2 Phase 2: capacity feasibility + Kueue CQ compilation.
# Prereqs: kind cluster, Kueue installed, Reservation CRD registered.
set -euo pipefail
KUBECTL="${KUBECTL:-kubectl}"; CTX="${CLUSTER_CONTEXT:-kind-platform-billing-dev}"
NS="${DEMO_NS:-billing-demo}"; PASS=0; FAIL=0
pass() { PASS=$((PASS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1" >&2; echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="; exit 1; }
info() { echo "--- $* ---"; }
kctl() { "$KUBECTL" --context "$CTX" "$@"; }

wait_for_reservation_state() {
  local name="$1" expected="$2" timeout="${3:-60}"
  local deadline=$((SECONDS + timeout)) state=""
  while [ $SECONDS -lt $deadline ]; do
    state=$(kctl get reservation "$name" -n "$NS" -o jsonpath='{.status.state}' 2>/dev/null)
    [ "$state" = "$expected" ] && return 0
    sleep 2
  done
  echo "  timeout: $name state=${state:-<empty>} (expected $expected)" >&2
  return 1
}

wait_for_cq() {
  local name="$1" timeout="${2:-60}"
  local deadline=$((SECONDS + timeout))
  while [ $SECONDS -lt $deadline ]; do
    kctl get clusterqueue "$name" &>/dev/null && return 0
    sleep 2
  done
  return 1
}

WINDOW_START=$(date -u -v+1H '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+1 hour' '+%Y-%m-%dT%H:%M:%SZ')
WINDOW_END=$(date -u -v+30d '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -d '+30 days' '+%Y-%m-%dT%H:%M:%SZ')
cleanup() {
  for r in res-g-1 res-g-2 res-p-1 res-g-over; do
    kctl delete reservation "$r" -n "$NS" --ignore-not-found >/dev/null 2>&1 || true
  done
  for cq in cap-a-guaranteed cap-a-preemptible cap-b-guaranteed; do
    kctl delete clusterqueue "$cq" --ignore-not-found >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

echo "=== G2 Phase 2: Capacity Feasibility + Kueue CQ Compilation ==="
echo "  Context: ${CTX}  Namespace: ${NS}"
echo ""

# ---------- Prerequisites ----------
info "Prerequisites"
"$KUBECTL" cluster-info --context "$CTX" &>/dev/null || fail "Cluster ${CTX} not reachable"
pass "Cluster reachable"
kctl get crd reservations.billing.opendatahub.io &>/dev/null || fail "Reservation CRD not registered"
pass "Reservation CRD registered"
kctl get namespace kueue-system &>/dev/null || fail "Kueue not installed"
pass "Kueue installed"

# ---------- Setup: label node + ResourceFlavor ----------
info "Setup"
NODE_NAME=$(kctl get nodes -o jsonpath='{.items[0].metadata.name}')
kctl label node "$NODE_NAME" nvidia.com/gpu.product=H200 nvidia.com/gpu.count=4 \
  topology.kubernetes.io/region=us-east --overwrite >/dev/null
pass "Node ${NODE_NAME} labeled (H200, count=4, region=us-east)"
kctl create namespace "$NS" --dry-run=client -o yaml | kctl apply -f - >/dev/null
kctl apply -f - <<'EOF' >/dev/null
apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  name: gpu-h200
spec:
  nodeLabels:
    nvidia.com/gpu.product: "H200"
EOF
pass "ResourceFlavor gpu-h200 applied"

# ---------- Step 1: Guaranteed reservation (2 GPUs) ----------
info "Step 1: Guaranteed reservation (2 GPUs)"
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: Reservation
metadata:
  name: res-g-1
spec:
  tenantRef: cap-a
  sku: h200
  count: 2
  geo: us-east
  class: Guaranteed
  monthlyPrice: "5000.000000"
  window:
    start: "${WINDOW_START}"
    end: "${WINDOW_END}"
EOF
wait_for_reservation_state res-g-1 Confirmed 60 || fail "res-g-1 did not reach Confirmed"
pass "res-g-1 Confirmed"
wait_for_cq cap-a-guaranteed 30 || fail "CQ cap-a-guaranteed not created"
pass "CQ cap-a-guaranteed created"
MANAGED=$(kctl get clusterqueue cap-a-guaranteed \
  -o jsonpath='{.metadata.labels.billing\.opendatahub\.io/managed-by}' 2>/dev/null)
[ "$MANAGED" = "reservation-controller" ] \
  || fail "CQ managed-by: expected reservation-controller, got ${MANAGED}"
pass "CQ managed-by=reservation-controller"

# ---------- Step 2: Second Guaranteed reservation (2 more GPUs, same tenant) ----------
info "Step 2: Second Guaranteed reservation (2 more GPUs)"
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: Reservation
metadata:
  name: res-g-2
spec:
  tenantRef: cap-a
  sku: h200
  count: 2
  geo: us-east
  class: Guaranteed
  monthlyPrice: "5000.000000"
  window:
    start: "${WINDOW_START}"
    end: "${WINDOW_END}"
EOF
wait_for_reservation_state res-g-2 Confirmed 60 || fail "res-g-2 did not reach Confirmed"
pass "res-g-2 Confirmed"
CQ_GPU=$(kctl get clusterqueue cap-a-guaranteed \
  -o jsonpath='{.spec.resourceGroups[0].flavors[0].resources[?(@.name=="nvidia.com/gpu")].nominalQuota}' 2>/dev/null)
[ "$CQ_GPU" = "4" ] || fail "CQ GPU nominalQuota: expected 4, got ${CQ_GPU}"
pass "CQ cap-a-guaranteed nominalQuota=4"

# ---------- Step 3: Preemptible reservation (2 GPUs) ----------
info "Step 3: Preemptible reservation (2 GPUs)"
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: Reservation
metadata:
  name: res-p-1
spec:
  tenantRef: cap-a
  sku: h200
  count: 2
  geo: us-east
  class: Preemptible
  monthlyPrice: "2500.000000"
  window:
    start: "${WINDOW_START}"
    end: "${WINDOW_END}"
EOF
wait_for_cq cap-a-preemptible 60 || fail "CQ cap-a-preemptible not created"
pass "CQ cap-a-preemptible created"
CQ_CLASS=$(kctl get clusterqueue cap-a-preemptible \
  -o jsonpath='{.metadata.labels.billing\.opendatahub\.io/reservation-class}' 2>/dev/null)
[ "$CQ_CLASS" = "Preemptible" ] \
  || fail "CQ reservation-class: expected Preemptible, got ${CQ_CLASS}"
pass "CQ reservation-class=Preemptible"

# ---------- Step 4: Over-capacity Guaranteed reservation ----------
info "Step 4: Over-capacity Guaranteed (should stay Requested)"
kctl apply -n "$NS" -f - <<EOF
apiVersion: billing.opendatahub.io/v1alpha1
kind: Reservation
metadata:
  name: res-g-over
spec:
  tenantRef: cap-b
  sku: h200
  count: 2
  geo: us-east
  class: Guaranteed
  monthlyPrice: "5000.000000"
  window:
    start: "${WINDOW_START}"
    end: "${WINDOW_END}"
EOF
sleep 10  # allow controller reconciliation
STATE=$(kctl get reservation res-g-over -n "$NS" -o jsonpath='{.status.state}' 2>/dev/null)
[ "$STATE" = "Requested" ] || fail "res-g-over state: expected Requested, got ${STATE}"
pass "res-g-over stays Requested"
AVAIL=$(kctl get reservation res-g-over -n "$NS" \
  -o jsonpath='{.status.capacityCheck.available}' 2>/dev/null)
[ "$AVAIL" = "false" ] || fail "capacityCheck.available: expected false, got ${AVAIL}"
pass "res-g-over capacityCheck.available=false"
COND_REASON=$(kctl get reservation res-g-over -n "$NS" \
  -o jsonpath='{.status.conditions[?(@.type=="CapacityConfirmed")].reason}' 2>/dev/null)
[ "$COND_REASON" = "CapacityInsufficient" ] \
  || fail "CapacityConfirmed reason: expected CapacityInsufficient, got ${COND_REASON}"
pass "res-g-over CapacityInsufficient"

# ---------- Step 5: Preemption ----------
info "Step 5: Preemption (Guaranteed=capacity -> HoldAndDrain)"
STOP_POLICY=$(kctl get clusterqueue cap-a-preemptible \
  -o jsonpath='{.spec.stopPolicy}' 2>/dev/null)
[ "$STOP_POLICY" = "HoldAndDrain" ] \
  || fail "cap-a-preemptible StopPolicy: expected HoldAndDrain, got ${STOP_POLICY}"
pass "CQ cap-a-preemptible StopPolicy=HoldAndDrain"

# ---------- Summary ----------
echo ""
echo "=== RESULT: ${PASS} passed, ${FAIL} failed ==="
[ "$FAIL" -gt 0 ] && exit 1
exit 0
