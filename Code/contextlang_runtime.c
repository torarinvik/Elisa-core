#include <stdlib.h>
#include <stdio.h>
#include <string.h>

#define ARENA_IMPLEMENTATION
#include "arena.h"

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

typedef struct {
    long long len;
    long long cap;
    long long elem_size;
    void **data;
    unsigned char *inline_boxes;
    long long inline_box_stride;
} ctx_stage0_list;

typedef struct {
    void **data;
    long long len;
    long long elem_size;
} ctx_stage0_list_view;

static ctx_stage0_list *ctx_stage0_list_alloc_with_boxes(long long cap, long long elem_size, int preallocate_boxes) {
    if (cap < 0) {
        cap = 0;
    }
    size_t data_bytes = cap > 0 ? (size_t)cap * sizeof(void *) : 0u;
    size_t inline_box_size = preallocate_boxes && elem_size > 0 ? (size_t)elem_size : 0u;
    size_t inline_box_bytes = inline_box_size > 0 && cap > 0 ? (size_t)cap * inline_box_size : 0u;
    size_t total_bytes = sizeof(ctx_stage0_list) + data_bytes + inline_box_bytes;
    unsigned char *raw = (unsigned char *)ctx_stage0_alloc_perm((long long)total_bytes);
    ctx_stage0_list *out = (ctx_stage0_list *)raw;
    out->len = 0;
    out->cap = cap;
    out->elem_size = elem_size > 0 ? elem_size : 0;
    out->data = cap > 0 ? (void **)(raw + sizeof(ctx_stage0_list)) : NULL;
    out->inline_boxes = inline_box_bytes > 0 ? raw + sizeof(ctx_stage0_list) + data_bytes : NULL;
    out->inline_box_stride = inline_box_size > 0 ? (long long)inline_box_size : 0;
    return out;
}

static ctx_stage0_list *ctx_stage0_list_alloc(long long cap, long long elem_size) {
    return ctx_stage0_list_alloc_with_boxes(cap, elem_size, 0);
}

static void *ctx_stage0_box_value(const void *value, long long elem_size) {
    long long box_size = elem_size > 0 ? elem_size : 1;
    void *out = ctx_stage0_alloc_perm(box_size);
    memset(out, 0, (size_t)box_size);
    if (value != NULL && elem_size > 0) {
        memcpy(out, value, (size_t)elem_size);
    }
    return out;
}

static void ctx_stage0_write_box_value(void *out, const void *value, long long elem_size) {
    long long box_size = elem_size > 0 ? elem_size : 1;
    memset(out, 0, (size_t)box_size);
    if (value != NULL && elem_size > 0) {
        memcpy(out, value, (size_t)elem_size);
    }
}

ctx_stage0_list *ctx_stage0_list_new(void) {
    return ctx_stage0_list_alloc(0, 0);
}

ctx_stage0_list *ctx_stage0_list_new_reserve(long long cap, long long elem_size) {
    return ctx_stage0_list_alloc_with_boxes(cap, elem_size, 1);
}

ctx_stage0_list *ctx_stage0_list_reserve(ctx_stage0_list *list, long long cap, long long elem_size) {
    long long resolved_cap = cap >= 0 ? cap : 0;
    long long resolved_elem_size = list && list->elem_size > 0 ? list->elem_size : elem_size;
    if (list == NULL) {
        return ctx_stage0_list_new_reserve(resolved_cap, resolved_elem_size);
    }
    if (list->elem_size <= 0 && elem_size > 0) {
        list->elem_size = elem_size;
        resolved_elem_size = elem_size;
    }
    if (resolved_cap <= list->cap) {
        return list;
    }
    ctx_stage0_list *grown = ctx_stage0_list_alloc_with_boxes(resolved_cap, resolved_elem_size, 1);
    if (grown == NULL) {
        return NULL;
    }
    grown->len = list->len;
    if (list->len > 0 && list->data != NULL) {
        if (list->inline_boxes != NULL && list->inline_box_stride == grown->inline_box_stride) {
            memcpy(grown->inline_boxes, list->inline_boxes, (size_t)list->len * (size_t)grown->inline_box_stride);
            for (long long i = 0; i < list->len; i++) {
                grown->data[i] = grown->inline_boxes + ((size_t)i * (size_t)grown->inline_box_stride);
            }
        } else {
            memcpy(grown->data, list->data, (size_t)list->len * sizeof(void *));
        }
    }
    return grown;
}

