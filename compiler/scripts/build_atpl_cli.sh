#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPILER_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${COMPILER_DIR}/.." && pwd)"
OUT_DIR="${COMPILER_DIR}/bin"
OUT_BIN="${OUT_DIR}/atpl"
ATPL_HOST_DIR="${COMPILER_DIR}/cmd/atpl"
GENERATED_DIR="${ATPL_HOST_DIR}/generated"

mkdir -p "${OUT_DIR}"
mkdir -p "${GENERATED_DIR}"

if [[ "${LLCONTEXT_USE_BOOTSTRAP:-0}" == "1" && -x "${COMPILER_DIR}/llcontext-compiler" ]]; then
  COMPILER_CMD=("${COMPILER_DIR}/llcontext-compiler")
else
  COMPILER_CMD=(go run ./src)
fi

(
  cd "${COMPILER_DIR}"
  "${COMPILER_CMD[@]}" -O3 -emit header -o "${GENERATED_DIR}/atpl_cli.h" ../Code/llcontext_atpl/src/atpl_cli.llcontext
  "${COMPILER_CMD[@]}" -O3 -emit obj -o "${GENERATED_DIR}/atpl_cli.o" ../Code/llcontext_atpl/src/atpl_cli.llcontext
)

(
  cd "${COMPILER_DIR}"
  go build -a -o "${OUT_BIN}" ./cmd/atpl
)

echo "${OUT_BIN}"
