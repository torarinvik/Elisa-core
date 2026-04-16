#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/../.." && pwd)
INPUT_PATH=${1:-"$REPO_ROOT/Code/benchmarks/lua_frontend_benchmark_corpus/closure_pipeline.lua"}
WORKERS=${2:-4}
ITERATIONS=${3:-12}

if [[ ! -f "$INPUT_PATH" ]]; then
    echo "input file not found: $INPUT_PATH" >&2
    exit 1
fi

TMP_RUN=$(mktemp -d)
trap 'rm -rf "$TMP_RUN"' EXIT

cd "$REPO_ROOT/compiler"

go run ./src -O3 -emit header -o "$TMP_RUN/lua_frontend.h" ../Code/llcontext_lua/src/lua_frontend.llcontext
go run ./src -O3 -emit obj -o "$TMP_RUN/lua_frontend.o" ../Code/llcontext_lua/src/lua_frontend.llcontext

cat > "$TMP_RUN/lua_frontend_parallel_smoke.c" <<'EOF'
#include "lua_frontend.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

typedef int64_t (*single_with_len_fn)(uint8_t *, uintptr_t);
typedef int64_t (*parallel_with_len_fn)(uint8_t *, uintptr_t, uintptr_t, uintptr_t);

typedef struct ModeSpec {
    const char *name;
    single_with_len_fn serial_value;
    single_with_len_fn serial_status;
    parallel_with_len_fn parallel_value;
} ModeSpec;

static uint8_t *read_entire_file(const char *path, size_t *out_len) {
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        return NULL;
    }
    if (fseek(file, 0, SEEK_END) != 0) {
        fclose(file);
        return NULL;
    }
    long size = ftell(file);
    if (size < 0) {
        fclose(file);
        return NULL;
    }
    if (fseek(file, 0, SEEK_SET) != 0) {
        fclose(file);
        return NULL;
    }

    uint8_t *buffer = (uint8_t *)malloc((size_t)size + 1u);
    if (buffer == NULL) {
        fclose(file);
        return NULL;
    }
    if (fread(buffer, 1, (size_t)size, file) != (size_t)size) {
        free(buffer);
        fclose(file);
        return NULL;
    }
    fclose(file);
    buffer[size] = 0;
    *out_len = (size_t)size;
    return buffer;
}

static uint64_t repeated_sum_bits(int64_t value, uintptr_t iterations) {
    uint64_t total = 0;
    for (uintptr_t i = 0; i < iterations; ++i) {
        total += (uint64_t)value;
    }
    return total;
}

int main(int argc, char **argv) {
    if (argc != 4) {
        fprintf(stderr, "usage: %s <lua-file> <workers> <iterations>\n", argv[0]);
        return 1;
    }

    const char *path = argv[1];
    uintptr_t workers = (uintptr_t)strtoull(argv[2], NULL, 10);
    uintptr_t iterations = (uintptr_t)strtoull(argv[3], NULL, 10);
    size_t input_len = 0;
    uint8_t *input = read_entire_file(path, &input_len);
    if (input == NULL) {
        fprintf(stderr, "failed to read %s\n", path);
        return 1;
    }

    ModeSpec modes[] = {
        {"parse", lua_frontend_parse_checksum_with_len, lua_frontend_parse_status_with_len, lua_frontend_parallel_parse_checksum_with_len},
        {"metrics", lua_frontend_parse_metrics_checksum_with_len, lua_frontend_parse_metrics_status_with_len, lua_frontend_parallel_metrics_checksum_with_len},
        {"lexer", lua_frontend_lexer_checksum_with_len, lua_frontend_lexer_status_with_len, lua_frontend_parallel_lexer_checksum_with_len},
        {"analysis", lua_frontend_analysis_fingerprint_with_len, lua_frontend_checked_status_with_len, lua_frontend_parallel_analysis_fingerprint_with_len},
    };

    for (size_t index = 0; index < sizeof(modes) / sizeof(modes[0]); ++index) {
        ModeSpec mode = modes[index];
        int64_t status = mode.serial_status(input, (uintptr_t)input_len);
        if (status != 0) {
            fprintf(stderr, "%s serial status failed for %s\n", mode.name, path);
            free(input);
            return 1;
        }

        int64_t serial_value = mode.serial_value(input, (uintptr_t)input_len);
        int64_t parallel_value = mode.parallel_value(input, (uintptr_t)input_len, workers, iterations);
        uint64_t expected_bits = repeated_sum_bits(serial_value, iterations);
        if ((uint64_t)parallel_value != expected_bits) {
            fprintf(stderr,
                    "%s mismatch: serial=%" PRId64 " iterations=%" PRIuPTR " expected_bits=%" PRIu64 " parallel=%" PRId64 "\n",
                    mode.name,
                    serial_value,
                    iterations,
                    expected_bits,
                    parallel_value);
            free(input);
            return 1;
        }
    }

    if (lua_frontend_parallel_parse_checksum_with_len(input, (uintptr_t)input_len, 0u, iterations) != -1) {
        fprintf(stderr, "expected zero-worker parse request to fail\n");
        free(input);
        return 1;
    }
    if (lua_frontend_parallel_parse_checksum_with_len(input, (uintptr_t)input_len, workers, 0u) != -1) {
        fprintf(stderr, "expected zero-iteration parse request to fail\n");
        free(input);
        return 1;
    }
    if (lua_frontend_parallel_parse_checksum_with_len(input, (uintptr_t)input_len, (uintptr_t)lua_frontend_parallel_max_workers() + 1u, 1u) != -1) {
        fprintf(stderr, "expected oversized worker request to fail\n");
        free(input);
        return 1;
    }

    printf("lua frontend parallel smoke passed for %s with workers=%" PRIuPTR " iterations=%" PRIuPTR "\n", path, workers, iterations);
    free(input);
    return 0;
}
EOF

clang -O3 -pthread -Wl,-undefined,dynamic_lookup -I "$TMP_RUN" \
    "$TMP_RUN/lua_frontend_parallel_smoke.c" \
    ../Code/benchmarks/json_parser_runtime_shims.c \
    ../Code/benchmarks/json_parser_concurrency_runtime.c \
    "$TMP_RUN/lua_frontend.o" \
    -o "$TMP_RUN/lua_frontend_parallel_smoke"

"$TMP_RUN/lua_frontend_parallel_smoke" "$INPUT_PATH" "$WORKERS" "$ITERATIONS"