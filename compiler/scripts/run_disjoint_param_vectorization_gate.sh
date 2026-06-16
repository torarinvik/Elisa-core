#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPILER_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

COUNT="${COUNT:-1}"

usage() {
  cat <<'EOF'
Usage: ./scripts/run_disjoint_param_vectorization_gate.sh [--count 1]

Runs the docs/84 disjoint-parameter vectorization gate:
  1. semantic proven_distinct / drift-frontier tests
  2. backend alias.scope/noalias stamping and O3 memcheck-elision tests
  3. native bit-identical O0/no-stamp vs O3/stamped checksum harness

The native harness uses clang when available and skips that subtest otherwise.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --count)
      COUNT="${2:?missing value for --count}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

run_cmd() {
  echo
  echo "==> $*"
  "$@"
}

cd "${COMPILER_DIR}"

run_cmd go test ./src/semantic -run '^(TestCallArgDisjoint|TestFuncDisjoint)' -count="${COUNT}"
run_cmd go test ./src/backend -run '^TestDisjointParamScopes' -count="${COUNT}"
run_cmd go test ./src -run '^TestDisjointParamVectorizationBitIdentical$' -count="${COUNT}"

echo
echo "Disjoint-parameter vectorization gate complete"
