#!/usr/bin/env bash
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
PYTHON_BIN="${PYTHON_BIN:-python3}"
OUT_DIR=""
OPT_LEVEL="-O3"
ITERATIONS="5000"
REPEATS="3"
CORPUS_MANIFEST=""
KEEP_TEMP="0"

while [ "$#" -gt 0 ]; do
    case "$1" in
        --out)
            OUT_DIR="${2:?missing value for --out}"
            shift 2
            ;;
        --opt-level)
            OPT_LEVEL="${2:?missing value for --opt-level}"
            shift 2
            ;;
        --iterations)
            ITERATIONS="${2:?missing value for --iterations}"
            shift 2
            ;;
        --repeats)
            REPEATS="${2:?missing value for --repeats}"
            shift 2
            ;;
        --corpus-manifest)
            CORPUS_MANIFEST="${2:?missing value for --corpus-manifest}"
            shift 2
            ;;
        --keep-temp)
            KEEP_TEMP="1"
            shift 1
            ;;
        *)
            echo "unknown option: $1" >&2
            exit 1
            ;;
    esac
done

if [ -z "$OUT_DIR" ]; then
    OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/llcontext-lua-execution-profile.XXXXXX")"
else
    mkdir -p "$OUT_DIR"
fi

echo "lua_frontend_execution_profile_out=$OUT_DIR"

BENCH_ARGS="--opt-level=$OPT_LEVEL --iterations $ITERATIONS --repeats $REPEATS"
if [ -n "$CORPUS_MANIFEST" ]; then
    BENCH_ARGS="$BENCH_ARGS --corpus-manifest $CORPUS_MANIFEST"
fi
if [ "$KEEP_TEMP" = "1" ]; then
    BENCH_ARGS="$BENCH_ARGS --keep-temp"
fi

LL_COMMAND="$PYTHON_BIN $SCRIPT_DIR/run_lua_frontend_execution_benchmark.py $BENCH_ARGS --json-out $OUT_DIR/execution_benchmark.json"
REF_COMMAND="$PYTHON_BIN $SCRIPT_DIR/run_lua_frontend_reference_execution_benchmark.py $BENCH_ARGS --json-out $OUT_DIR/reference_execution_benchmark.json"

# shellcheck disable=SC2086
"$PYTHON_BIN" "$SCRIPT_DIR/run_lua_frontend_execution_benchmark.py" \
    $BENCH_ARGS \
    --json-out "$OUT_DIR/execution_benchmark.json" \
    > "$OUT_DIR/execution_benchmark.log" 2>&1

# shellcheck disable=SC2086
"$PYTHON_BIN" "$SCRIPT_DIR/run_lua_frontend_reference_execution_benchmark.py" \
    $BENCH_ARGS \
    --json-out "$OUT_DIR/reference_execution_benchmark.json" \
    > "$OUT_DIR/reference_execution_benchmark.log" 2>&1

"$PYTHON_BIN" "$SCRIPT_DIR/write_lua_bundle_metadata.py" \
    --output "$OUT_DIR/metadata.json" \
    --bundle-type execution-profile \
    --repo-root "$REPO_ROOT" \
    --out-dir "$OUT_DIR" \
    --setting "opt_level=$OPT_LEVEL" \
    --setting "iterations=$ITERATIONS" \
    --setting "repeats=$REPEATS" \
    --setting "corpus_manifest=$CORPUS_MANIFEST" \
    --setting "keep_temp=$KEEP_TEMP" \
    --command "llcontext_execution=$LL_COMMAND" \
    --command "reference_execution=$REF_COMMAND"

cat > "$OUT_DIR/README.txt" <<EOF2
Lua frontend execution profiling bundle
=====================================

execution_benchmark.log
    Human-readable llcontext execution benchmark sweep output.

execution_benchmark.json
    Structured llcontext execution benchmark results.

reference_execution_benchmark.log
    Human-readable PUC Lua reference execution benchmark sweep output.

reference_execution_benchmark.json
    Structured reference execution benchmark results.

metadata.json
    Bundle metadata: timestamp, repo/git state, host info, settings, and command lines.

Settings
--------
opt_level: $OPT_LEVEL
iterations: $ITERATIONS
repeats: $REPEATS
corpus_manifest: ${CORPUS_MANIFEST:-<default-execution-manifest>}
keep_temp: $KEEP_TEMP
repo_root: $REPO_ROOT
EOF2