ctx_stage0_list *ctx_stage0_list_push_mut(ctx_stage0_list *list, const void *value, long long elem_size) {
    long long resolved_elem_size = list && list->elem_size > 0 ? list->elem_size : elem_size;
    long long box_size = resolved_elem_size > 0 ? resolved_elem_size : 1;
    if (list == NULL) {
        list = ctx_stage0_list_alloc(4, resolved_elem_size);
        if (list == NULL) {
            return NULL;
        }
    }
    if (list->elem_size <= 0 && elem_size > 0) {
        list->elem_size = elem_size;
        resolved_elem_size = elem_size;
        box_size = resolved_elem_size;
    }
    if (list->len >= list->cap) {
        long long new_cap = list->cap > 0 ? list->cap * 2 : 4;
        ctx_stage0_list *grown = ctx_stage0_list_alloc_with_boxes(new_cap, resolved_elem_size, 1);
        grown->len = list->len;
        if (list->len > 0 && list->data != NULL) {
            if (list->inline_boxes != NULL && list->inline_box_stride == grown->inline_box_stride) {
                memcpy(grown->inline_boxes, list->inline_boxes, (size_t)list->len * (size_t)grown->inline_box_stride);
                for (long long i = 0; i < list->len; i++) {
                    grown->data[i] = grown->inline_boxes + ((size_t)i * (size_t)grown->inline_box_stride);
                }
            } else {
                for (long long i = 0; i < list->len; i++) {
                    void *slot = grown->inline_boxes + ((size_t)i * (size_t)grown->inline_box_stride);
                    grown->data[i] = slot;
                    ctx_stage0_write_box_value(slot, list->data[i], resolved_elem_size);
                }
            }
        }
        grown->data[grown->len] = grown->inline_boxes + ((size_t)grown->len * (size_t)grown->inline_box_stride);
        ctx_stage0_write_box_value(grown->data[grown->len], value, resolved_elem_size);
        grown->len += 1;
        return grown;
    }
    if (list->inline_boxes != NULL && list->inline_box_stride == resolved_elem_size) {
        list->data[list->len] = list->inline_boxes + ((size_t)list->len * (size_t)list->inline_box_stride);
        ctx_stage0_write_box_value(list->data[list->len], value, resolved_elem_size);
    } else {
        list->data[list->len] = ctx_stage0_box_value(value, resolved_elem_size);
    }
    list->len += 1;
    return list;
}

ctx_stage0_list *ctx_stage0_list_push(ctx_stage0_list *list, const void *value, long long elem_size) {
    long long old_len = list ? list->len : 0;
    long long old_cap = list ? list->cap : 0;
    long long new_cap = old_cap > old_len ? old_cap : old_len + 1;
    long long resolved_elem_size = list && list->elem_size > 0 ? list->elem_size : elem_size;
    long long box_size = resolved_elem_size > 0 ? resolved_elem_size : 1;
    if (new_cap < 4) {
        new_cap = 4;
    }
    size_t data_bytes = (size_t)new_cap * sizeof(void *);
    size_t total_bytes = sizeof(ctx_stage0_list) + data_bytes + (size_t)box_size;
    unsigned char *raw = (unsigned char *)ctx_stage0_alloc_perm((long long)total_bytes);
    ctx_stage0_list *out = (ctx_stage0_list *)raw;
    if (out == NULL) {
        return NULL;
    }
    out->len = 0;
    out->cap = new_cap;
    out->elem_size = resolved_elem_size;
    out->data = (void **)(raw + sizeof(ctx_stage0_list));
    if (list != NULL && list->len > 0 && list->data != NULL) {
        memcpy(out->data, list->data, (size_t)list->len * sizeof(void *));
    }
    out->len = old_len + 1;
    out->data[old_len] = raw + sizeof(ctx_stage0_list) + data_bytes;
    ctx_stage0_write_box_value(out->data[old_len], value, resolved_elem_size);
    return out;
}

