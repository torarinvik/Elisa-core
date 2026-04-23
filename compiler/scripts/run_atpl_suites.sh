#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPILER_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

TIMEOUT_SECONDS="${ATPL_TEST_TIMEOUT_SECONDS:-240}"

COMPILER_MODE="${ATPL_TEST_COMPILER_MODE:-fresh}"

if [[ "${COMPILER_MODE}" == "binary" && -x "${COMPILER_DIR}/llcontext-compiler" ]]; then
  COMPILER_CMD=("${COMPILER_DIR}/llcontext-compiler")
else
  COMPILER_CMD=(go run ./src)
fi

run_suite() {
  local suite_path="$1"

  echo "=== ${suite_path} ==="
  (
    cd "${COMPILER_DIR}"
    perl -e 'alarm shift; exec @ARGV or die $!;' \
      "${TIMEOUT_SECONDS}" \
      "${COMPILER_CMD[@]}" \
      -emit test \
      "${suite_path}"
  )
}

run_go_suite() {
  local test_name="$1"

  echo "=== go test -tags atplsuite ./src -run ${test_name} ==="
  (
    cd "${COMPILER_DIR}"
    perl -e 'alarm shift; exec @ARGV or die $!;' \
      "${TIMEOUT_SECONDS}" \
      go \
      test \
      -tags \
      atplsuite \
      ./src \
      -run "${test_name}" \
      -count=1
  )
}

run_compiler_go_suite() {
  local package_path="$1"

  echo "=== go test ${package_path} ==="
  (
    cd "${COMPILER_DIR}"
    perl -e 'alarm shift; exec @ARGV or die $!;' \
      "${TIMEOUT_SECONDS}" \
      go \
      test \
      "${package_path}" \
      -count=1
  )
}

run_suite "../Code/llcontext_atpl/test/atpl_frontend_tests.llcontext"
run_suite "../Code/llcontext_atpl/test/atpl_runtime_tests.llcontext"
run_suite "../Code/llcontext_atpl/test/atpl_cli_tests.llcontext"
run_compiler_go_suite "./internal/atplcli"
run_go_suite "^TestATPL"

echo
echo "ATPL sweep complete"