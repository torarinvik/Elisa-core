#include "packed_lowering_ml_ast_mega_core.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static double elapsed_seconds(struct timespec start, struct timespec end) {
    return (double)(end.tv_sec - start.tv_sec) + ((double)(end.tv_nsec - start.tv_nsec) / 1000000000.0);
}

int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s <scalar|parallel> <iterations> [workers]\n", argv[0]);
        return 1;
    }

    const char *mode = argv[1];
    int iterations = atoi(argv[2]);
    int workers = argc >= 4 ? atoi(argv[3]) : 1;
    if (iterations <= 0) {
        fprintf(stderr, "iterations must be positive\n");
        return 1;
    }
    if (workers <= 0) {
        fprintf(stderr, "workers must be positive\n");
        return 1;
    }

    int64_t checksum = packed_ml_ast_checksum();
    if (checksum <= 0) {
        fprintf(stderr, "packed_ml_ast_checksum failed with %" PRId64 "\n", checksum);
        return 1;
    }

    struct timespec start = {0};
    struct timespec end = {0};
    clock_gettime(CLOCK_MONOTONIC, &start);

    int64_t total_checksum = 0;
    if (strcmp(mode, "scalar") == 0) {
        total_checksum = packed_ml_ast_repeated_checksum((uintptr_t)iterations);
    } else if (strcmp(mode, "parallel") == 0) {
        if ((int64_t)workers > packed_ml_ast_parallel_max_workers()) {
            fprintf(stderr, "workers must be <= %" PRId64 "\n", packed_ml_ast_parallel_max_workers());
            return 1;
        }
        total_checksum = packed_ml_ast_parallel_checksum((uintptr_t)workers, (uintptr_t)iterations);
    } else {
        fprintf(stderr, "unknown mode %s (expected scalar or parallel)\n", mode);
        return 1;
    }

    clock_gettime(CLOCK_MONOTONIC, &end);

    if (total_checksum <= 0) {
        fprintf(stderr, "%s checksum failed with %" PRId64 "\n", mode, total_checksum);
        return 1;
    }

    double seconds = elapsed_seconds(start, end);
    double checksums_per_second = seconds > 0.0 ? (double)iterations / seconds : 0.0;

    printf("mode=%s iterations=%d workers=%d checksum=%" PRId64 " total_checksum=%" PRId64 " seconds=%.6f checksums_per_second=%.2f\n",
           mode,
           iterations,
           workers,
           checksum,
           total_checksum,
           seconds,
           checksums_per_second);
    return 0;
}
