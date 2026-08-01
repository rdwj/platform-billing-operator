#!/usr/bin/env bash
# hack/setup-kueue.sh — Install Kueue into a kind cluster with fake GPU resources.
#
# Installs: Kueue operator, ResourceFlavor (gpu-h200), ClusterQueue, LocalQueue,
# and labels kind nodes to simulate GPU hardware for integration testing.
#
# Usage:
#   ./hack/setup-kueue.sh [CLUSTER_CONTEXT]
#
# Defaults to kind-platform-billing-dev if no context is provided.

set -euo pipefail

KUEUE_VERSION="${KUEUE_VERSION:-0.11.1}"
CLUSTER_CONTEXT="${1:-kind-platform-billing-dev}"
KUBECTL="${KUBECTL:-kubectl}"
DEMO_NS="${DEMO_NS:-billing-demo}"

info() { echo "=== $* ==="; }
kctl() { "$KUBECTL" --context "$CLUSTER_CONTEXT" "$@"; }

# ---------- Kueue operator ----------
info "Installing Kueue v${KUEUE_VERSION}"
kctl apply --server-side -f \
  "https://github.com/kubernetes-sigs/kueue/releases/download/v${KUEUE_VERSION}/manifests.yaml"

info "Waiting for Kueue controller manager"
kctl wait --for=condition=Available deployment/kueue-controller-manager \
  -n kueue-system --timeout=180s

# ---------- Label kind node for fake GPU ----------
info "Labeling kind node with fake GPU metadata"
NODE_NAME=$(kctl get nodes -o jsonpath='{.items[0].metadata.name}')
kctl label node "$NODE_NAME" nvidia.com/gpu.product=H200 --overwrite
kctl label node "$NODE_NAME" nvidia.com/gpu.count=2 --overwrite

# NOTE: On a real cluster the NVIDIA device plugin would advertise
# nvidia.com/gpu in the node's status.capacity. On kind there is no device
# plugin, so Kueue manages the quota purely at the ClusterQueue level. The
# node labels are still useful for ResourceFlavor affinity.

# ---------- Demo namespace ----------
info "Ensuring demo namespace exists"
kctl create namespace "$DEMO_NS" --dry-run=client -o yaml | kctl apply -f -

# ---------- Kueue resources ----------
info "Creating Kueue resources (ResourceFlavor, ClusterQueue, LocalQueue)"
kctl apply -f - <<'EOF'
apiVersion: kueue.x-k8s.io/v1beta1
kind: ResourceFlavor
metadata:
  name: gpu-h200
spec:
  nodeLabels:
    nvidia.com/gpu.product: "H200"
EOF

kctl apply -f - <<'EOF'
apiVersion: kueue.x-k8s.io/v1beta1
kind: ClusterQueue
metadata:
  name: gpu-cluster-queue
spec:
  resourceGroups:
    - coveredResources: ["cpu", "memory", "nvidia.com/gpu"]
      flavors:
        - name: gpu-h200
          resources:
            - name: cpu
              nominalQuota: 8
            - name: memory
              nominalQuota: 32Gi
            - name: "nvidia.com/gpu"
              nominalQuota: 4
EOF

kctl apply -n "$DEMO_NS" -f - <<'EOF'
apiVersion: kueue.x-k8s.io/v1beta1
kind: LocalQueue
metadata:
  name: team-ml-batch
spec:
  clusterQueue: gpu-cluster-queue
EOF

# ---------- Tenant annotation ----------
info "Annotating demo namespace for tenant mapping"
kctl annotate namespace "$DEMO_NS" billing.opendatahub.io/tenant-id=ten-helix --overwrite

# ---------- Summary ----------
info "Kueue installation complete"
echo "  Context:        ${CLUSTER_CONTEXT}"
echo "  Kueue:          v${KUEUE_VERSION}"
echo "  ResourceFlavor: gpu-h200"
echo "  ClusterQueue:   gpu-cluster-queue"
echo "  LocalQueue:     team-ml-batch (namespace: ${DEMO_NS})"
echo "  Tenant:         ten-helix"
