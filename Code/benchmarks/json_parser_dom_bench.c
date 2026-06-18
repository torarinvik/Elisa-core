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

    uint8_t *buffer = (uint8_t *)malloc((size_t)size + 1);
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

static uint64_t mix_u64(uint64_t acc, uint64_t value) {
    acc ^= value + UINT64_C(0x9e3779b97f4a7c15) + (acc << 6) + (acc >> 2);
    return acc;
}

static uint64_t hash_bytes(const uint8_t *bytes, size_t len) {
    uint64_t acc = UINT64_C(1469598103934665603);
    for (size_t i = 0; i < len; ++i) {
        acc ^= (uint64_t)bytes[i];
        acc *= UINT64_C(1099511628211);
    }
    return mix_u64(acc, (uint64_t)len);
}

static int scratch_reserve(ScratchBuffer *scratch, size_t required) {
    if (required <= scratch->capacity) {
        return 1;
    }

    size_t next_capacity = scratch->capacity == 0 ? 64 : scratch->capacity;
    while (next_capacity < required) {
        size_t grown = next_capacity * 2;
        if (grown < next_capacity) {
            next_capacity = required;
            break;
        }
        next_capacity = grown;
    }

    uint8_t *next = (uint8_t *)realloc(scratch->data, next_capacity);
    if (next == NULL) {
        return 0;
    }

    scratch->data = next;
    scratch->capacity = next_capacity;
    return 1;
}

static void scratch_release(ScratchBuffer *scratch) {
    free(scratch->data);
    scratch->data = NULL;
    scratch->capacity = 0;
}

static uint64_t hash_value_string_fallback(JsonParserValue *value, ScratchBuffer *scratch, int *ok) {
    int64_t len = json_parser_value_string_len(value);
    if (len < 0) {
        fprintf(stderr, "value string len failed\n");
        *ok = 0;
        return 0;
    }

    size_t required = (size_t)len + 1;
    if (!scratch_reserve(scratch, required)) {
        fprintf(stderr, "value string scratch reserve failed\n");
        *ok = 0;
        return 0;
    }

    int64_t copied = json_parser_value_string_copy(value, scratch->data, scratch->capacity);
    if (copied != len) {
        fprintf(stderr, "value string copy failed len=%" PRId64 " copied=%" PRId64 "\n", len, copied);
        *ok = 0;
        return 0;
    }

    *ok = 1;
    return hash_bytes(scratch->data, (size_t)len);
}

static uint64_t hash_value_string(JsonParserValue *value, ScratchBuffer *scratch, int *ok) {
    int64_t escaped = json_parser_value_string_is_escaped(value);
    if (escaped < 0) {
        fprintf(stderr, "value string escaped check failed\n");
        *ok = 0;
        return 0;
    }

    if (escaped == 0) {
        uint8_t *raw = json_parser_value_string_raw_ptr(value);
        int64_t raw_len = json_parser_value_string_raw_len(value);
        if (raw == NULL || raw_len < 0) {
            fprintf(stderr, "value raw string view failed\n");
            *ok = 0;
            return 0;
        }
        *ok = 1;
        return hash_bytes(raw, (size_t)raw_len);
    }

    return hash_value_string_fallback(value, scratch, ok);
}

static uint64_t hash_object_key_fallback(JsonParserObjectIter *iter, ScratchBuffer *scratch, int *ok) {
    int64_t len = json_parser_object_iter_key_string_len(iter);
    if (len < 0) {
        fprintf(stderr, "object key string len failed\n");
        *ok = 0;
        return 0;
    }

    size_t required = (size_t)len + 1;
    if (!scratch_reserve(scratch, required)) {
        fprintf(stderr, "object key scratch reserve failed\n");
        *ok = 0;
        return 0;
    }

    int64_t copied = json_parser_object_iter_key_string_copy(iter, scratch->data, scratch->capacity);
    if (copied != len) {
        fprintf(stderr, "object key string copy failed len=%" PRId64 " copied=%" PRId64 "\n", len, copied);
        *ok = 0;
        return 0;
    }

    *ok = 1;
    return hash_bytes(scratch->data, (size_t)len);
}

