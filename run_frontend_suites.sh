#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$REPO_ROOT"

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM HUP

suite_names=(
	"Lua frontend"
	"Pascal frontend"
	"SML frontend"
	"Perl frontend"
	"ATPL frontend"
)

suite_scripts=(
	"$REPO_ROOT/Code/llcontext_lua/test/run_tests.sh"
	"$REPO_ROOT/Code/llcontext_pascal/test/run_tests.sh"
	"$REPO_ROOT/Code/llcontext_sml/test/run_tests.sh"
	"$REPO_ROOT/Code/llcontext_perl/test/run_tests.sh"
	"$REPO_ROOT/Code/llcontext_atpl/test/run_tests.sh"
)

pids=()
logs=()
statuses=()

echo "Launching frontend suites in parallel"
for i in "${!suite_names[@]}"; do
	log_file="$TMP_DIR/$(printf '%02d' "$i")_frontend.log"
	logs[i]="$log_file"
	statuses[i]=0
	bash "${suite_scripts[i]}" >"$log_file" 2>&1 &
	pids[i]=$!
done

overall_status=0
for i in "${!suite_names[@]}"; do
	if wait "${pids[i]}"; then
		statuses[i]=0
	else
		statuses[i]=$?
		overall_status=1
	fi
done

for i in "${!suite_names[@]}"; do
	echo
	echo "=== ${suite_names[i]} ==="
	if [[ "${statuses[i]}" -eq 0 ]]; then
		echo "[ PASS ] ${suite_names[i]}"
	else
		echo "[ FAIL ] ${suite_names[i]} (exit ${statuses[i]})"
	fi
	cat "${logs[i]}"
done

echo
if [[ "$overall_status" -eq 0 ]]; then
	echo "Frontend sweep complete"
else
	echo "Frontend sweep failed"
fi

exit "$overall_status"
