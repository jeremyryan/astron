#!/usr/bin/env bash
set -euo pipefail

# Regenerates the Helm chart's CRD template (charts/astron/templates/crds.yaml)
# from the controller-gen output in config/crd/bases, so the chart's CRDs stay
# in sync with the API types. This is invoked automatically by `make manifests`.
#
# The only chart-specific changes applied to the generated CRDs are:
#   * wrapping every CRD in an `{{- if .Values.crds.install }}` guard, and
#   * adding the `helm.sh/resource-policy: keep` annotation so `helm uninstall`
#     does not delete the CRDs (and the custom resources they own).

CRD_DIR="config/crd/bases"
OUT="charts/astron/templates/crds.yaml"

{
  echo '{{- if .Values.crds.install }}'
  for f in "$CRD_DIR"/*.yaml; do
    echo '---'
    # Drop the leading '---' controller-gen emits (we add our own separator
    # above) and inject the Helm resource-policy annotation after the existing
    # controller-gen annotation.
    awk '
      NR == 1 && $0 == "---" { next }
      { print }
      /controller-gen\.kubebuilder\.io\/version:/ { print "    helm.sh/resource-policy: keep" }
    ' "$f"
  done
  echo '{{- end }}'
} > "$OUT"

echo "wrote $OUT from $CRD_DIR"
