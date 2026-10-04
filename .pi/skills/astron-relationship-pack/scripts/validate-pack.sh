#!/usr/bin/env bash
# Validates one or more relationship-pack YAML files by running them through
# "astron projections generate --with-relationships=false" in dry-run mode
# (emits the manifest to stdout; makes no cluster writes). Requires the
# target namespace's cluster to have the relevant CRDs/kinds installed.
#
# Usage: validate-pack.sh <namespace> <group>[,<group>...] <pack-file> [<pack-file> ...]
set -euo pipefail

if [ "$#" -lt 3 ]; then
  echo "Usage: $0 <namespace> <group>[,<group>...] <pack-file> [<pack-file> ...]" >&2
  exit 1
fi

namespace="$1"
groups="$2"
shift 2

packs="$(IFS=,; echo "$*")"

astron projections generate "$namespace" \
  --include-group "$groups" \
  --with-relationships=false \
  --relationship-pack "$packs" \
  --output-file -
