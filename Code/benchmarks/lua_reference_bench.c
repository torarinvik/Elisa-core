#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <time.h>

#define MAKE_LIB
#include "../lua/onelua.c"

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

static int parse_once(lua_State *state, const uint8_t *input, size_t input_len, const char *path, int64_t *checksum_acc) {
    int status = luaL_loadbufferx(state, (const char *)input, input_len, path, NULL);
    if (status != LUA_OK) {
        const char *message = lua_tostring(state, -1);
        if (message == NULL) {
            message = "(no error message)";
        }
        fprintf(stderr, "reference parser rejected %s: %s\n", path, message);
        lua_pop(state, 1);
        return 0;
    }

    *checksum_acc += (int64_t)lua_type(state, -1);
    lua_pop(state, 1);
    return 1;
}

int main(int argc, char **argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s <lua-file> <iterations>\n", argv[0]);
        return 1;
    }

    const char *path = argv[1];
    int iterations = atoi(argv[2]);
    if (iterations <= 0) {
        fprintf(stderr, "iterations must be positive\n");
        return 1;
    }

    size_t input_len = 0;
    uint8_t *input = read_entire_file(path, &input_len);
    if (input == NULL) {
        return 1;
    }

    lua_State *state = luaL_newstate();
    if (state == NULL) {
        fprintf(stderr, "failed to create lua state\n");
        free(input);
        return 1;
    }

    int64_t warmup_checksum = 0;
    if (!parse_once(state, input, input_len, path, &warmup_checksum)) {
        lua_close(state);
        free(input);
        return 1;
    }

    struct timespec start = {0};
    struct timespec end = {0};
    clock_gettime(CLOCK_MONOTONIC, &start);

    int64_t checksum_acc = 0;
    for (int i = 0; i < iterations; ++i) {
        if (!parse_once(state, input, input_len, path, &checksum_acc)) {
            lua_close(state);
            free(input);
            return 1;
        }
    }

    clock_gettime(CLOCK_MONOTONIC, &end);

    double seconds = elapsed_seconds(start, end);
    double total_bytes = (double)input_len * (double)iterations;
    double mib_per_second = seconds > 0.0 ? (total_bytes / (1024.0 * 1024.0)) / seconds : 0.0;

    printf("mode=parse file=%s bytes=%zu iterations=%d checksum=%" PRId64 " total_checksum=%" PRId64 " seconds=%.6f MiB/s=%.2f\n",
           path,
           input_len,
           iterations,
           warmup_checksum,
           checksum_acc,
           seconds,
           mib_per_second);

    lua_close(state);
    free(input);
    return 0;
}