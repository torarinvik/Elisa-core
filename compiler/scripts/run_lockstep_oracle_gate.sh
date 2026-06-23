#!/usr/bin/env bash
# EASM lockstep execution oracle gate (docs/103 stage 3c, docs/104 rigor rung 4).
#
# Runs the lockstep oracle with the toolchain present so it ACTUALLY executes the assemble-both +
# fuzz-and-diff equivalence check (rather than degrading to a skip). Fails if the toolchain is missing
# in CI -- a silent skip would let the oracle bit-rot unnoticed -- and runs the oracle test family,
# including the production-representative real-AeroLib-stub check.
set -euo pipefail

echo "== locating llvm-mc / clang (the oracle assembles + links both bodies) =="
LLVM_MC="$(command -v llvm-mc || true)"
for p in /opt/homebrew/opt/llvm/bin/llvm-mc /usr/local/opt/llvm/bin/llvm-mc /usr/lib/llvm-*/bin/llvm-mc; do
  [ -z "${LLVM_MC}" ] && [ -x "$p" ] && LLVM_MC="$p"
done
if [ -z "${LLVM_MC}" ]; then
  echo "ERROR: llvm-mc not found; the lockstep oracle would skip. Install LLVM in CI." >&2
  exit 1
fi
echo "llvm-mc: ${LLVM_MC}"
command -v clang >/dev/null || { echo "ERROR: clang not found." >&2; exit 1; }

echo "== running the lockstep oracle test family =="
# Scope to the oracle tests (TestLockstepOracle*): they set ELISA_EASM_LOCKSTEP_ORACLE themselves and
# manage the toolchain-override env. Do NOT force the env globally -- that would make the structural
# lockstep tests (which assert zero issues) see expected oracle skip-warnings on non-leaf fixtures.
# -count=1 defeats caching so the subprocess equivalence runs every time.
go test ./src/easm/ -run 'TestLockstepOracle' -count=1 -v

echo "== OK: lockstep oracle executed against synthetic and real (AeroLib stub) routines =="