ctx_stage0_list *ctx_stage0_list_concat(ctx_stage0_list *lhs, ctx_stage0_list *rhs) {
    long long left_len = lhs ? lhs->len : 0;
    long long right_len = rhs ? rhs->len : 0;
    if (left_len == 0) {
        return rhs ? rhs : ctx_stage0_list_new();
    }
    if (right_len == 0) {
        return lhs;
    }
    long long elem_size = 0;
    if (lhs != NULL && lhs->elem_size > 0) {
        elem_size = lhs->elem_size;
    } else if (rhs != NULL) {
        elem_size = rhs->elem_size;
    }
    ctx_stage0_list *out = ctx_stage0_list_alloc(left_len + right_len, elem_size);
    if (out == NULL) {
        return NULL;
    }
    if (lhs != NULL && lhs->len > 0 && lhs->data != NULL) {
        memcpy(out->data, lhs->data, (size_t)lhs->len * sizeof(void *));
    }
    if (rhs != NULL && rhs->len > 0 && rhs->data != NULL) {
        memcpy(out->data + left_len, rhs->data, (size_t)rhs->len * sizeof(void *));
    }
    out->len = left_len + right_len;
    return out;
}

ctx_stage0_list *ctx_stage0_list_truncate(ctx_stage0_list *list, long long size) {
    if (list == NULL) {
        return ctx_stage0_list_new();
    }
    long long new_len = size;
    if (new_len < 0) {
        new_len = 0;
    }
    if (new_len > list->len) {
        new_len = list->len;
    }
    list->len = new_len;
    return list;
}

ctx_stage0_list *ctx_stage0_list_clear(ctx_stage0_list *list) {
    return ctx_stage0_list_truncate(list, 0);
}

ctx_stage0_list_view ctx_stage0_list_view_make(ctx_stage0_list *list, long long start, long long end) {
    ctx_stage0_list_view view;
    view.data = NULL;
    view.len = 0;
    view.elem_size = list ? list->elem_size : 0;
    if (list == NULL || list->len <= 0 || list->data == NULL) {
        return view;
    }
    long long lo = start > 0 ? start : 0;
    long long hi = end >= 0 ? end : list->len;
    if (lo > list->len) {
        lo = list->len;
    }
    if (hi > list->len) {
        hi = list->len;
    }
    if (hi < lo) {
        hi = lo;
    }
    if (hi == lo) {
        return view;
    }
    view.data = list->data + lo;
    view.len = hi - lo;
    return view;
}

long long ctx_stage0_list_view_len(ctx_stage0_list_view view) {
    return view.len;
}

ctx_stage0_list_view ctx_stage0_list_view_slice(ctx_stage0_list_view view, long long start, long long end) {
    ctx_stage0_list_view out;
    out.data = NULL;
    out.len = 0;
    out.elem_size = view.elem_size;
    if (view.data == NULL || view.len <= 0) {
        return out;
    }
    long long lo = start > 0 ? start : 0;
    long long hi = end >= 0 ? end : view.len;
    if (lo > view.len) {
        lo = view.len;
    }
    if (hi > view.len) {
        hi = view.len;
    }
    if (hi < lo) {
        hi = lo;
    }
    if (hi == lo) {
        return out;
    }
    out.data = view.data + lo;
    out.len = hi - lo;
    return out;
}

void *ctx_stage0_list_view_get(ctx_stage0_list_view view, long long index, long long elem_size) {
    if (view.data == NULL || index < 0 || index >= view.len) {
        long long fallback_elem_size = elem_size > 0 ? elem_size : view.elem_size;
        return ctx_stage0_box_value(NULL, fallback_elem_size);
    }
    return view.data[index];
}

ctx_stage0_list *ctx_stage0_list_view_copy(ctx_stage0_list_view view) {
    if (view.len <= 0) {
        return ctx_stage0_list_new_reserve(0, view.elem_size);
    }
    ctx_stage0_list *out = ctx_stage0_list_alloc(view.len, view.elem_size);
    if (out == NULL) {
        return NULL;
    }
    out->len = view.len;
    if (view.data != NULL && out->data != NULL) {
        memcpy(out->data, view.data, (size_t)view.len * sizeof(void *));
    }
    return out;
}

long long ctx_stage0_list_len(ctx_stage0_list *list) {
    return list ? list->len : 0;
}

void *ctx_stage0_list_get(ctx_stage0_list *list, long long index, long long elem_size) {
    if (list == NULL || index < 0 || index >= list->len) {
        long long fallback_elem_size = elem_size > 0 ? elem_size : (list ? list->elem_size : 0);
        return ctx_stage0_box_value(NULL, fallback_elem_size);
    }
    return list->data[index];
}

