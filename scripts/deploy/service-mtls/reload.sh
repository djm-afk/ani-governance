#!/bin/sh
# Run with the namespace-scoped service account in reload-cronjob.yaml.
# Only public leaf certificates and the explicit trust bundle enter the digest.
set -eu
: "${POD_NAMESPACE:?POD_NAMESPACE is required}"
: "${BINDINGS_FILE:=/etc/service-mtls/bindings}"
kube() { kubectl --cache-dir=/tmp/kubectl-cache -n "$POD_NAMESPACE" "$@"; }
while IFS='|' read -r kind workload identities; do
  test -n "$kind" || continue
  public_material=$(kube get configmap ani-internal-service-ca -o 'jsonpath={.data.ca\.crt}')
  for identity in $identities; do
    leaf=$(kube get secret "$identity" -o 'jsonpath={.data.tls\.crt}')
    test -n "$leaf"
    public_material="$public_material$leaf"
  done
  digest=$(printf '%s' "$public_material" | sha256sum | cut -d ' ' -f 1)
  loaded=$(kube get "$kind" "$workload" -o 'jsonpath={.spec.template.metadata.annotations.ani\.io/service-mtls-material-sha256}')
  if test "$digest" != "$loaded"; then
    kube patch "$kind" "$workload" --type=merge -p "{\"spec\":{\"template\":{\"metadata\":{\"annotations\":{\"ani.io/service-mtls-material-sha256\":\"$digest\"}}}}}"
  fi
done < "$BINDINGS_FILE"