static uint64_t hash_object_key(JsonParserObjectIter *iter, ScratchBuffer *scratch, int *ok) {
    int64_t escaped = json_parser_object_iter_key_string_is_escaped(iter);
    if (escaped < 0) {
        fprintf(stderr, "object key escaped check failed\n");
        *ok = 0;
        return 0;
    }

    if (escaped == 0) {
        uint8_t *raw = json_parser_object_iter_key_string_raw_ptr(iter);
        int64_t raw_len = json_parser_object_iter_key_string_raw_len(iter);
        if (raw == NULL || raw_len < 0) {
            fprintf(stderr, "object key raw string view failed\n");
            *ok = 0;
            return 0;
        }
        *ok = 1;
        return hash_bytes(raw, (size_t)raw_len);
    }

    return hash_object_key_fallback(iter, scratch, ok);
}

static uint64_t hash_f64(double value) {
    uint64_t bits = 0;
    memcpy(&bits, &value, sizeof(bits));
    return bits;
}

static uint64_t dom_visit_value(JsonParserValue *value, ScratchBuffer *scratch, int *ok);

static const char *benchmark_mode_name(BenchmarkMode mode) {
    switch (mode) {
    case BENCHMARK_MODE_PARSE:
        return "dom-parse";
    case BENCHMARK_MODE_COPY:
        return "dom-copy";
    case BENCHMARK_MODE_BUILD:
        return "dom-build";
    case BENCHMARK_MODE_DESTROY:
        return "dom-destroy";
    case BENCHMARK_MODE_WALK:
        return "dom-walk";
    case BENCHMARK_MODE_RETAINED_WALK:
        return "dom-retained-walk";
    default:
        return "unknown";
    }
}

static int parse_benchmark_mode(const char *text, BenchmarkMode *out_mode) {
    if (text == NULL || out_mode == NULL) {
        return 0;
    }
    if (strcmp(text, "parse") == 0 || strcmp(text, "dom-parse") == 0) {
        *out_mode = BENCHMARK_MODE_PARSE;
        return 1;
    }
    if (strcmp(text, "copy") == 0 || strcmp(text, "dom-copy") == 0) {
        *out_mode = BENCHMARK_MODE_COPY;
        return 1;
    }
    if (strcmp(text, "build") == 0 || strcmp(text, "dom-build") == 0) {
        *out_mode = BENCHMARK_MODE_BUILD;
        return 1;
    }
    if (strcmp(text, "destroy") == 0 || strcmp(text, "dom-destroy") == 0) {
        *out_mode = BENCHMARK_MODE_DESTROY;
        return 1;
    }
    if (strcmp(text, "walk") == 0 || strcmp(text, "dom") == 0 || strcmp(text, "dom-walk") == 0) {
        *out_mode = BENCHMARK_MODE_WALK;
        return 1;
    }
    if (strcmp(text, "retained") == 0 || strcmp(text, "retained-walk") == 0 || strcmp(text, "dom-retained-walk") == 0) {
        *out_mode = BENCHMARK_MODE_RETAINED_WALK;
        return 1;
    }
    return 0;
}

static uint64_t dom_visit_array(JsonParserValue *value, ScratchBuffer *scratch, int *ok) {
    int64_t len = json_parser_value_array_len(value);
    if (len < 0) {
        fprintf(stderr, "value array len failed\n");
        *ok = 0;
        return 0;
    }

    uint64_t acc = mix_u64(UINT64_C(0xa4d94f3c2b1e9071), (uint64_t)len);
    JsonParserArrayIter iter = {0};
    if (len > 0 && json_parser_value_array_iter(value, &iter) != 1) {
        fprintf(stderr, "value array iter creation failed len=%" PRId64 "\n", len);
        *ok = 0;
        return 0;
    }

    while (json_parser_array_iter_is_valid(&iter) == 1) {
        JsonParserValue child = {0};
        if (json_parser_array_iter_value(&iter, &child) != 1) {
            fprintf(stderr, "array iter value failed\n");
            *ok = 0;
            return 0;
        }

        acc = mix_u64(acc, dom_visit_value(&child, scratch, ok));
        if (!*ok) {
            return 0;
        }

        JsonParserArrayIter next = {0};
        if (json_parser_array_iter_next(&iter, &next) != 1) {
            break;
        }
        iter = next;
    }

    return mix_u64(acc, UINT64_C(0x51ed270b84f7e3a9));
}