long long ctx_stage0_list_set(ctx_stage0_list *list, long long index, const void *value, long long elem_size) {
    if (list == NULL || index < 0 || index >= list->len) {
        return 0;
    }
    if (list->elem_size <= 0 && elem_size > 0) {
        list->elem_size = elem_size;
    }
    long long resolved_elem_size = list->elem_size > 0 ? list->elem_size : elem_size;
    if (list->data[index] == NULL) {
        list->data[index] = ctx_stage0_box_value(value, resolved_elem_size);
    } else {
        ctx_stage0_write_box_value(list->data[index], value, resolved_elem_size);
    }
    return 1;
}

char *ctx_stage0_concat2(const char *lhs, const char *rhs) {
    const char *left = lhs ? lhs : "";
    const char *right = rhs ? rhs : "";

    size_t left_len = ctx_stage0_runtime_strlen(left);
    size_t right_len = ctx_stage0_runtime_strlen(right);
    if (left_len == 0) {
        return (char *)right;
    }
    if (right_len == 0) {
        return (char *)left;
    }
    if (left_len + right_len <= 8) {
        char text[9];
        memcpy(text, left, left_len);
        memcpy(text + left_len, right, right_len);
        text[left_len + right_len] = '\0';
        return ctx_stage0_intern_small_string(text, left_len + right_len);
    }
    char *out = (char *)ctx_stage0_alloc_perm((long long)(left_len + right_len + 1));

    memcpy(out, left, left_len);
    memcpy(out + left_len, right, right_len);
    out[left_len + right_len] = '\0';
    ctx_stage0_register_perm_string_len(out, left_len + right_len);
    return out;
}

char *ctx_stage0_concat2_scratch(const char *lhs, const char *rhs) {
    const char *left = lhs ? lhs : "";
    const char *right = rhs ? rhs : "";

    size_t left_len = ctx_stage0_runtime_strlen(left);
    size_t right_len = ctx_stage0_runtime_strlen(right);
    if (left_len == 0) {
        return (char *)right;
    }
    if (right_len == 0) {
        return (char *)left;
    }
    if (left_len + right_len <= 8) {
        char text[9];
        memcpy(text, left, left_len);
        memcpy(text + left_len, right, right_len);
        text[left_len + right_len] = '\0';
        return ctx_stage0_intern_small_string(text, left_len + right_len);
    }
    char *out = (char *)ctx_stage0_alloc_scratch((long long)(left_len + right_len + 1));

    memcpy(out, left, left_len);
    memcpy(out + left_len, right, right_len);
    out[left_len + right_len] = '\0';
    return out;
}

char *ctx_stage0_int_to_string(long long value) {
    return ctx_stage0_int_to_string_into(&ctx_stage0_perm_arena, value);
}

char *ctx_stage0_int_to_string_scratch(long long value) {
    return ctx_stage0_int_to_string_into(&ctx_stage0_scratch_arena, value);
}

char *ctx_stage0_bool_to_string(int value) {
    return ctx_stage0_bool_to_string_into(&ctx_stage0_perm_arena, value);
}

char *ctx_stage0_bool_to_string_scratch(int value) {
    return ctx_stage0_bool_to_string_into(&ctx_stage0_scratch_arena, value);
}

char *ctx_stage0_char_to_string(long long value) {
    return ctx_stage0_char_to_string_into(&ctx_stage0_perm_arena, value);
}

char *ctx_stage0_char_to_string_scratch(long long value) {
    return ctx_stage0_char_to_string_into(&ctx_stage0_scratch_arena, value);
}

long long ctx_stage0_strlen(const char *value) {
    return (long long)ctx_stage0_runtime_strlen(value);
}

int ctx_stage0_streq(const char *lhs, const char *rhs) {
    const char *left = lhs ? lhs : "";
    const char *right = rhs ? rhs : "";
    if (left == right) {
        return 1;
    }
    return strcmp(left, right) == 0 ? 1 : 0;
}

long long ctx_stage0_string_index(const char *value, long long index) {
    const char *src = value ? value : "";
    size_t len = ctx_stage0_runtime_strlen(src);
    if (index < 0 || (size_t)index >= len) {
        return -1;
    }
    return (unsigned char)src[index];
}

