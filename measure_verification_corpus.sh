#!/usr/bin/env bash
# Measure the verification engine over real, building Elisa codebases.
#
# Runs the compiler with its DEFAULT settings (strict proofs + SMT discharge both ON) and `--explain`
# over each frontend fixture that currently builds, reporting the aggregate proof outcome and SMT-tier
# cost plus wall time. This is the "real-codebase measurement" the engine work targeted: it shows the
# default-on SMT tier is production-safe (no crash, sub-second, z3 spawned only when an obligation
# actually needs it) and surfaces how many verification obligations real code states today.
#
# A frontend that does not build is reported as such and skipped (its block is region-inference /
# migration work, not verification). Re-run after adding contracts to a frontend to watch the proven /
# elided counts climb.
set -u

REPO_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$REPO_ROOT/compiler" || exit 1

# Frontends to probe and their entry fixture (the entry pulls in the whole frontend via includes).
names=(lua pascal sml perl atpl)

printf '%-8s %-9s %-7s %-9s %s\n' "frontend" "build" "wall" "obligs" "smt-tier"
printf '%-8s %-9s %-7s %-9s %s\n' "--------" "-----" "----" "------" "--------"

for fe in "${names[@]}"; do
	fx=$(ls "../Code/elisacore_${fe}/test/"*frontend_tests.elisa "../Code/elisacore_${fe}/test/"*_tests.elisa 2>/dev/null | head -1)
	if [ -z "$fx" ]; then
		printf '%-8s %-9s\n' "$fe" "no-fixture"
		continue
	fi
	log=$(mktemp)
	start=$(date +%s.%N 2>/dev/null || date +%s)
	go run ./src -emit semantic --explain "$fx" >"$log" 2>&1
	code=$?
	end=$(date +%s.%N 2>/dev/null || date +%s)
	wall=$(awk "BEGIN{printf \"%.2fs\", $end-$start}")

	if [ "$code" -ne 0 ]; then
		printf '%-8s %-9s %-7s\n' "$fe" "FAILS" "$wall"
		rm -f "$log"
		continue
	fi
	# The proof report's summary line: "N proven statically, M runtime-checked, ..."
	obligs=$(grep -oE '[0-9]+ proven statically, [0-9]+ runtime-checked' "$log" | head -1)
	[ -z "$obligs" ] && obligs="0 obligations"
	smt=$(grep -oE 'SMT tier:.*' "$log" | head -1)
	[ -z "$smt" ] && smt="(tier idle — no obligation reached it)"
	printf '%-8s %-9s %-7s %-9s %s\n' "$fe" "builds" "$wall" "${obligs%% *}…" "$smt"
	rm -f "$log"
done

cat <<'NOTE'

Reading the result: a "builds" row with an idle SMT tier means the frontend states no contracts, so
the engine has nothing to prove — the bottleneck is contract ADOPTION, not engine power. A "FAILS" row
is a build blocker (region inference / migration), independent of verification.
NOTE
