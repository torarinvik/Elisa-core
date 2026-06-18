#include "json_parser.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static uint8_t* read_entire_file(const char* path, size_t* out_len) {
    FILE* file = fopen(path, "rb");
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

    uint8_t* buffer = (uint8_t*)malloc((size_t)size + 1);
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

int main(int argc, char** argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s <json-file> <iterations> [full|cached]\n", argv[0]);
        return 1;
    }

    const char* path = argv[1];
    int iterations = atoi(argv[2]);
    if (iterations <= 0) {
        fprintf(stderr, "iterations must be positive\n");
        return 1;
    }

    const char* mode = "ast-full";
    int64_t (*checksum_fn)(uint8_t*) = json_parser_ast_checksum;
    if (argc >= 4) {
        if (strcmp(argv[3], "cached") == 0) {
            mode = "ast-cached";
            checksum_fn = json_parser_ast_cached_checksum;
        } else if (strcmp(argv[3], "full") != 0) {
            fprintf(stderr, "unknown mode %s (expected full or cached)\n", argv[3]);
            return 1;
        }
    }

    size_t input_len = 0;
    uint8_t* input = read_entire_file(path, &input_len);
    if (input == NULL) {
        return 1;
    }

    int64_t warmup_checksum = checksum_fn(input);
    if (warmup_checksum < 0) {
        fprintf(stderr, "%s parser rejected %s\n", mode, path);
        free(input);
        return 1;
    }

    int64_t node_count = json_parser_ast_node_count(input);
    if (node_count < 0) {
        fprintf(stderr, "failed to compute node count for %s\n", path);
        free(input);
        return 1;
    }

    struct timespec start = {0};
    struct timespec end = {0};
    clock_gettime(CLOCK_MONOTONIC, &start);

    int64_t checksum_acc = 0;
    for (int i = 0; i < iterations; ++i) {
        checksum_acc += checksum_fn(input);
    }

    clock_gettime(CLOCK_MONOTONIC, &end);

    double seconds = elapsed_seconds(start, end);
    double total_bytes = (double)input_len * (double)iterations;
    double mib_per_second = seconds > 0.0 ? (total_bytes / (1024.0 * 1024.0)) / seconds : 0.0;

        printf("mode=%s file=%s bytes=%zu nodes=%" PRId64 " iterations=%d checksum=%" PRId64 " total_checksum=%" PRId64 " seconds=%.6f MiB/s=%.2f\n",
            mode,
           path,
           input_len,
            node_count,
           iterations,
           warmup_checksum,
           checksum_acc,
           seconds,
           mib_per_second);

    free(input);
    return 0;
}