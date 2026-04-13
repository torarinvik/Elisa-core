#!/usr/bin/env bash
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
BASELINE_DIR=""
CANDIDATE_OUT=""
COMPARE_JSON_OUT=""
OPT_LEVEL="-O3"
OPT_LEVELS=""
PARSE_ITERATIONS="20"
SAMPLE_ITERATIONS="5000"
REPEATS="3"
SKIP_REAL_CORPUS="0"
SKIP_BENCHMARK="0"
SKIP_DIFFERENTIAL="0"
KEEP_TEMP="0"
FAIL_ON_METADATA_CHANGE="0"
MIN_DELTA_PCT=""
MIN_DELTA_MIB_S=""
PYTHON_BIN="${PYTHON_BIN:-python3}"

while [ "$#" -gt 0 ]; do
    case "$1" in
        --baseline-dir)
            BASELINE_DIR="${2:?missing value for --baseline-dir}"
            shift 2
            ;;
        --candidate-out)
            CANDIDATE_OUT="${2:?missing value for --candidate-out}"
            shift 2
            ;;
        --compare-json-out)
            COMPARE_JSON_OUT="${2:?missing value for --compare-json-out}"
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
        --skip-real-corpus)
            SKIP_REAL_CORPUS="1"
            shift 1
            ;;
        --skip-benchmark)
            SKIP_BENCHMARK="1"
            shift 1
            ;;
        --skip-differential)
            SKIP_DIFFERENTIAL="1"
            shift 1
            ;;
        --keep-temp)
            KEEP_TEMP="1"
            shift 1
            ;;
        --fail-on-metadata-change)
            FAIL_ON_METADATA_CHANGE="1"
            shift 1
            ;;
        --min-delta-pct)
            MIN_DELTA_PCT="${2:?missing value for --min-delta-pct}"
            shift 2
            ;;
        --min-delta-mib-s)
            MIN_DELTA_MIB_S="${2:?missing value for --min-delta-mib-s}"
            shift 2
            ;;
        *)
            echo "unknown option: $1" >&2
            exit 1
            ;;
    esac
done

if [ -z "$BASELINE_DIR" ]; then
    echo "missing required --baseline-dir" >&2
    exit 1
fi
if [ ! -d "$BASELINE_DIR" ]; then
    echo "baseline bundle directory does not exist: $BASELINE_DIR" >&2
    exit 1
fi

if [ -z "$CANDIDATE_OUT" ]; then
    CANDIDATE_OUT="$(mktemp -d "${TMPDIR:-/tmp}/llcontext-lua-candidate.XXXXXX")"
else
    mkdir -p "$CANDIDATE_OUT"
fi

echo "lua_frontend_ci_candidate_out=$CANDIDATE_OUT"

CAPTURE_ARGS="--opt-level $OPT_LEVEL --parse-iterations $PARSE_ITERATIONS --sample-iterations $SAMPLE_ITERATIONS --repeats $REPEATS"
COMPARE_ARGS="--ci-bundle"

if [ -n "$OPT_LEVELS" ]; then
    CAPTURE_ARGS="$CAPTURE_ARGS --opt-levels $OPT_LEVELS"
fi
if [ "$SKIP_REAL_CORPUS" = "1" ]; then
    CAPTURE_ARGS="$CAPTURE_ARGS --skip-real-corpus"
fi
if [ "$SKIP_BENCHMARK" = "1" ]; then
    CAPTURE_ARGS="$CAPTURE_ARGS --skip-benchmark"
fi
if [ "$SKIP_DIFFERENTIAL" = "1" ]; then
    CAPTURE_ARGS="$CAPTURE_ARGS --skip-differential"
fi
if [ "$KEEP_TEMP" = "1" ]; then
    CAPTURE_ARGS="$CAPTURE_ARGS --keep-temp"
fi
if [ "$FAIL_ON_METADATA_CHANGE" = "1" ]; then
    COMPARE_ARGS="$COMPARE_ARGS --fail-on-metadata-change"
fi
if [ -n "$MIN_DELTA_PCT" ]; then
    COMPARE_ARGS="$COMPARE_ARGS --min-delta-pct $MIN_DELTA_PCT"
fi
if [ -n "$MIN_DELTA_MIB_S" ]; then
    COMPARE_ARGS="$COMPARE_ARGS --min-delta-mib-s $MIN_DELTA_MIB_S"
fi
if [ -n "$COMPARE_JSON_OUT" ]; then
    COMPARE_ARGS="$COMPARE_ARGS --json-out $COMPARE_JSON_OUT"
fi

# shellcheck disable=SC2086
bash "$SCRIPT_DIR/capture_lua_frontend_baseline.sh" --out "$CANDIDATE_OUT" $CAPTURE_ARGS

# shellcheck disable=SC2086
"$PYTHON_BIN" "$SCRIPT_DIR/compare_lua_frontend_reports.py" "$BASELINE_DIR" "$CANDIDATE_OUT" $COMPARE_ARGS