static uint64_t dom_visit_object(JsonParserValue *value, ScratchBuffer *scratch, int *ok) {
    int64_t len = json_parser_value_object_len(value);
    if (len < 0) {
        fprintf(stderr, "value object len failed\n");
        *ok = 0;
        return 0;
    }

    uint64_t acc = mix_u64(UINT64_C(0x3c79ac492ba7b653), (uint64_t)len);
    JsonParserObjectIter iter = {0};
    if (len > 0 && json_parser_value_object_iter(value, &iter) != 1) {
        fprintf(stderr, "value object iter creation failed len=%" PRId64 "\n", len);
        *ok = 0;
        return 0;
    }

    while (json_parser_object_iter_is_valid(&iter) == 1) {
        acc = mix_u64(acc, UINT64_C(0x6eed0e9da4d94a4f));
        acc = mix_u64(acc, hash_object_key(&iter, scratch, ok));
        if (!*ok) {
            return 0;
        }

        JsonParserValue child = {0};
        if (json_parser_object_iter_value(&iter, &child) != 1) {
            fprintf(stderr, "object iter value failed\n");
            *ok = 0;
            return 0;
        }

        acc = mix_u64(acc, dom_visit_value(&child, scratch, ok));
        if (!*ok) {
            return 0;
        }

        JsonParserObjectIter next = {0};
        if (json_parser_object_iter_next(&iter, &next) != 1) {
            break;
        }
        iter = next;
    }

    return mix_u64(acc, UINT64_C(0x94d049bb133111eb));
}

static uint64_t dom_visit_value(JsonParserValue *value, ScratchBuffer *scratch, int *ok) {
    int64_t kind = json_parser_value_kind(value);
    if (kind < 0) {
        fprintf(stderr, "value kind failed\n");
        *ok = 0;
        return 0;
    }
    uint64_t acc = mix_u64(UINT64_C(0x243f6a8885a308d3), (uint64_t)(uint8_t)kind);

    switch (kind) {
    case JSON_KIND_NULL:
        if (json_parser_value_is_null(value) != 1) {
            fprintf(stderr, "value null check failed\n");
            *ok = 0;
            return 0;
        }
        return mix_u64(acc, UINT64_C(0x13198a2e03707344));
    case JSON_KIND_BOOL: {
        int64_t bool_value = json_parser_value_bool(value);
        if (bool_value < 0) {
            fprintf(stderr, "value bool failed\n");
            *ok = 0;
            return 0;
        }
        return mix_u64(acc, (uint64_t)bool_value);
    }
    case JSON_KIND_NUMBER: {
        JsonParserNumberData number = {0};
        if (json_parser_value_number_data(value, &number) != 1) {
            fprintf(stderr, "value number data failed\n");
            *ok = 0;
            return 0;
        }
        int64_t number_kind = number.kind;
        acc = mix_u64(acc, (uint64_t)(uint8_t)number_kind);
        switch (number_kind) {
        case JSON_NUMBER_UNSIGNED:
            return mix_u64(acc, number.unsigned_value);
        case JSON_NUMBER_SIGNED:
            return mix_u64(acc, (uint64_t)number.signed_value);
        case JSON_NUMBER_NON_INTEGRAL:
        case JSON_NUMBER_OUT_OF_RANGE:
            return mix_u64(acc, hash_f64(number.float_value));
        default:
            fprintf(stderr, "unexpected number kind=%" PRId64 "\n", number_kind);
            *ok = 0;
            return 0;
        }
    }
    case JSON_KIND_STRING:
        return mix_u64(acc, hash_value_string(value, scratch, ok));
    case JSON_KIND_ARRAY:
        return mix_u64(acc, dom_visit_array(value, scratch, ok));
    case JSON_KIND_OBJECT:
        return mix_u64(acc, dom_visit_object(value, scratch, ok));
    default:
        fprintf(stderr, "unexpected value kind=%" PRId64 "\n", kind);
        *ok = 0;
        return 0;
    }
}

