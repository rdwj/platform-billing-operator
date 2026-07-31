#!/usr/bin/env bash
# hack/setup-kuadrant.sh — Install full Kuadrant stack into a kind cluster.
#
# Installs: cert-manager, Gateway API CRDs, Istio (minimal), Kuadrant operator
# (with Authorino, Limitador, DNS sub-operators via Helm), a Kuadrant CR, and a
# demo Gateway.
#
# Usage:
#   ./hack/setup-kuadrant.sh [CLUSTER_CONTEXT]
#
# Defaults to kind-platform-billing-dev if no context is provided.

set -euo pipefail

CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.16.1}"
GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.4.1}"
ISTIO_VERSION="${ISTIO_VERSION:-1.29.1}"
KUADRANT_VERSION="${KUADRANT_VERSION:-1.5.2}"
CLUSTER_CONTEXT="${1:-kind-platform-billing-dev}"
KUBECTL="${KUBECTL:-kubectl}"
DEMO_NS="${DEMO_NS:-billing-demo}"

info() { echo "=== $* ==="; }
kctl() { "$KUBECTL" --context "$CLUSTER_CONTEXT" "$@"; }

# ---------- cert-manager ----------
info "Installing cert-manager ${CERT_MANAGER_VERSION}"
kctl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
kctl wait --for=condition=Available deployment --all -n cert-manager --timeout=180s

# ---------- Gateway API CRDs ----------
info "Installing Gateway API CRDs ${GATEWAY_API_VERSION}"
kctl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"

# ---------- Istio ----------
info "Installing Istio ${ISTIO_VERSION}"
ISTIO_OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$ISTIO_OS" in
  darwin) ISTIO_OS="osx" ;;
esac
ISTIO_ARCH="$(uname -m)"
case "$ISTIO_ARCH" in
  x86_64|amd64) ISTIO_ARCH="amd64" ;;
  arm64|aarch64) ISTIO_ARCH="arm64" ;;
esac

ISTIO_DIR="$(mktemp -d)"
trap 'rm -rf "$ISTIO_DIR"' EXIT
curl -sL "https://github.com/istio/istio/releases/download/${ISTIO_VERSION}/istio-${ISTIO_VERSION}-${ISTIO_OS}-${ISTIO_ARCH}.tar.gz" \
  | tar xz -C "$ISTIO_DIR"
"${ISTIO_DIR}/istio-${ISTIO_VERSION}/bin/istioctl" install \
  --set profile=minimal \
  --context "$CLUSTER_CONTEXT" -y
kctl wait --for=condition=Available deployment/istiod -n istio-system --timeout=180s

# ---------- Kuadrant operator via Helm ----------
info "Installing Kuadrant operator ${KUADRANT_VERSION} via Helm"
helm repo add kuadrant https://kuadrant.io/helm-charts/ 2>/dev/null || true
helm repo update kuadrant
helm install kuadrant-operator kuadrant/kuadrant-operator \
  --version "${KUADRANT_VERSION}" \
  --namespace kuadrant-system --create-namespace \
  --kube-context "$CLUSTER_CONTEXT" \
  --timeout 1200s

# The Kuadrant chart has no configurable values for resource requests.
# On kind (single-node, 4 vCPU runner), the default 200m CPU request per
# operator exhausts allocatable CPU after cert-manager + Istio + 3 other
# Kuadrant operators are scheduled. Reduce requests so all 4 fit.
if [[ "$CLUSTER_CONTEXT" == kind-* ]]; then
  info "Reducing CPU requests for kind cluster"
  for deploy in authorino-operator dns-operator-controller-manager \
                limitador-operator-controller-manager kuadrant-operator-controller-manager; do
    kctl patch deployment "$deploy" -n kuadrant-system --type=json \
      -p '[{"op":"replace","path":"/spec/template/spec/containers/0/resources/requests/cpu","value":"50m"},
           {"op":"replace","path":"/spec/template/spec/containers/0/resources/limits/cpu","value":"200m"}]' \
      2>/dev/null || true
  done
fi

info "Waiting for Kuadrant operator pods to become ready"
DEADLINE=$((SECONDS + 1200))
while true; do
  if kctl wait --for=condition=Available deployment --all -n kuadrant-system --timeout=30s 2>/dev/null; then
    break
  fi
  if [ $SECONDS -ge $DEADLINE ]; then
    echo "ERROR: Kuadrant pods not ready after 1200s"
    echo "=== Pod status ==="
    kctl get pods -n kuadrant-system -o wide 2>/dev/null || true
    echo "=== Events ==="
    kctl get events -n kuadrant-system --sort-by='.lastTimestamp' 2>/dev/null || true
    echo "=== Describe non-ready pods ==="
    kctl get pods -n kuadrant-system -o json 2>/dev/null | python3 -c "
import sys, json
data = json.load(sys.stdin)
for pod in data.get('items', []):
    ready = all(cs.get('ready', False) for cs in pod.get('status', {}).get('containerStatuses', []))
    if not ready:
        print(f'--- {pod[\"metadata\"][\"name\"]} ---')
        for cs in pod.get('status', {}).get('containerStatuses', []):
            print(f'  {cs[\"name\"]}: ready={cs.get(\"ready\")}, state={cs.get(\"state\", {})}')
" 2>/dev/null || true
    exit 1
  fi
  echo "  Pods not ready yet (${SECONDS}s elapsed):"
  kctl get pods -n kuadrant-system --no-headers 2>/dev/null | while read line; do echo "    $line"; done
  sleep 30
done

# ---------- Kuadrant CR ----------
info "Creating Kuadrant CR"
kctl apply -n kuadrant-system -f - <<EOF
apiVersion: kuadrant.io/v1beta1
kind: Kuadrant
metadata:
  name: kuadrant
spec: {}
EOF

# ---------- Demo namespace + Gateway ----------
info "Creating demo namespace and Gateway"
kctl create namespace "$DEMO_NS" --dry-run=client -o yaml | kctl apply -f -
kctl apply -n "$DEMO_NS" -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: billing-demo-gateway
spec:
  gatewayClassName: istio
  listeners:
    - name: http
      port: 80
      protocol: HTTP
      allowedRoutes:
        namespaces:
          from: Same
EOF

info "Kuadrant stack installation complete"
echo "  Context:   ${CLUSTER_CONTEXT}"
echo "  Gateway:   billing-demo-gateway (namespace: ${DEMO_NS})"
echo "  Kuadrant:  ${KUADRANT_VERSION}"
echo "  Istio:     ${ISTIO_VERSION}"
