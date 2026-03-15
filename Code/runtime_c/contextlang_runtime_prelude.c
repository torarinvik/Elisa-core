#include <stdlib.h>
#include <stdio.h>
#include <string.h>

#define ARENA_IMPLEMENTATION
#include "../arena.h"

static Arena ctx_stage0_perm_arena = {0};
static Arena ctx_stage0_scratch_arena = {0};

#define CTX_STAGE0_SMALL_STRING_CACHE_BUCKETS 1024u
#define CTX_STAGE0_STRING_LEN_CACHE_BUCKETS 4096u

typedef struct ctx_stage0_small_string_cache_entry {
    struct ctx_stage0_small_string_cache_entry *next;
    size_t len;
    char text[];
} ctx_stage0_small_string_cache_entry;

typedef struct ctx_stage0_string_len_cache_entry {
    struct ctx_stage0_string_len_cache_entry *next;
    const char *ptr;
    size_t len;
} ctx_stage0_string_len_cache_entry;

typedef struct {
    char *data;
    long long len;
} ctx_stage0_string_view;

static ctx_stage0_small_string_cache_entry *ctx_stage0_small_string_cache[CTX_STAGE0_SMALL_STRING_CACHE_BUCKETS] = {0};
static ctx_stage0_string_len_cache_entry *ctx_stage0_string_len_cache[CTX_STAGE0_STRING_LEN_CACHE_BUCKETS] = {0};

void *ctx_stage0_alloc_perm(long long size) {
    size_t resolved = size > 0 ? (size_t)size : 1u;
    return arena_alloc(&ctx_stage0_perm_arena, resolved);
}

void *ctx_stage0_alloc_scratch(long long size) {
    size_t resolved = size > 0 ? (size_t)size : 1u;
    return arena_alloc(&ctx_stage0_scratch_arena, resolved);
}

void ctx_stage0_reset_scratch(void) {
    arena_reset(&ctx_stage0_scratch_arena);
}

static size_t ctx_stage0_string_len_bucket(const char *ptr) {
    uintptr_t bits = (uintptr_t)ptr;
    bits ^= bits >> 33;
    bits *= (uintptr_t)0xff51afd7ed558ccdULL;
    bits ^= bits >> 33;
    return (size_t)(bits & (CTX_STAGE0_STRING_LEN_CACHE_BUCKETS - 1u));
}

static void ctx_stage0_register_perm_string_len(const char *ptr, size_t len) {
    if (ptr == NULL || len == 0) {
        return;
    }
    size_t bucket = ctx_stage0_string_len_bucket(ptr);
    for (ctx_stage0_string_len_cache_entry *entry = ctx_stage0_string_len_cache[bucket]; entry != NULL; entry = entry->next) {
        if (entry->ptr == ptr) {
            entry->len = len;
            return;
        }
    }
    ctx_stage0_string_len_cache_entry *entry = (ctx_stage0_string_len_cache_entry *)ctx_stage0_alloc_perm((long long)sizeof(ctx_stage0_string_len_cache_entry));
    entry->next = ctx_stage0_string_len_cache[bucket];
    entry->ptr = ptr;
    entry->len = len;
    ctx_stage0_string_len_cache[bucket] = entry;
}

static int ctx_stage0_lookup_cached_string_len(const char *ptr, size_t *len_out) {
    if (ptr == NULL) {
        return 0;
    }
    size_t bucket = ctx_stage0_string_len_bucket(ptr);
    ctx_stage0_string_len_cache_entry *prev = NULL;
    for (ctx_stage0_string_len_cache_entry *entry = ctx_stage0_string_len_cache[bucket]; entry != NULL; prev = entry, entry = entry->next) {
        if (entry->ptr == ptr) {
            if (prev != NULL) {
                prev->next = entry->next;
                entry->next = ctx_stage0_string_len_cache[bucket];
                ctx_stage0_string_len_cache[bucket] = entry;
            }
            *len_out = entry->len;
            return 1;
        }
    }
    return 0;
}

static size_t ctx_stage0_runtime_strlen(const char *value) {
    const char *src = value ? value : "";
    size_t len = 0;
    if (src[0] == '\0') {
        return 0;
    }
    if (ctx_stage0_lookup_cached_string_len(src, &len)) {
        return len;
    }
    return strlen(src);
}

