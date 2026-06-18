#include <stddef.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>

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

int main(int argc, char **argv) {
    if (argc < 2) {
        fprintf(stderr, "usage: %s <lua-file>\n", argv[0]);
        return 1;
    }

    size_t input_len = 0;
    uint8_t *input = read_entire_file(argv[1], &input_len);
    if (input == NULL) {
        return 1;
    }

    lua_State *state = luaL_newstate();
    if (state == NULL) {
        fprintf(stderr, "failed to create lua state\n");
        free(input);
        return 1;
    }

    int status = luaL_loadbufferx(state, (const char *)input, input_len, argv[1], NULL);
    if (status == LUA_OK) {
        printf("status=accept file=%s\n", argv[1]);
        lua_close(state);
        free(input);
        return 0;
    }

    const char *message = lua_tostring(state, -1);
    if (message == NULL) {
        message = "(no error message)";
    }
    printf("status=reject file=%s message=%s\n", argv[1], message);
    lua_close(state);
    free(input);
    return 2;
}
