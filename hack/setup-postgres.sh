#!/usr/bin/env bash
# hack/setup-postgres.sh — Deploy Postgres 16 to a kind cluster for dev/test.
#
# Creates a billing-system namespace, deploys Postgres 16, creates the
# billing_ledger database, and applies goose-style migrations.
#
# Usage:
#   ./hack/setup-postgres.sh [CLUSTER_CONTEXT]
#
# Defaults to kind-platform-billing-dev if no context is provided.

set -euo pipefail

CLUSTER_CONTEXT="${1:-kind-platform-billing-dev}"
KUBECTL="${KUBECTL:-kubectl}"
POSTGRES_NS="billing-system"
POSTGRES_DB="billing_ledger"
POSTGRES_USER="billing"
POSTGRES_PASSWORD="billing-dev-password"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
MIGRATIONS_DIR="${SCRIPT_DIR}/../migrations"
SKIP_MIGRATIONS="${SKIP_MIGRATIONS:-false}"

info() { echo "=== $* ==="; }
kctl() { "$KUBECTL" --context "$CLUSTER_CONTEXT" "$@"; }

info "Creating namespace ${POSTGRES_NS}"
kctl create namespace "$POSTGRES_NS" --dry-run=client -o yaml | kctl apply -f -

info "Creating Postgres credentials secret"
kctl apply -n "$POSTGRES_NS" -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: billing-ledger-postgres
type: Opaque
stringData:
  POSTGRES_USER: "${POSTGRES_USER}"
  POSTGRES_PASSWORD: "${POSTGRES_PASSWORD}"
  POSTGRES_DB: "${POSTGRES_DB}"
  DSN: "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres.${POSTGRES_NS}.svc:5432/${POSTGRES_DB}?sslmode=disable"
EOF

info "Deploying Postgres 16"
kctl apply -n "$POSTGRES_NS" -f - <<EOF
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: postgres-data
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 1Gi
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres
spec:
  replicas: 1
  selector:
    matchLabels:
      app: postgres
  template:
    metadata:
      labels:
        app: postgres
    spec:
      containers:
        - name: postgres
          image: postgres:16
          ports:
            - containerPort: 5432
          envFrom:
            - secretRef:
                name: billing-ledger-postgres
          volumeMounts:
            - name: data
              mountPath: /var/lib/postgresql/data
              subPath: pgdata
          readinessProbe:
            exec:
              command: [pg_isready, -U, "${POSTGRES_USER}"]
            initialDelaySeconds: 5
            periodSeconds: 5
          resources:
            requests:
              cpu: 100m
              memory: 256Mi
            limits:
              cpu: 500m
              memory: 512Mi
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: postgres-data
---
apiVersion: v1
kind: Service
metadata:
  name: postgres
spec:
  selector:
    app: postgres
  ports:
    - port: 5432
      targetPort: 5432
EOF

info "Waiting for Postgres to be ready"
kctl wait --for=condition=Available deployment/postgres -n "$POSTGRES_NS" --timeout=120s
POSTGRES_POD=$(kctl get pod -n "$POSTGRES_NS" -l app=postgres -o jsonpath='{.items[0].metadata.name}')
kctl wait --for=condition=Ready "pod/${POSTGRES_POD}" -n "$POSTGRES_NS" --timeout=60s

if [ "$SKIP_MIGRATIONS" != "true" ]; then
  info "Applying migrations (${MIGRATIONS_DIR})"
  for f in "${MIGRATIONS_DIR}"/000*.sql; do
    fname=$(basename "$f")
    echo "  Applying ${fname}..."
    # Strip goose Down section for plain-psql application
    sed -n '/-- +goose Down/q;p' "$f" | \
      kctl exec -i -n "$POSTGRES_NS" "${POSTGRES_POD}" -- \
        psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -f - >/dev/null
  done
else
  info "Skipping migrations (SKIP_MIGRATIONS=true)"
fi

info "Postgres setup complete"
echo "  Context:   ${CLUSTER_CONTEXT}"
echo "  Namespace: ${POSTGRES_NS}"
echo "  Database:  ${POSTGRES_DB}"
echo "  Secret:    billing-ledger-postgres (contains DSN)"
