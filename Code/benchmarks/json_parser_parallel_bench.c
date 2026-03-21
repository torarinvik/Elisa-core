#include "json_parser.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

typedef int64_t (*single_checksum_fn)(uint8_t *);
typedef int64_t (*parallel_checksum_fn)(uint8_t *, uintptr_t, uintptr_t);

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

static int select_mode(const char *mode, single_checksum_fn *warmup_fn, parallel_checksum_fn *parallel_fn) {
    if (strcmp(mode, "checksum") == 0) {
        *warmup_fn = json_parser_checksum;
        *parallel_fn = json_parser_parallel_checksum;
        return 1;
    }
    if (strcmp(mode, "ast-full") == 0) {
        *warmup_fn = json_parser_ast_checksum;
        *parallel_fn = json_parser_parallel_ast_checksum;
        return 1;
    }
    if (strcmp(mode, "ast-cached") == 0) {
        *warmup_fn = json_parser_ast_cached_checksum;
        *parallel_fn = json_parser_parallel_ast_cached_checksum;
        return 1;
    }
    return 0;
}

int main(int argc, char **argv) {
    if (argc < 4) {
        fprintf(stderr, "usage: %s <json-file> <iterations> <workers> [checksum|ast-full|ast-cached]\n", argv[0]);
        return 1;
    }

    const char *path = argv[1];
    int iterations = atoi(argv[2]);
    int workers = atoi(argv[3]);
    const char *mode = argc >= 5 ? argv[4] : "checksum";
    single_checksum_fn warmup_fn = NULL;
    parallel_checksum_fn parallel_fn = NULL;

    if (iterations <= 0) {
        fprintf(stderr, "iterations must be positive\n");
        return 1;
    }
    if (workers <= 0) {
        fprintf(stderr, "workers must be positive\n");
        return 1;
    }
    if (!select_mode(mode, &warmup_fn, &parallel_fn)) {
        fprintf(stderr, "unknown mode %s (expected checksum, ast-full, or ast-cached)\n", mode);
        return 1;
    }
    if ((int64_t)workers > json_parser_parallel_max_workers()) {
        fprintf(stderr, "workers must be <= %" PRId64 "\n", json_parser_parallel_max_workers());
        return 1;
    }

    size_t input_len = 0;
    uint8_t *input = read_entire_file(path, &input_len);
    if (input == NULL) {
        return 1;
    }

    int64_t warmup_checksum = warmup_fn(input);
    if (warmup_checksum < 0) {
        fprintf(stderr, "%s parser rejected %s\n", mode, path);
        free(input);
        return 1;
    }

    struct timespec start = {0};
    struct timespec end = {0};
    clock_gettime(CLOCK_MONOTONIC, &start);

    int64_t checksum_acc = parallel_fn(input, (uintptr_t)workers, (uintptr_t)iterations);

    clock_gettime(CLOCK_MONOTONIC, &end);

    if (checksum_acc < 0) {
        fprintf(stderr, "parallel %s parser rejected %s\n", mode, path);
        free(input);
        return 1;
    }

    double seconds = elapsed_seconds(start, end);
    double total_bytes = (double)input_len * (double)iterations;
    double mib_per_second = seconds > 0.0 ? (total_bytes / (1024.0 * 1024.0)) / seconds : 0.0;

    printf("mode=%s file=%s bytes=%zu workers=%d iterations=%d checksum=%" PRId64 " total_checksum=%" PRId64 " seconds=%.6f MiB/s=%.2f\n",
           mode,
           path,
           input_len,
           workers,
           iterations,
           warmup_checksum,
           checksum_acc,
           seconds,
           mib_per_second);

    free(input);
    return 0;
}