#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/../.." && pwd)
OUTPUT_PATH="${1:-${ELISACORE_JSON_BENCH_INPUT:-/tmp/zimdjson-dom-large.json}}"

if [[ -e "$OUTPUT_PATH" && ! -f "$OUTPUT_PATH" ]]; then
    echo "json parser bench input path is not a regular file: $OUTPUT_PATH" >&2
    exit 1
fi

if [[ -s "$OUTPUT_PATH" ]]; then
    printf '%s\n' "$OUTPUT_PATH"
    exit 0
fi

mkdir -p -- "$(dirname -- "$OUTPUT_PATH")"
echo "generating json parser benchmark input: $OUTPUT_PATH" >&2

cd "$REPO_ROOT/compiler"
go run ./test/benchmarks/cmd/gen_synthetic_json -case large -o "$OUTPUT_PATH"

if [[ ! -s "$OUTPUT_PATH" ]]; then
    echo "json parser benchmark input generation produced an empty file: $OUTPUT_PATH" >&2
    exit 1
fi

printf '%s\n' "$OUTPUT_PATH"