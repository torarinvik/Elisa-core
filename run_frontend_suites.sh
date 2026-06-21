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
	"$REPO_ROOT/Code/elisacore_lua/test/run_tests.sh"
	"$REPO_ROOT/Code/elisacore_pascal/test/run_tests.sh"
	"$REPO_ROOT/Code/elisacore_sml/test/run_tests.sh"
	"$REPO_ROOT/Code/elisacore_perl/test/run_tests.sh"
	"$REPO_ROOT/Code/elisacore_atpl/test/run_tests.sh"
)

pids=()
logs=()
statuses=()
running_indices=()

max_parallel=${ELISACORE_FRONTEND_SUITE_PARALLELISM:-2}
if ! [[ "$max_parallel" =~ ^[0-9]+$ ]] || [[ "$max_parallel" -lt 1 ]]; then
	echo "ELISACORE_FRONTEND_SUITE_PARALLELISM must be a positive integer, got: $max_parallel" >&2
	exit 2
fi

reap_one_suite() {
	local idx="$1"
	if wait "${pids[idx]}"; then
		statuses[idx]=0
	else
		statuses[idx]=$?
		overall_status=1
	fi
}

echo "Launching frontend suites with parallelism=${max_parallel}"
overall_status=0
for i in "${!suite_names[@]}"; do
	log_file="$TMP_DIR/$(printf '%02d' "$i")_frontend.log"
	logs[i]="$log_file"
	statuses[i]=0
	bash "${suite_scripts[i]}" >"$log_file" 2>&1 &
	pids[i]=$!
	running_indices+=("$i")
	if [[ "${#running_indices[@]}" -ge "$max_parallel" ]]; then
		reap_one_suite "${running_indices[0]}"
		running_indices=("${running_indices[@]:1}")
	fi
done

for i in "${running_indices[@]}"; do
	reap_one_suite "$i"
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
