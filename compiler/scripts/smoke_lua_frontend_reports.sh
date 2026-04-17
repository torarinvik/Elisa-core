#!/usr/bin/env bash
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
OUT_DIR=""
KEEP_TEMP="0"
PARALLEL_WORKERS=""
PYTHON_BIN="${PYTHON_BIN:-python3}"

while [ "$#" -gt 0 ]; do
    case "$1" in
        --out)
            OUT_DIR="${2:?missing value for --out}"
            shift 2
            ;;
        --keep-temp)
            KEEP_TEMP="1"
            shift 1
            ;;
        --parallel-workers)
            PARALLEL_WORKERS="${2:?missing value for --parallel-workers}"
            shift 2
            ;;
        *)
            echo "unknown option: $1" >&2
            exit 1
            ;;
    esac
done

if [ -z "$OUT_DIR" ]; then
    OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/llcontext-lua-report-smoke.XXXXXX")"
else
    mkdir -p "$OUT_DIR"
fi

echo "lua_frontend_reports_smoke_out=$OUT_DIR"

COMMON_ARGS="--skip-real-corpus --parse-iterations 1 --sample-iterations 1 --repeats 1"
KEEP_TEMP_ARGS=""
BENCH_PARALLEL_ARGS=""
if [ "$KEEP_TEMP" = "1" ]; then
    KEEP_TEMP_ARGS="--keep-temp"
fi
if [ -n "$PARALLEL_WORKERS" ]; then
    BENCH_PARALLEL_ARGS="--parallel-workers $PARALLEL_WORKERS"
fi

BENCH_JSON="$OUT_DIR/bench_multi.json"
DIFF_JSON="$OUT_DIR/diff.json"
REFERENCE_JSON="$OUT_DIR/reference_bench.json"
PROFILE_DIR="$OUT_DIR/profile"
BASELINE_DIR="$OUT_DIR/baseline"

# shellcheck disable=SC2086
"$PYTHON_BIN" "$SCRIPT_DIR/run_lua_frontend_storage_benchmark.py" \
    --opt-levels=-O0,-O2 \
    $BENCH_PARALLEL_ARGS \
    --modes parse \
    $COMMON_ARGS \
    $KEEP_TEMP_ARGS \
    --json-out "$BENCH_JSON" \
    > "$OUT_DIR/bench_multi.log" 2>&1

# shellcheck disable=SC2086
"$PYTHON_BIN" "$SCRIPT_DIR/run_lua_frontend_differential.py" \
    $KEEP_TEMP_ARGS \
    --json-out "$DIFF_JSON" \
    > "$OUT_DIR/diff.log" 2>&1

# shellcheck disable=SC2086
"$PYTHON_BIN" "$SCRIPT_DIR/run_lua_frontend_reference_benchmark.py" \
    --skip-real-corpus \
    --parse-iterations 1 \
    --repeats 1 \
    $KEEP_TEMP_ARGS \
    --json-out "$REFERENCE_JSON" \
    > "$OUT_DIR/reference_bench.log" 2>&1

# shellcheck disable=SC2086
bash "$SCRIPT_DIR/profile_lua_frontend.sh" \
    --out "$PROFILE_DIR" \
    --modes parse \
    --skip-differential \
    $COMMON_ARGS \
    $KEEP_TEMP_ARGS \
    > "$OUT_DIR/profile.stdout" 2>&1

# shellcheck disable=SC2086
bash "$SCRIPT_DIR/capture_lua_frontend_baseline.sh" \
    --out "$BASELINE_DIR" \
    --modes parse \
    --skip-differential \
    $COMMON_ARGS \
    $KEEP_TEMP_ARGS \
    > "$OUT_DIR/baseline.stdout" 2>&1

cp "$DIFF_JSON" "$PROFILE_DIR/differential.json"
cp "$DIFF_JSON" "$BASELINE_DIR/differential.json"
cp "$OUT_DIR/diff.log" "$PROFILE_DIR/differential.log"
cp "$OUT_DIR/diff.log" "$BASELINE_DIR/differential.log"

"$PYTHON_BIN" "$SCRIPT_DIR/compare_lua_frontend_reports.py" \
    "$PROFILE_DIR" \
    "$PROFILE_DIR" \
    --ci-bundle \
    --json-out "$OUT_DIR/profile_self_compare.json" \
    > "$OUT_DIR/profile_self_compare.log" 2>&1

"$PYTHON_BIN" "$SCRIPT_DIR/compare_lua_frontend_reports.py" \
    "$PROFILE_DIR" \
    "$BASELINE_DIR" \
    --json-out "$OUT_DIR/profile_vs_baseline.json" \
    > "$OUT_DIR/profile_vs_baseline.log" 2>&1

for required in \
    "$BENCH_JSON" \
    "$DIFF_JSON" \
    "$REFERENCE_JSON" \
    "$PROFILE_DIR/benchmark.json" \
    "$PROFILE_DIR/differential.json" \
    "$PROFILE_DIR/metadata.json" \
    "$BASELINE_DIR/benchmark.json" \
    "$BASELINE_DIR/differential.json" \
    "$BASELINE_DIR/metadata.json" \
    "$OUT_DIR/profile_self_compare.json" \
    "$OUT_DIR/profile_vs_baseline.json"
do
    if [ ! -f "$required" ]; then
        echo "missing expected artifact: $required" >&2
        exit 1
    fi
done

echo "smoke_benchmark_json=$BENCH_JSON"
echo "smoke_differential_json=$DIFF_JSON"
echo "smoke_reference_benchmark_json=$REFERENCE_JSON"
echo "smoke_profile_dir=$PROFILE_DIR"
echo "smoke_baseline_dir=$BASELINE_DIR"
