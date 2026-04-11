#include "lua_frontend.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static uint8_t *read_entire_file(const char *path, size_t *out_len) {
    FILE *file = fopen(path, "rb");
    if (file == NULL) {
        fprintf(stderr, "failed to open %s\n", path);
        return NULL;
    }

    if (fseek(file, 0, SEEK_END) != 0) {
        fprintf(stderr, "failed to seek %s\n", path);
        fclose(file);
        return NULL;
    }

    long size = ftell(file);
    if (size < 0) {
        fprintf(stderr, "failed to stat %s\n", path);
        fclose(file);
        return NULL;
    }

    if (fseek(file, 0, SEEK_SET) != 0) {
        fprintf(stderr, "failed to rewind %s\n", path);
        fclose(file);
        return NULL;
    }

    uint8_t *buffer = (uint8_t *)malloc((size_t)size + 1u);
    if (buffer == NULL) {
        fprintf(stderr, "failed to allocate %ld bytes\n", size);
        fclose(file);
        return NULL;
    }

    if (fread(buffer, 1, (size_t)size, file) != (size_t)size) {
        fprintf(stderr, "failed to read %s\n", path);
        free(buffer);
        fclose(file);
        return NULL;
    }

    buffer[size] = 0;
    fclose(file);
    *out_len = (size_t)size;
    return buffer;
}

static double elapsed_seconds(struct timespec start, struct timespec end) {
    return (double)(end.tv_sec - start.tv_sec) + ((double)(end.tv_nsec - start.tv_nsec) / 1000000000.0);
}

typedef enum BenchmarkMode {
    BENCH_PARSE = 0,
    BENCH_SAMPLE = 1,
    BENCH_ENV = 2,
    BENCH_CLOSURE = 3,
    BENCH_LABEL = 4,
    BENCH_ANALYSIS = 5,
    BENCH_CONTROL = 6,
    BENCH_CHECKED = 7,
} BenchmarkMode;

static const char *benchmark_mode_name(BenchmarkMode mode) {
    switch (mode) {
    case BENCH_PARSE:
        return "parse";
    case BENCH_SAMPLE:
        return "sample";
    case BENCH_ENV:
        return "env";
    case BENCH_CLOSURE:
        return "closure";
    case BENCH_LABEL:
        return "label";
    case BENCH_ANALYSIS:
        return "analysis";
    case BENCH_CONTROL:
        return "control";
    case BENCH_CHECKED:
        return "checked";
    }
    return "unknown";
}

static int parse_benchmark_mode(const char *text, BenchmarkMode *out_mode) {
    if (strcmp(text, "parse") == 0) {
        *out_mode = BENCH_PARSE;
        return 1;
    }
    if (strcmp(text, "sample") == 0) {
        *out_mode = BENCH_SAMPLE;
        return 1;
    }
    if (strcmp(text, "env") == 0) {
        *out_mode = BENCH_ENV;
        return 1;
    }
    if (strcmp(text, "closure") == 0) {
        *out_mode = BENCH_CLOSURE;
        return 1;
    }
    if (strcmp(text, "label") == 0) {
        *out_mode = BENCH_LABEL;
        return 1;
    }
    if (strcmp(text, "analysis") == 0) {
        *out_mode = BENCH_ANALYSIS;
        return 1;
    }
    if (strcmp(text, "control") == 0) {
        *out_mode = BENCH_CONTROL;
        return 1;
    }
    if (strcmp(text, "checked") == 0) {
        *out_mode = BENCH_CHECKED;
        return 1;
    }
    return 0;
}

static int64_t run_mode(BenchmarkMode mode, const uint8_t *input, size_t input_len) {
    switch (mode) {
    case BENCH_PARSE:
        return lua_frontend_parse_checksum_with_len((uint8_t *)input, input_len);
    case BENCH_SAMPLE:
        return lua_frontend_sample_checksum();
    case BENCH_ENV:
        return lua_frontend_env_summary_fingerprint((uint8_t *)input);
    case BENCH_CLOSURE:
        return lua_frontend_closure_summary_fingerprint((uint8_t *)input);
    case BENCH_LABEL:
        return lua_frontend_label_scope_fingerprint((uint8_t *)input);
    case BENCH_ANALYSIS:
        return lua_frontend_analysis_fingerprint((uint8_t *)input);
    case BENCH_CONTROL:
        return lua_frontend_control_flow_diagnostic((uint8_t *)input);
    case BENCH_CHECKED:
        return lua_frontend_checked_status((uint8_t *)input);
    }
    return -1;
}

int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s <lua-file> <iterations> [mode]\n", argv[0]);
        fprintf(stderr, "modes: parse (default), sample, env, closure, label, analysis, control, checked\n");
        return 1;
    }

    const char *path = argv[1];
    int iterations = atoi(argv[2]);
    const char *mode_text = argc >= 4 ? argv[3] : "parse";
    BenchmarkMode mode = BENCH_PARSE;
    if (iterations <= 0) {
        fprintf(stderr, "iterations must be positive\n");
        return 1;
    }
    if (!parse_benchmark_mode(mode_text, &mode)) {
        fprintf(stderr, "unknown benchmark mode '%s' (expected parse, sample, env, closure, label, analysis, control, or checked)\n", mode_text);
        return 1;
    }

    size_t input_len = 0;
    uint8_t *input = read_entire_file(path, &input_len);
    if (input == NULL) {
        return 1;
    }

    int64_t warmup_checksum = run_mode(mode, input, input_len);
    if ((mode == BENCH_PARSE || mode == BENCH_ENV || mode == BENCH_CLOSURE || mode == BENCH_LABEL || mode == BENCH_ANALYSIS) && warmup_checksum < 0) {
        fprintf(stderr, "%s parser rejected %s\n", benchmark_mode_name(mode), path);
        free(input);
        return 1;
    }
    if (mode == BENCH_CONTROL && warmup_checksum != 0) {
        fprintf(stderr, "control-flow validator rejected %s with code %" PRId64 "\n", path, warmup_checksum);
        free(input);
        return 1;
    }
    if (mode == BENCH_CHECKED && warmup_checksum != 0) {
        fprintf(stderr, "checked frontend rejected %s with status %" PRId64 "\n", path, warmup_checksum);
        free(input);
        return 1;
    }

    struct timespec start = {0};
    struct timespec end = {0};
    clock_gettime(CLOCK_MONOTONIC, &start);

    int64_t checksum_acc = 0;
    for (int i = 0; i < iterations; ++i) {
        checksum_acc += run_mode(mode, input, input_len);
    }

    clock_gettime(CLOCK_MONOTONIC, &end);

    double seconds = elapsed_seconds(start, end);
    double total_bytes = (double)input_len * (double)iterations;
    double mib_per_second = seconds > 0.0 ? (total_bytes / (1024.0 * 1024.0)) / seconds : 0.0;

    printf("mode=%s file=%s bytes=%zu iterations=%d checksum=%" PRId64 " total_checksum=%" PRId64 " seconds=%.6f MiB/s=%.2f\n",
           benchmark_mode_name(mode),
           path,
           input_len,
           iterations,
           warmup_checksum,
           checksum_acc,
           seconds,
           mib_per_second);

    free(input);
    return 0;
}
