#!/bin/bash
# Apply the ModelDev execution-detail environment prerequisites.
#
# Four changes are required before the detail read routes work. Three of them
# live outside the Go source, so a fresh environment must reproduce them or the
# routes fail closed. Every step is idempotent and separately switchable.
#
#   images   set the detail-era ani-governance / ani-modeldev images
#   apis     register the three new (path, method) pairs in the API directory
#   grant    link those APIs to modeldev:execution:get / modeldev:execution:logs
#   rbac     let ani-modeldev-control list Events in the tenant namespace
#
# Usage:
#   scripts/ops/apply-modeldev-execution-detail.sh [options] <step>...
#
# Options (no cluster, namespace, role or image is defaulted; pass the target
# explicitly so this cannot act on whichever cluster happens to be current):
#   -n, --namespace            namespace holding ani-modeldev / ani-governance
#   -t, --tenant-namespace     tenant namespace that runs the executions
#   -p, --postgres-namespace   namespace holding the governance StatefulSet
#   -s, --postgres-statefulset Postgres StatefulSet name
#   -r, --migrate-role         migration-owner role that owns sys_apis
#   -d, --database             governance database name
#   -a, --admin-deployment     governance-admin Deployment carrying the CLI
#       --modeldev-image       ani-modeldev-service image reference
#       --governance-image     ani-governance image reference
#   -h, --help
#
# The grant step needs the migration-owner role because the runtime role cannot
# read sys_apis or write sys_permission_apis.
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PERMISSION_SQL="${SCRIPT_DIR}/sql/register-modeldev-execution-detail-permissions.sql"

NAMESPACE=""; TENANT_NAMESPACE=""; POSTGRES_NAMESPACE=""; POSTGRES_STATEFULSET=""
MIGRATE_ROLE=""; GOVERNANCE_DB=""; ADMIN_DEPLOYMENT=""
MODELDEV_IMAGE=""; GOVERNANCE_IMAGE=""

usage() {
  sed -n '3,30p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
}

fail() { echo "error: $*" >&2; exit 2; }
need() { [ -n "${2:-}" ] || fail "$1 is required for step '$3'"; }

while [ $# -gt 0 ]; do
  case "$1" in
    -n|--namespace) NAMESPACE="$2"; shift 2 ;;
    -t|--tenant-namespace) TENANT_NAMESPACE="$2"; shift 2 ;;
    -p|--postgres-namespace) POSTGRES_NAMESPACE="$2"; shift 2 ;;
    -s|--postgres-statefulset) POSTGRES_STATEFULSET="$2"; shift 2 ;;
    -r|--migrate-role) MIGRATE_ROLE="$2"; shift 2 ;;
    -d|--database) GOVERNANCE_DB="$2"; shift 2 ;;
    -a|--admin-deployment) ADMIN_DEPLOYMENT="$2"; shift 2 ;;
    --modeldev-image) MODELDEV_IMAGE="$2"; shift 2 ;;
    --governance-image) GOVERNANCE_IMAGE="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    -*) fail "unknown option: $1" ;;
    *) break ;;
  esac
done
STEPS=("$@")

set_images() {
  need --namespace "$NAMESPACE" images
  need --modeldev-image "$MODELDEV_IMAGE" images
  need --governance-image "$GOVERNANCE_IMAGE" images
  kubectl -n "$NAMESPACE" set image deploy/ani-modeldev modeldev="$MODELDEV_IMAGE"
  kubectl -n "$NAMESPACE" set image deploy/ani-governance governance="$GOVERNANCE_IMAGE"
  kubectl -n "$NAMESPACE" annotate deploy/ani-modeldev deploy/ani-governance \
    kubernetes.io/change-cause="modeldev execution detail" --overwrite
  kubectl -n "$NAMESPACE" rollout status deploy/ani-modeldev --timeout=300s
  kubectl -n "$NAMESPACE" rollout status deploy/ani-governance --timeout=300s
}

# sync-apis only writes the API directory. It never links a permission, so this
# step alone leaves every new route unauthorized; run grant afterwards.
sync_apis() {
  need --namespace "$NAMESPACE" apis
  need --admin-deployment "$ADMIN_DEPLOYMENT" apis
  local pod
  pod=$(kubectl -n "$NAMESPACE" get pods -l "app=$ADMIN_DEPLOYMENT" \
    -o jsonpath='{.items[0].metadata.name}')
  [ -n "$pod" ] || fail "no $ADMIN_DEPLOYMENT pod in $NAMESPACE"
  kubectl -n "$NAMESPACE" exec "$pod" -- admin sync-apis --dry-run | tee /dev/stderr \
    | grep -q 'pods\|events\|logs/stream' || fail "dry-run did not report the detail routes"
  kubectl -n "$NAMESPACE" exec "$pod" -- admin sync-apis
}

grant_apis() {
  need --postgres-namespace "$POSTGRES_NAMESPACE" grant
  need --postgres-statefulset "$POSTGRES_STATEFULSET" grant
  need --migrate-role "$MIGRATE_ROLE" grant
  need --database "$GOVERNANCE_DB" grant
  [ -f "$PERMISSION_SQL" ] || fail "missing $PERMISSION_SQL"
  kubectl -n "$POSTGRES_NAMESPACE" exec -i "statefulset/$POSTGRES_STATEFULSET" -- \
    psql -U "$MIGRATE_ROLE" -d "$GOVERNANCE_DB" -v ON_ERROR_STOP=1 -f - < "$PERMISSION_SQL"
}

patch_rbac() {
  need --namespace "$NAMESPACE" rbac
  need --tenant-namespace "$TENANT_NAMESPACE" rbac
  local sa="system:serviceaccount:${NAMESPACE}:modeldev-control"
  if kubectl auth can-i list events -n "$TENANT_NAMESPACE" --as="$sa" | grep -qx yes; then
    echo "events already readable in $TENANT_NAMESPACE"
    return 0
  fi
  kubectl -n "$TENANT_NAMESPACE" patch role ani-modeldev-v21-control --type=json -p='[
    {"op":"add","path":"/rules/-","value":{"apiGroups":[""],"resources":["events"],"verbs":["get","list"]}}
  ]' || fail "role ani-modeldev-v21-control not found in $TENANT_NAMESPACE"
  kubectl auth can-i list events -n "$TENANT_NAMESPACE" --as="$sa"
}

[ ${#STEPS[@]} -gt 0 ] || { usage >&2; fail "name at least one step: images apis grant rbac"; }
for step in "${STEPS[@]}"; do
  echo "=== $step ==="
  case "$step" in
    images) set_images ;;
    apis) sync_apis ;;
    grant) grant_apis ;;
    rbac) patch_rbac ;;
    *) fail "unknown step: $step" ;;
  esac || exit $?
done
