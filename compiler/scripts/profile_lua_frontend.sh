#!/usr/bin/env bash
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
OUT_DIR=""
OPT_LEVEL="-O3"
PARSE_ITERATIONS="20"
SAMPLE_ITERATIONS="5000"
REPEATS="3"
SKIP_DIFFERENTIAL="0"

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
        --skip-differential)
            SKIP_DIFFERENTIAL="1"
            shift 1
            ;;
        *)
            echo "unknown option: $1" >&2
            exit 1
            ;;
    esac
done

if [ -z "$OUT_DIR" ]; then
    OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/llcontext-lua-profile.XXXXXX")"
else
    mkdir -p "$OUT_DIR"
fi

echo "lua_frontend_profile_out=$OUT_DIR"

python3 "$SCRIPT_DIR/run_lua_frontend_storage_benchmark.py" \
    --opt-level="$OPT_LEVEL" \
    --parse-iterations "$PARSE_ITERATIONS" \
    --sample-iterations "$SAMPLE_ITERATIONS" \
    --repeats "$REPEATS" \
    --modes parse,env,closure,label,analysis \
    > "$OUT_DIR/benchmark.log" 2>&1

if [ "$SKIP_DIFFERENTIAL" != "1" ]; then
    python3 "$SCRIPT_DIR/run_lua_frontend_differential.py" \
        > "$OUT_DIR/differential.log" 2>&1
fi

cat > "$OUT_DIR/README.txt" <<EOF2
Lua frontend profiling bundle
=============================

benchmark.log
  Synthetic + real-corpus benchmark sweep across parse/env/closure/label/analysis.

differential.log
  Curated accept/reject comparison against the C reference parser.

Settings
--------
opt_level: $OPT_LEVEL
parse_iterations: $PARSE_ITERATIONS
sample_iterations: $SAMPLE_ITERATIONS
repeats: $REPEATS
EOF2