static int64_t document_dom_checksum(uint8_t *input, size_t input_len, int64_t *out_node_count) {
    JsonParserDocument doc = {0};
    JsonParserValue root = {0};
    ScratchBuffer scratch = {0};
    int ok = 1;
    int64_t result = -1;

    if (json_parser_document_parse_with_len(input, input_len, &doc) != 1) {
        fprintf(stderr, "document parse failed\n");
        goto cleanup;
    }

    if (json_parser_document_root_value(&doc, &root) != 1) {
        fprintf(stderr, "document root value failed\n");
        goto cleanup;
    }

    if (out_node_count != NULL) {
        *out_node_count = json_parser_document_node_count(&doc);
        if (*out_node_count < 0) {
            fprintf(stderr, "document node count failed\n");
            goto cleanup;
        }
    }

    result = (int64_t)(dom_visit_value(&root, &scratch, &ok) & (uint64_t)INT64_MAX);
    if (!ok) {
        fprintf(stderr, "dom visit failed\n");
        result = -1;
    }

cleanup:
    scratch_release(&scratch);
    if (doc.impl_bits != (uintptr_t)0) {
        (void)json_parser_document_destroy(doc.impl_bits);
        doc.impl_bits = (uintptr_t)0;
    }
    return result;
}

static int64_t document_dom_retained_checksum(JsonParserDocument *doc, int64_t *out_node_count) {
    JsonParserValue root = {0};
    ScratchBuffer scratch = {0};
    int ok = 1;
    int64_t result = -1;

    if (doc == NULL || doc->impl_bits == (uintptr_t)0) {
        fprintf(stderr, "retained document missing handle\n");
        goto cleanup;
    }

    if (json_parser_document_root_value(doc, &root) != 1) {
        fprintf(stderr, "retained document root value failed\n");
        goto cleanup;
    }

    if (out_node_count != NULL) {
        *out_node_count = json_parser_document_node_count(doc);
        if (*out_node_count < 0) {
            fprintf(stderr, "retained document node count failed\n");
            goto cleanup;
        }
    }

    result = (int64_t)(dom_visit_value(&root, &scratch, &ok) & (uint64_t)INT64_MAX);
    if (!ok) {
        fprintf(stderr, "retained dom visit failed\n");
        result = -1;
    }

cleanup:
    scratch_release(&scratch);
    return result;
}

static int document_dom_parse_only(uint8_t *input, size_t input_len) {
    JsonParserDocument doc = {0};
    int ok = 0;

    if (json_parser_document_parse_with_len(input, input_len, &doc) != 1) {
        fprintf(stderr, "document parse failed\n");
        goto cleanup;
    }

    ok = 1;

cleanup:
    if (doc.impl_bits != (uintptr_t)0) {
        (void)json_parser_document_destroy(doc.impl_bits);
        doc.impl_bits = (uintptr_t)0;
    }
    return ok;
}

static int document_copy_only(const uint8_t *input, size_t input_len, uint8_t *scratch) {
    if (scratch == NULL) {
        return 0;
    }
    if (input_len > 0) {
        memcpy(scratch, input, input_len);
    }
    scratch[input_len] = 0;
    return 1;
}

static void destroy_document_batch(JsonParserDocument *docs, int count) {
    if (docs == NULL) {
        return;
    }
    for (int i = 0; i < count; ++i) {
        if (docs[i].impl_bits != (uintptr_t)0) {
            (void)json_parser_document_destroy(docs[i].impl_bits);
            docs[i].impl_bits = (uintptr_t)0;
        }
    }
}

static int build_document_batch(uint8_t *input, size_t input_len, JsonParserDocument *docs, int count) {
    if (docs == NULL) {
        return 0;
    }
    for (int i = 0; i < count; ++i) {
        if (json_parser_document_parse_with_len(input, input_len, &docs[i]) != 1) {
            fprintf(stderr, "document parse failed at batch slot %d\n", i);
            destroy_document_batch(docs, count);
            return 0;
        }
    }
    return 1;
}

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
        copy_scratch = (uint8_t *)malloc(input_len + 1);
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