static char *ctx_stage0_intern_small_string(const char *src, size_t len) {
    if (len == 0) {
        return "";
    }
    unsigned long long hash = 1469598103934665603ULL;
    for (size_t i = 0; i < len; i++) {
        hash ^= (unsigned char)src[i];
        hash *= 1099511628211ULL;
    }
    size_t bucket = (size_t)(hash & (CTX_STAGE0_SMALL_STRING_CACHE_BUCKETS - 1u));
    for (ctx_stage0_small_string_cache_entry *entry = ctx_stage0_small_string_cache[bucket]; entry != NULL; entry = entry->next) {
        if (entry->len == len && memcmp(entry->text, src, len) == 0) {
            return entry->text;
        }
    }
    ctx_stage0_small_string_cache_entry *entry = (ctx_stage0_small_string_cache_entry *)ctx_stage0_alloc_perm((long long)(sizeof(ctx_stage0_small_string_cache_entry) + len + 1));
    entry->next = ctx_stage0_small_string_cache[bucket];
    entry->len = len;
    memcpy(entry->text, src, len);
    entry->text[len] = '\0';
    ctx_stage0_small_string_cache[bucket] = entry;
    ctx_stage0_register_perm_string_len(entry->text, len);
    return entry->text;
}

static char *ctx_stage0_int_to_string_into(Arena *arena, long long value) {
    int len = snprintf(NULL, 0, "%lld", value);
    if (len > 0 && len <= 8) {
        char text[9];
        snprintf(text, (size_t)len + 1, "%lld", value);
        return ctx_stage0_intern_small_string(text, (size_t)len);
    }
    char *out = (char *)arena_alloc(arena, (size_t)len + 1);
    snprintf(out, (size_t)len + 1, "%lld", value);
    if (arena == &ctx_stage0_perm_arena) {
        ctx_stage0_register_perm_string_len(out, (size_t)len);
    }
    return out;
}

static char *ctx_stage0_bool_to_string_into(Arena *arena, int value) {
    (void)arena;
    const char *src = value ? "True" : "False";
    return (char *)src;
}

static char *ctx_stage0_char_to_string_into(Arena *arena, long long value) {
    (void)arena;
    char text[2];
    text[0] = (char)(unsigned char)value;
    text[1] = '\0';
    return ctx_stage0_intern_small_string(text, 1);
}

typedef struct {
    long long len;
    long long cap;
    char *data;
} ctx_stage0_string_builder;

static long long ctx_stage0_string_builder_next_cap(long long min_cap) {
    long long cap = 16;
    while (cap < min_cap) {
        cap *= 2;
    }
    return cap;
}

ctx_stage0_string_builder *ctx_stage0_string_builder_new(const char *initial) {
    const char *src = initial ? initial : "";
    size_t len = ctx_stage0_runtime_strlen(src);
    ctx_stage0_string_builder *builder = (ctx_stage0_string_builder *)ctx_stage0_alloc_perm((long long)sizeof(ctx_stage0_string_builder));
    builder->len = (long long)len;
    builder->cap = ctx_stage0_string_builder_next_cap((long long)len + 1);
    builder->data = (char *)ctx_stage0_alloc_perm(builder->cap);
    if (len > 0) {
        memcpy(builder->data, src, len);
    }
    builder->data[len] = '\0';
    return builder;
}

ctx_stage0_string_builder *ctx_stage0_string_builder_append(ctx_stage0_string_builder *builder, const char *suffix) {
    const char *src = suffix ? suffix : "";
    size_t suffix_len = ctx_stage0_runtime_strlen(src);
    if (builder == NULL) {
        return ctx_stage0_string_builder_new(src);
    }
    if (suffix_len == 0) {
        return builder;
    }
    long long needed = builder->len + (long long)suffix_len + 1;
    if (needed > builder->cap) {
        long long new_cap = ctx_stage0_string_builder_next_cap(needed);
        char *new_data = (char *)ctx_stage0_alloc_perm(new_cap);
        if (builder->len > 0) {
            memcpy(new_data, builder->data, (size_t)builder->len);
        }
        builder->data = new_data;
        builder->cap = new_cap;
    }
    memcpy(builder->data + builder->len, src, suffix_len);
    builder->len += (long long)suffix_len;
    builder->data[builder->len] = '\0';
    return builder;
}

char *ctx_stage0_string_builder_finish(ctx_stage0_string_builder *builder) {
    if (builder == NULL || builder->len <= 0) {
        return "";
    }
    if (builder->len <= 8) {
        return ctx_stage0_intern_small_string(builder->data, (size_t)builder->len);
    }
    ctx_stage0_register_perm_string_len(builder->data, (size_t)builder->len);
    return builder->data;
}
