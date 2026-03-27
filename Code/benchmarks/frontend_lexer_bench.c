#include "frontend_lexer.h"

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

static uint8_t *read_file_cstr(const char *path, size_t *out_len) {
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

    size_t read_count = fread(buffer, 1u, (size_t)size, file);
    fclose(file);
    if (read_count != (size_t)size) {
        free(buffer);
        return NULL;
    }

    buffer[size] = 0;
    if (out_len != NULL) {
        *out_len = (size_t)size;
    }
    return buffer;
}

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: %s <source-file> [iterations]\n", argv[0]);
        return 1;
    }

    long iterations = 100;
    if (argc >= 3) {
        iterations = strtol(argv[2], NULL, 10);
        if (iterations <= 0) {
            fprintf(stderr, "iterations must be > 0\n");
            return 1;
        }
    }

    size_t source_len = 0;
    uint8_t *source = read_file_cstr(argv[1], &source_len);
    if (source == NULL) {
        fprintf(stderr, "failed to read %s\n", argv[1]);
        return 1;
    }

    clock_t start = clock();
    int64_t checksum = 0;
    for (long i = 0; i < iterations; ++i) {
        checksum += frontend_lexer_token_count_with_len(source, source_len);
    }
    clock_t end = clock();

    double seconds = (double)(end - start) / (double)CLOCKS_PER_SEC;
    printf("frontend_lexer_token_count: file=%s bytes=%zu iterations=%ld checksum=%lld seconds=%.6f\n",
           argv[1],
           source_len,
           iterations,
           (long long)checksum,
           seconds);

    free(source);
    return 0;
}
