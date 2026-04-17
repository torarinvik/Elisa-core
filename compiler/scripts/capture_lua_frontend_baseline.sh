#!/usr/bin/env bash
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
OUT_DIR=""
OPT_LEVEL="-O3"
OPT_LEVELS=""
PARALLEL_WORKERS=""
PARSE_ITERATIONS="20"
SAMPLE_ITERATIONS="5000"
REPEATS="3"
MODES="parse,checksum,lexer,env,closure,label,analysis"
SKIP_DIFFERENTIAL="0"
SKIP_BENCHMARK="0"
SKIP_REAL_CORPUS="0"
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
        --opt-levels)
            OPT_LEVELS="${2:?missing value for --opt-levels}"
            shift 2
            ;;
        --parallel-workers)
            PARALLEL_WORKERS="${2:?missing value for --parallel-workers}"
            shift 2
            ;;
        --parse-iterations)
            PARSE_ITERATIONS="${2:?missing value for --parse-iterations}"
            shift 2
            ;;
        --sample-iterations)
            SAMPLE_ITERATIONS="${2:?missing value for --sample-iterations}"
            shift 2
            ;;
        --repeats)
            REPEATS="${2:?missing value for --repeats}"
            shift 2
            ;;
        --modes)
            MODES="${2:?missing value for --modes}"
            shift 2
            ;;
        --skip-differential)
            SKIP_DIFFERENTIAL="1"
            shift 1
            ;;
        --skip-benchmark)
            SKIP_BENCHMARK="1"
            shift 1
            ;;
        --skip-real-corpus)
            SKIP_REAL_CORPUS="1"
            shift 1
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
    OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/llcontext-lua-baseline.XXXXXX")"
else
    mkdir -p "$OUT_DIR"
fi

echo "lua_frontend_baseline_out=$OUT_DIR"

BENCH_ARGS="--opt-level=$OPT_LEVEL --parse-iterations $PARSE_ITERATIONS --sample-iterations $SAMPLE_ITERATIONS --repeats $REPEATS --modes $MODES"
KEEP_TEMP_ARGS=""
if [ -n "$OPT_LEVELS" ]; then
    BENCH_ARGS="$BENCH_ARGS --opt-levels=$OPT_LEVELS"
fi
if [ -n "$PARALLEL_WORKERS" ]; then
    BENCH_ARGS="$BENCH_ARGS --parallel-workers=$PARALLEL_WORKERS"
fi
if [ "$SKIP_REAL_CORPUS" = "1" ]; then
    BENCH_ARGS="$BENCH_ARGS --skip-real-corpus"
fi
if [ "$KEEP_TEMP" = "1" ]; then
    KEEP_TEMP_ARGS="--keep-temp"
    BENCH_ARGS="$BENCH_ARGS --keep-temp"
fi

BENCH_COMMAND="python3 $SCRIPT_DIR/run_lua_frontend_storage_benchmark.py $BENCH_ARGS --json-out $OUT_DIR/benchmark.json"
DIFF_COMMAND="python3 $SCRIPT_DIR/run_lua_frontend_differential.py --strict --json-out $OUT_DIR/differential.json"
if [ "$KEEP_TEMP" = "1" ]; then
    DIFF_COMMAND="$DIFF_COMMAND --keep-temp"
fi

if [ "$SKIP_BENCHMARK" != "1" ]; then
    # shellcheck disable=SC2086
    python3 "$SCRIPT_DIR/run_lua_frontend_storage_benchmark.py" \
        $BENCH_ARGS \
        --json-out "$OUT_DIR/benchmark.json" \
        > "$OUT_DIR/benchmark.log" 2>&1
fi

if [ "$SKIP_DIFFERENTIAL" != "1" ]; then
    # shellcheck disable=SC2086
    python3 "$SCRIPT_DIR/run_lua_frontend_differential.py" \
        --strict \
        $KEEP_TEMP_ARGS \
        --json-out "$OUT_DIR/differential.json" \
        > "$OUT_DIR/differential.log" 2>&1
fi

python3 "$SCRIPT_DIR/write_lua_bundle_metadata.py" \
    --output "$OUT_DIR/metadata.json" \
    --bundle-type baseline \
    --repo-root "$REPO_ROOT" \
    --out-dir "$OUT_DIR" \
    --setting "opt_level=$OPT_LEVEL" \
    --setting "opt_levels=$OPT_LEVELS" \
    --setting "parallel_workers=$PARALLEL_WORKERS" \
    --setting "parse_iterations=$PARSE_ITERATIONS" \
    --setting "sample_iterations=$SAMPLE_ITERATIONS" \
    --setting "repeats=$REPEATS" \
    --setting "modes=$MODES" \
    --setting "skip_real_corpus=$SKIP_REAL_CORPUS" \
    --setting "skip_benchmark=$SKIP_BENCHMARK" \
    --setting "skip_differential=$SKIP_DIFFERENTIAL" \
    --setting "keep_temp=$KEEP_TEMP" \
    --command "benchmark=$BENCH_COMMAND" \
    --command "differential=$DIFF_COMMAND"

cat > "$OUT_DIR/README.txt" <<EOF2
Lua frontend baseline bundle
===========================

Files
-----
benchmark.log
    Human-readable benchmark sweep output across parse/checksum/lexer/env/closure/label/analysis.

benchmark.json
  Structured benchmark results when benchmark capture is enabled.

differential.log
  Human-readable strict differential sweep output.

differential.json
  Structured differential results when differential capture is enabled.

metadata.json
    Bundle metadata: timestamp, repo/git state, host info, settings, and command lines.

Settings
--------
opt_level: $OPT_LEVEL
opt_levels: ${OPT_LEVELS:-<default-single-opt-level>}
parallel_workers: ${PARALLEL_WORKERS:-<disabled>}
parse_iterations: $PARSE_ITERATIONS
sample_iterations: $SAMPLE_ITERATIONS
repeats: $REPEATS
modes: $MODES
skip_real_corpus: $SKIP_REAL_CORPUS
skip_benchmark: $SKIP_BENCHMARK
skip_differential: $SKIP_DIFFERENTIAL
keep_temp: $KEEP_TEMP
repo_root: $REPO_ROOT
EOF2