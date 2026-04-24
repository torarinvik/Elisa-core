#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

echo "=== Lua frontend ==="
bash "$SCRIPT_DIR/Code/llcontext_lua/test/run_tests.sh"

echo
echo "=== Pascal frontend ==="
bash "$SCRIPT_DIR/Code/llcontext_pascal/test/run_tests.sh"

echo
echo "=== SML frontend ==="
bash "$SCRIPT_DIR/Code/llcontext_sml/test/run_tests.sh"

echo
echo "=== Perl frontend ==="
bash "$SCRIPT_DIR/Code/llcontext_perl/test/run_tests.sh"

echo
echo "=== ATPL frontend ==="
bash "$SCRIPT_DIR/Code/llcontext_atpl/test/run_tests.sh"

echo
echo "Frontend sweep complete"
