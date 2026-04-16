#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
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

static uint64_t hash_bytes(uint64_t hash, const void *data, size_t len) {
    const uint8_t *bytes = (const uint8_t *)data;
    for (size_t index = 0; index < len; ++index) {
        hash ^= (uint64_t)bytes[index];
        hash *= UINT64_C(1099511628211);
    }
    return hash;
}

static int checksum_results(lua_State *state, int first_result_index, int64_t *out_checksum) {
    uint64_t hash = UINT64_C(1469598103934665603);
    int top = lua_gettop(state);
    int result_count = top >= first_result_index ? (top - first_result_index + 1) : 0;

    hash = hash_bytes(hash, &result_count, sizeof(result_count));
    for (int index = first_result_index; index <= top; ++index) {
        int type = lua_type(state, index);
        hash = hash_bytes(hash, &type, sizeof(type));

        switch (type) {
        case LUA_TNIL: {
            uint8_t marker = 0;
            hash = hash_bytes(hash, &marker, sizeof(marker));
            break;
        }
        case LUA_TBOOLEAN: {
            uint8_t value = (uint8_t)(lua_toboolean(state, index) != 0);
            hash = hash_bytes(hash, &value, sizeof(value));
            break;
        }
        case LUA_TNUMBER:
            if (lua_isinteger(state, index)) {
                uint8_t marker = 'I';
                lua_Integer value = lua_tointeger(state, index);
                hash = hash_bytes(hash, &marker, sizeof(marker));
                hash = hash_bytes(hash, &value, sizeof(value));
            } else {
                uint8_t marker = 'N';
                lua_Number value = lua_tonumber(state, index);
                hash = hash_bytes(hash, &marker, sizeof(marker));
                hash = hash_bytes(hash, &value, sizeof(value));
            }
            break;
        case LUA_TSTRING: {
            size_t len = 0;
            const char *text = lua_tolstring(state, index, &len);
            hash = hash_bytes(hash, &len, sizeof(len));
            hash = hash_bytes(hash, text, len);
            break;
        }
        default:
            fprintf(stderr, "reference executor does not support %s return values\n", luaL_typename(state, index));
            return 0;
        }
    }

    *out_checksum = (int64_t)hash;
    return 1;
}

static int execute_once(lua_State *state, const uint8_t *input, size_t input_len, const char *path, int64_t *checksum_out) {
    int function_index = lua_gettop(state) + 1;
    int status = luaL_loadbufferx(state, (const char *)input, input_len, path, NULL);
    if (status != LUA_OK) {
        const char *message = lua_tostring(state, -1);
        if (message == NULL) {
            message = "(no error message)";
        }
        fprintf(stderr, "reference executor rejected %s during load: %s\n", path, message);
        lua_pop(state, 1);
        return 0;
    }

    status = lua_pcall(state, 0, LUA_MULTRET, 0);
    if (status != LUA_OK) {
        const char *message = lua_tostring(state, -1);
        if (message == NULL) {
            message = "(no error message)";
        }
        fprintf(stderr, "reference executor rejected %s during execute: %s\n", path, message);
        lua_pop(state, 1);
        return 0;
    }

    if (!checksum_results(state, function_index, checksum_out)) {
        lua_settop(state, 0);
        return 0;
    }

    lua_settop(state, 0);
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
    if (!execute_once(state, input, input_len, path, &warmup_checksum)) {
        lua_close(state);
        free(input);
        return 1;
    }

    struct timespec start = {0};
    struct timespec end = {0};
    clock_gettime(CLOCK_MONOTONIC, &start);

    int64_t checksum_acc = 0;
    for (int i = 0; i < iterations; ++i) {
        int64_t iteration_checksum = 0;
        if (!execute_once(state, input, input_len, path, &iteration_checksum)) {
            lua_close(state);
            free(input);
            return 1;
        }
        checksum_acc += iteration_checksum;
    }

    clock_gettime(CLOCK_MONOTONIC, &end);

    double seconds = elapsed_seconds(start, end);
    double total_bytes = (double)input_len * (double)iterations;
    double mib_per_second = seconds > 0.0 ? (total_bytes / (1024.0 * 1024.0)) / seconds : 0.0;

    printf("mode=execute file=%s bytes=%zu iterations=%d checksum=%" PRId64 " total_checksum=%" PRId64 " seconds=%.6f MiB/s=%.2f\n",
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