int ctx_stage0_string_slice_eq(const char *value, long long start, long long end, const char *other) {
    const char *src = value ? value : "";
    const char *rhs = other ? other : "";
    size_t len = ctx_stage0_runtime_strlen(src);
    size_t lo = 0;
    size_t hi = len;

    if (start > 0) {
        lo = (size_t)start;
        if (lo > len) {
            lo = len;
        }
    }
    if (end >= 0) {
        hi = (size_t)end;
        if (hi > len) {
            hi = len;
        }
    }
    if (hi < lo) {
        hi = lo;
    }

    size_t slice_len = hi - lo;
    size_t rhs_len = ctx_stage0_runtime_strlen(rhs);
    if (slice_len != rhs_len) {
        return 0;
    }
    if (slice_len == 0) {
        return 1;
    }
    if (src + lo == rhs) {
        return 1;
    }
    return memcmp(src + lo, rhs, slice_len) == 0 ? 1 : 0;
}

int ctx_stage0_string_slices_eq(const char *lhs, long long lhs_start, long long lhs_end, const char *rhs, long long rhs_start, long long rhs_end) {
    const char *left = lhs ? lhs : "";
    const char *right = rhs ? rhs : "";
    size_t left_len = ctx_stage0_runtime_strlen(left);
    size_t right_len = ctx_stage0_runtime_strlen(right);
    size_t left_lo = 0;
    size_t left_hi = left_len;
    size_t right_lo = 0;
    size_t right_hi = right_len;

    if (lhs_start > 0) {
        left_lo = (size_t)lhs_start;
        if (left_lo > left_len) {
            left_lo = left_len;
        }
    }
    if (lhs_end >= 0) {
        left_hi = (size_t)lhs_end;
        if (left_hi > left_len) {
            left_hi = left_len;
        }
    }
    if (left_hi < left_lo) {
        left_hi = left_lo;
    }

    if (rhs_start > 0) {
        right_lo = (size_t)rhs_start;
        if (right_lo > right_len) {
            right_lo = right_len;
        }
    }
    if (rhs_end >= 0) {
        right_hi = (size_t)rhs_end;
        if (right_hi > right_len) {
            right_hi = right_len;
        }
    }
    if (right_hi < right_lo) {
        right_hi = right_lo;
    }

    size_t left_slice_len = left_hi - left_lo;
    size_t right_slice_len = right_hi - right_lo;
    if (left_slice_len != right_slice_len) {
        return 0;
    }
    if (left_slice_len == 0) {
        return 1;
    }
    if (left + left_lo == right + right_lo) {
        return 1;
    }
    return memcmp(left + left_lo, right + right_lo, left_slice_len) == 0 ? 1 : 0;
}

char *ctx_stage0_string_slice(const char *value, long long start, long long end) {
    const char *src = value ? value : "";
    size_t len = ctx_stage0_runtime_strlen(src);
    size_t lo = 0;
    size_t hi = len;

    if (start > 0) {
        lo = (size_t)start;
        if (lo > len) {
            lo = len;
        }
    }
    if (end >= 0) {
        hi = (size_t)end;
        if (hi > len) {
            hi = len;
        }
    }
    if (hi < lo) {
        hi = lo;
    }

    if (lo == 0 && hi == len) {
        return (char *)src;
    }

    size_t out_len = hi - lo;
    if (out_len == 0) {
        return "";
    }
    if (out_len <= 8) {
        return ctx_stage0_intern_small_string(src + lo, out_len);
    }
    char *out = (char *)ctx_stage0_alloc_perm((long long)(out_len + 1));
    memcpy(out, src + lo, out_len);
    out[out_len] = '\0';
    ctx_stage0_register_perm_string_len(out, out_len);
    return out;
}

ctx_stage0_string_view ctx_stage0_string_view_make(const char *value, long long start, long long end) {
    const char *src = value ? value : "";
    size_t len = ctx_stage0_runtime_strlen(src);
    size_t lo = 0;
    size_t hi = len;
    if (start > 0) {
        lo = (size_t)start;
        if (lo > len) {
            lo = len;
        }
    }
    if (end >= 0) {
        hi = (size_t)end;
        if (hi > len) {
            hi = len;
        }
    }
    if (hi < lo) {
        hi = lo;
    }
    ctx_stage0_string_view view;
    view.data = (char *)(src + lo);
    view.len = (long long)(hi - lo);
    return view;
}

long long ctx_stage0_string_view_len(ctx_stage0_string_view view) {
    return view.len;
}

