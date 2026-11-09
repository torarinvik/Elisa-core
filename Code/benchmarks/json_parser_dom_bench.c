#include "json_parser.h"

#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

enum {
    JSON_KIND_NULL = 0,
    JSON_KIND_BOOL = 1,
    JSON_KIND_NUMBER = 2,
    JSON_KIND_STRING = 3,
    JSON_KIND_ARRAY = 4,
    JSON_KIND_OBJECT = 5,
};

enum {
    JSON_NUMBER_UNSIGNED = 0,
    JSON_NUMBER_SIGNED = 1,
    JSON_NUMBER_NON_INTEGRAL = 2,
    JSON_NUMBER_OUT_OF_RANGE = 3,
};

typedef struct ScratchBuffer {
    uint8_t *data;
    size_t capacity;
} ScratchBuffer;

typedef enum BenchmarkMode {
    BENCHMARK_MODE_PARSE,
    BENCHMARK_MODE_COPY,
    BENCHMARK_MODE_BUILD,
    BENCHMARK_MODE_DESTROY,
    BENCHMARK_MODE_WALK,
    BENCHMARK_MODE_RETAINED_WALK,
} BenchmarkMode;

#include "json_parser_dom_bench_support.h"

int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s <json-file> <iterations> [parse|copy|build|destroy|walk|retained-walk]\n", argv[0]);
        return 1;
    }

    const char *path = argv[1];
    int iterations = atoi(argv[2]);
    if (iterations <= 0) {
        fprintf(stderr, "iterations must be positive\n");
        return 1;
    }

    BenchmarkMode mode = BENCHMARK_MODE_PARSE;
    if (argc >= 4 && !parse_benchmark_mode(argv[3], &mode)) {
        fprintf(stderr, "unknown benchmark mode '%s' (expected parse or walk)\n", argv[3]);
        return 1;
    }

    size_t input_len = 0;
    uint8_t *input = read_entire_file(path, &input_len);
    if (input == NULL) {
        return 1;
    }

    int64_t node_count = -1;
    int64_t warmup_checksum = -1;
    JsonParserDocument retained_doc = {0};

    JsonParserDocument *docs = NULL;
    if (mode == BENCHMARK_MODE_BUILD || mode == BENCHMARK_MODE_DESTROY) {
        docs = (JsonParserDocument *)calloc((size_t)iterations, sizeof(*docs));
        if (docs == NULL) {
            fprintf(stderr, "failed to allocate %d document handles\n", iterations);
            free(input);
            return 1;
        }
    }

    uint8_t *copy_scratch = NULL;
    if (mode == BENCHMARK_MODE_COPY) {
        copy_scratch = (uint8_t *)malloc(input_len + 1u);
        if (copy_scratch == NULL) {
            fprintf(stderr, "failed to allocate copy scratch\n");
            free(docs);
            free(input);
            return 1;
        }
    }

    if (mode == BENCHMARK_MODE_PARSE) {
        if (!document_dom_parse_only(input, input_len)) {
            fprintf(stderr, "dom parser rejected %s\n", path);
            free(copy_scratch);
            free(docs);
            free(input);
            return 1;
        }
    } else if (mode == BENCHMARK_MODE_COPY) {
        if (!document_copy_only(input, input_len, copy_scratch)) {
            fprintf(stderr, "dom copy warmup failed for %s\n", path);
            free(copy_scratch);
            free(docs);
            free(input);
            return 1;
        }
    } else if (mode == BENCHMARK_MODE_BUILD) {
        if (!build_document_batch(input, input_len, docs, 1)) {
            fprintf(stderr, "dom build warmup failed for %s\n", path);
            free(copy_scratch);
            free(docs);
            free(input);
            return 1;
        }
        destroy_document_batch(docs, 1);
    } else if (mode == BENCHMARK_MODE_DESTROY) {
        if (!build_document_batch(input, input_len, docs, iterations)) {
            fprintf(stderr, "dom destroy warmup failed for %s\n", path);
            free(copy_scratch);
            free(docs);
            free(input);
            return 1;
        }
    } else {
        if (mode == BENCHMARK_MODE_RETAINED_WALK) {
            if (json_parser_document_parse_with_len(input, input_len, &retained_doc) != 1) {
                fprintf(stderr, "retained dom parser rejected %s\n", path);
                free(copy_scratch);
                free(docs);
                free(input);
                return 1;
            }
            warmup_checksum = document_dom_retained_checksum(&retained_doc, &node_count);
        } else {
            warmup_checksum = document_dom_checksum(input, input_len, &node_count);
        }
        if (warmup_checksum < 0 || node_count < 0) {
            fprintf(stderr, "dom parser rejected %s\n", path);
            if (retained_doc.impl_bits != (uintptr_t)0) {
                (void)json_parser_document_destroy(retained_doc.impl_bits);
                retained_doc.impl_bits = (uintptr_t)0;
            }
            free(copy_scratch);
            free(docs);
            free(input);
            return 1;
        }
    }

    struct timespec start = {0};
    struct timespec end = {0};
    clock_gettime(CLOCK_MONOTONIC, &start);

    int64_t total_metric = 0;
    for (int i = 0; i < iterations; ++i) {
        if (mode == BENCHMARK_MODE_PARSE) {
            if (!document_dom_parse_only(input, input_len)) {
                fprintf(stderr, "dom parse failed for %s at iteration %d\n", path, i);
                free(copy_scratch);
                free(docs);
                free(input);
                return 1;
            }
            total_metric += 1;
        } else if (mode == BENCHMARK_MODE_COPY) {
            if (!document_copy_only(input, input_len, copy_scratch)) {
                fprintf(stderr, "dom copy failed for %s at iteration %d\n", path, i);
                free(copy_scratch);
                free(docs);
                free(input);
                return 1;
            }
            total_metric += 1;
        } else if (mode == BENCHMARK_MODE_BUILD) {
            if (json_parser_document_parse_with_len(input, input_len, &docs[i]) != 1) {
                fprintf(stderr, "dom build failed for %s at iteration %d\n", path, i);
                destroy_document_batch(docs, iterations);
                free(copy_scratch);
                free(docs);
                free(input);
                return 1;
            }
            total_metric += 1;
        } else if (mode == BENCHMARK_MODE_DESTROY) {
            if (docs[i].impl_bits == (uintptr_t)0) {
                fprintf(stderr, "dom destroy missing handle for %s at iteration %d\n", path, i);
                free(copy_scratch);
                free(docs);
                free(input);
                return 1;
            }
            (void)json_parser_document_destroy(docs[i].impl_bits);
            docs[i].impl_bits = (uintptr_t)0;
            total_metric += 1;
        } else if (mode == BENCHMARK_MODE_RETAINED_WALK) {
            int64_t iteration_checksum = document_dom_retained_checksum(&retained_doc, NULL);
            if (iteration_checksum < 0) {
                fprintf(stderr, "retained dom traversal failed for %s at iteration %d\n", path, i);
                free(copy_scratch);
                free(docs);
                free(input);
                return 1;
            }
            total_metric += iteration_checksum;
        } else {
            int64_t iteration_checksum = document_dom_checksum(input, input_len, NULL);
            if (iteration_checksum < 0) {
                fprintf(stderr, "dom traversal failed for %s at iteration %d\n", path, i);
                free(copy_scratch);
                free(docs);
                free(input);
                return 1;
            }
            total_metric += iteration_checksum;
        }
    }

    clock_gettime(CLOCK_MONOTONIC, &end);

    double seconds = elapsed_seconds(start, end);
    double total_bytes = (double)input_len * (double)iterations;
    double mib_per_second = seconds > 0.0 ? (total_bytes / (1024.0 * 1024.0)) / seconds : 0.0;

    if (mode == BENCHMARK_MODE_BUILD) {
        destroy_document_batch(docs, iterations);
    }
    if (retained_doc.impl_bits != (uintptr_t)0) {
        (void)json_parser_document_destroy(retained_doc.impl_bits);
        retained_doc.impl_bits = (uintptr_t)0;
    }

    if (mode == BENCHMARK_MODE_PARSE || mode == BENCHMARK_MODE_COPY ||
        mode == BENCHMARK_MODE_BUILD || mode == BENCHMARK_MODE_DESTROY) {
        printf("mode=%s file=%s bytes=%zu iterations=%d parses=%" PRId64 " seconds=%.6f MiB/s=%.2f\n",
               benchmark_mode_name(mode),
               path,
               input_len,
               iterations,
               total_metric,
               seconds,
               mib_per_second);
    } else {
        printf("mode=%s file=%s bytes=%zu nodes=%" PRId64 " iterations=%d checksum=%" PRId64 " total_checksum=%" PRId64 " seconds=%.6f MiB/s=%.2f\n",
               benchmark_mode_name(mode),
               path,
               input_len,
               node_count,
               iterations,
               warmup_checksum,
               total_metric,
               seconds,
               mib_per_second);
    }

    free(copy_scratch);
    free(docs);
    free(input);
    return 0;
}