ctx_stage0_string_view ctx_stage0_string_view_slice(ctx_stage0_string_view view, long long start, long long end) {
    long long lo = start > 0 ? start : 0;
    long long hi = end >= 0 ? end : view.len;
    if (lo > view.len) {
        lo = view.len;
    }
    if (hi > view.len) {
        hi = view.len;
    }
    if (hi < lo) {
        hi = lo;
    }
    ctx_stage0_string_view out;
    out.data = view.data + lo;
    out.len = hi - lo;
    return out;
}

long long ctx_stage0_string_view_index(ctx_stage0_string_view view, long long index) {
    if (index < 0 || index >= view.len) {
        return -1;
    }
    return (unsigned char)view.data[index];
}

int ctx_stage0_string_view_eq(ctx_stage0_string_view view, const char *other) {
    const char *rhs = other ? other : "";
    size_t rhs_len = ctx_stage0_runtime_strlen(rhs);
    if (view.len < 0 || (size_t)view.len != rhs_len) {
        return 0;
    }
    if (view.len == 0) {
        return 1;
    }
    if (view.data == rhs) {
        return 1;
    }
    return memcmp(view.data, rhs, (size_t)view.len) == 0 ? 1 : 0;
}

int ctx_stage0_string_views_eq(ctx_stage0_string_view lhs, ctx_stage0_string_view rhs) {
    if (lhs.len != rhs.len) {
        return 0;
    }
    if (lhs.len == 0) {
        return 1;
    }
    if (lhs.data == rhs.data) {
        return 1;
    }
    return memcmp(lhs.data, rhs.data, (size_t)lhs.len) == 0 ? 1 : 0;
}

char *ctx_stage0_string_view_copy(ctx_stage0_string_view view) {
    if (view.len <= 0) {
        return "";
    }
    if (view.len <= 8) {
        return ctx_stage0_intern_small_string(view.data, (size_t)view.len);
    }
    char *out = (char *)ctx_stage0_alloc_perm(view.len + 1);
    memcpy(out, view.data, (size_t)view.len);
    out[view.len] = '\0';
    ctx_stage0_register_perm_string_len(out, (size_t)view.len);
    return out;
}

char *ctx_stage1rt_concat2(const char *lhs, const char *rhs) {
    return ctx_stage0_concat2(lhs, rhs);
}

char *ctx_stage1rt_concat2_scratch(const char *lhs, const char *rhs) {
    return ctx_stage0_concat2_scratch(lhs, rhs);
}

ctx_stage0_string_builder *ctx_stage1rt_string_builder_new(const char *initial) {
    return ctx_stage0_string_builder_new(initial);
}

ctx_stage0_string_builder *ctx_stage1rt_string_builder_append(ctx_stage0_string_builder *builder, const char *suffix) {
    return ctx_stage0_string_builder_append(builder, suffix);
}

char *ctx_stage1rt_string_builder_finish(ctx_stage0_string_builder *builder) {
    return ctx_stage0_string_builder_finish(builder);
}

char *ctx_stage1rt_int_to_string(long long value) {
    return ctx_stage0_int_to_string(value);
}

char *ctx_stage1rt_int_to_string_scratch(long long value) {
    return ctx_stage0_int_to_string_scratch(value);
}

char *ctx_stage1rt_bool_to_string(int value) {
    return ctx_stage0_bool_to_string(value);
}

char *ctx_stage1rt_bool_to_string_scratch(int value) {
    return ctx_stage0_bool_to_string_scratch(value);
}

char *ctx_stage1rt_char_to_string(long long value) {
    return ctx_stage0_char_to_string(value);
}

char *ctx_stage1rt_char_to_string_scratch(long long value) {
    return ctx_stage0_char_to_string_scratch(value);
}

long long ctx_stage1rt_strlen(const char *value) {
    return ctx_stage0_strlen(value);
}

int ctx_stage1rt_streq(const char *lhs, const char *rhs) {
    return ctx_stage0_streq(lhs, rhs);
}

int ctx_stage1rt_string_slice_eq(const char *value, long long start, long long end, const char *other) {
    return ctx_stage0_string_slice_eq(value, start, end, other);
}

int ctx_stage1rt_string_slices_eq(const char *lhs, long long lhs_start, long long lhs_end, const char *rhs, long long rhs_start, long long rhs_end) {
    return ctx_stage0_string_slices_eq(lhs, lhs_start, lhs_end, rhs, rhs_start, rhs_end);
}

long long ctx_stage1rt_string_index(const char *value, long long index) {
    return ctx_stage0_string_index(value, index);
}

char *ctx_stage1rt_string_slice(const char *value, long long start, long long end) {
    return ctx_stage0_string_slice(value, start, end);
}

ctx_stage0_string_view ctx_stage1rt_string_view(const char *value, long long start, long long end) {
    return ctx_stage0_string_view_make(value, start, end);
}

long long ctx_stage1rt_string_view_len(ctx_stage0_string_view view) {
    return ctx_stage0_string_view_len(view);
}

ctx_stage0_string_view ctx_stage1rt_string_view_slice(ctx_stage0_string_view view, long long start, long long end) {
    return ctx_stage0_string_view_slice(view, start, end);
}

long long ctx_stage1rt_string_view_index(ctx_stage0_string_view view, long long index) {
    return ctx_stage0_string_view_index(view, index);
}

int ctx_stage1rt_string_view_eq(ctx_stage0_string_view view, const char *other) {
    return ctx_stage0_string_view_eq(view, other);
}

int ctx_stage1rt_string_views_eq(ctx_stage0_string_view lhs, ctx_stage0_string_view rhs) {
    return ctx_stage0_string_views_eq(lhs, rhs);
}

char *ctx_stage1rt_string_from_view(ctx_stage0_string_view view) {
    return ctx_stage0_string_view_copy(view);
}

ctx_stage0_list *ctx_stage1rt_list_new(void) {
    return ctx_stage0_list_new();
}

ctx_stage0_list *ctx_stage1rt_list_new_reserve(long long cap, long long elem_size) {
    return ctx_stage0_list_new_reserve(cap, elem_size);
}

ctx_stage0_list *ctx_stage1rt_list_reserve(ctx_stage0_list *values, long long cap, long long elem_size) {
    return ctx_stage0_list_reserve(values, cap, elem_size);
}

ctx_stage0_list *ctx_stage1rt_list_push(ctx_stage0_list *values, const void *elem, long long elem_size) {
    return ctx_stage0_list_push(values, elem, elem_size);
}

ctx_stage0_list *ctx_stage1rt_list_push_mut(ctx_stage0_list *values, const void *elem, long long elem_size) {
    return ctx_stage0_list_push_mut(values, elem, elem_size);
}

ctx_stage0_list *ctx_stage1rt_list_concat(ctx_stage0_list *left, ctx_stage0_list *right) {
    return ctx_stage0_list_concat(left, right);
}

ctx_stage0_list *ctx_stage1rt_list_truncate(ctx_stage0_list *values, long long size) {
    return ctx_stage0_list_truncate(values, size);
}

ctx_stage0_list *ctx_stage1rt_list_clear(ctx_stage0_list *values) {
    return ctx_stage0_list_clear(values);
}

ctx_stage0_list_view ctx_stage1rt_list_view(ctx_stage0_list *values, long long start, long long end) {
    return ctx_stage0_list_view_make(values, start, end);
}

long long ctx_stage1rt_list_view_len(ctx_stage0_list_view view) {
    return ctx_stage0_list_view_len(view);
}

ctx_stage0_list_view ctx_stage1rt_list_view_slice(ctx_stage0_list_view view, long long start, long long end) {
    return ctx_stage0_list_view_slice(view, start, end);
}

void *ctx_stage1rt_list_view_get(ctx_stage0_list_view view, long long index, long long elem_size) {
    return ctx_stage0_list_view_get(view, index, elem_size);
}

ctx_stage0_list *ctx_stage1rt_list_from_view(ctx_stage0_list_view view) {
    return ctx_stage0_list_view_copy(view);
}

long long ctx_stage1rt_list_len(ctx_stage0_list *values) {
    return ctx_stage0_list_len(values);
}

void *ctx_stage1rt_list_get(ctx_stage0_list *values, long long index, long long elem_size) {
    return ctx_stage0_list_get(values, index, elem_size);
}

long long ctx_stage1rt_list_set(ctx_stage0_list *values, long long index, const void *elem, long long elem_size) {
    return ctx_stage0_list_set(values, index, elem, elem_size);
}

void ctx_stage1rt_reset_scratch(void) {
    ctx_stage0_reset_scratch();
}

int ctx_stage1rt_puts(const char *value) {
    return puts(value ? value : "");
}

__attribute__((weak)) void ctx_llvm_codegen_fatal(const char *msg) {
    fprintf(stderr, "ctx_llvm_codegen_fatal: %s\n", msg ? msg : "<null>");
    exit(1);
}
