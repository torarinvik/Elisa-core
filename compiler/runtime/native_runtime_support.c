#if defined(__clang__) || defined(__GNUC__)
#define CTX_RUNTIME_WEAK __attribute__((weak))
#else
#define CTX_RUNTIME_WEAK
#endif

#define ARENA_API CTX_RUNTIME_WEAK
#define ARENA_IMPLEMENTATION
#include "arena_reference.h"

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    uint8_t *data;
    int64_t len;
} StringView;

typedef struct {
    void *data;
    int64_t len;
    int64_t elem_size;
} DynArrayView;

static uint8_t llcontext_empty_string[] = "";

static uint8_t *llcontext_nonnull_string(uint8_t *value) {
    return value != NULL ? value : llcontext_empty_string;
}

static int64_t llcontext_strlen_i64(uint8_t *value) {
    return (int64_t)strlen((const char *)llcontext_nonnull_string(value));
}

static int64_t llcontext_clamp_i64(int64_t value, int64_t low, int64_t high) {
    if (value < low) {
        return low;
    }
    if (value > high) {
        return high;
    }
    return value;
}

static StringView llcontext_string_view_clamped(uint8_t *value, int64_t start, int64_t end) {
    uint8_t *data = llcontext_nonnull_string(value);
    int64_t len = llcontext_strlen_i64(data);
    int64_t lo = llcontext_clamp_i64(start, 0, len);
    int64_t hi = llcontext_clamp_i64(end, lo, len);
    StringView view;
    view.data = data + lo;
    view.len = hi - lo;
    return view;
}

static void *llcontext_offset_ptr(void *data, size_t offset_bytes) {
    if (data == NULL) {
        return NULL;
    }
    return (uint8_t *)data + offset_bytes;
}

static int64_t llcontext_bytes_equal(const uint8_t *left, const uint8_t *right, int64_t len) {
    if (len <= 0) {
        return 1;
    }
    return memcmp(left, right, (size_t)len) == 0 ? 1 : 0;
}

CTX_RUNTIME_WEAK int64_t ctx_strlen(uint8_t *value) {
    return llcontext_strlen_i64(value);
}

CTX_RUNTIME_WEAK int64_t ctx_streq(uint8_t *lhs, uint8_t *rhs) {
    uint8_t *left = llcontext_nonnull_string(lhs);
    uint8_t *right = llcontext_nonnull_string(rhs);
    return strcmp((const char *)left, (const char *)right) == 0 ? 1 : 0;
}

CTX_RUNTIME_WEAK int64_t ctx_string_index(uint8_t *value, int64_t index) {
    uint8_t *data = llcontext_nonnull_string(value);
    int64_t len = llcontext_strlen_i64(data);
    if (index < 0 || index >= len) {
        return 0;
    }
    return (int64_t)data[index];
}

CTX_RUNTIME_WEAK uint8_t *ctx_string_slice(uint8_t *value, int64_t start, int64_t end) {
    StringView view = llcontext_string_view_clamped(value, start, end);
    uint8_t *out = (uint8_t *)malloc((size_t)view.len + 1u);
    if (out == NULL) {
        abort();
    }
    if (view.len > 0) {
        memcpy(out, view.data, (size_t)view.len);
    }
    out[view.len] = 0;
    return out;
}

CTX_RUNTIME_WEAK StringView ctx_string_view(uint8_t *value, int64_t start, int64_t end) {
    return llcontext_string_view_clamped(value, start, end);
}

CTX_RUNTIME_WEAK int64_t ctx_string_view_len(StringView view) {
    return view.len;
}

CTX_RUNTIME_WEAK StringView ctx_string_view_slice(StringView view, int64_t start, int64_t end) {
    int64_t len = view.len < 0 ? 0 : view.len;
    if (view.data == NULL) {
        len = 0;
    }
    int64_t lo = llcontext_clamp_i64(start, 0, len);
    int64_t hi = llcontext_clamp_i64(end, lo, len);
    StringView out;
    out.data = (uint8_t *)llcontext_offset_ptr(view.data, (size_t)lo);
    out.len = hi - lo;
    return out;
}

CTX_RUNTIME_WEAK StringView ctx_string_view_prefix(StringView view, int64_t end) {
    return ctx_string_view_slice(view, 0, end);
}

CTX_RUNTIME_WEAK StringView ctx_string_view_suffix(StringView view, int64_t start) {
    return ctx_string_view_slice(view, start, view.len);
}

CTX_RUNTIME_WEAK int64_t ctx_string_view_index(StringView view, int64_t index) {
    if (index < 0 || index >= view.len) {
        return 0;
    }
    if (view.data == NULL) {
        return 0;
    }
    return (int64_t)view.data[index];
}

CTX_RUNTIME_WEAK int64_t ctx_string_view_eq(StringView view, uint8_t *other) {
    uint8_t *rhs = llcontext_nonnull_string(other);
    int64_t rhs_len = llcontext_strlen_i64(rhs);
    if (view.len != rhs_len) {
        return 0;
    }
    return llcontext_bytes_equal(view.data, rhs, view.len);
}

CTX_RUNTIME_WEAK int64_t ctx_string_views_eq(StringView lhs, StringView rhs) {
    if (lhs.len != rhs.len) {
        return 0;
    }
    return llcontext_bytes_equal(lhs.data, rhs.data, lhs.len);
}

CTX_RUNTIME_WEAK int64_t ctx_string_slice_eq(uint8_t *value, int64_t start, int64_t end, uint8_t *other) {
    StringView lhs = ctx_string_view(value, start, end);
    return ctx_string_view_eq(lhs, other);
}

CTX_RUNTIME_WEAK int64_t ctx_string_slices_eq(uint8_t *lhs, int64_t lhs_start, int64_t lhs_end, uint8_t *rhs, int64_t rhs_start, int64_t rhs_end) {
    StringView left = ctx_string_view(lhs, lhs_start, lhs_end);
    StringView right = ctx_string_view(rhs, rhs_start, rhs_end);
    return ctx_string_views_eq(left, right);
}

CTX_RUNTIME_WEAK uint8_t *ctx_string_from_view(StringView view) {
    if (view.len < 0) {
        view.len = 0;
    }
    if (view.data == NULL) {
        view.len = 0;
    }
    uint8_t *out = (uint8_t *)malloc((size_t)view.len + 1u);
    if (out == NULL) {
        abort();
    }
    if (view.len > 0 && view.data != NULL) {
        memcpy(out, view.data, (size_t)view.len);
    }
    out[view.len] = 0;
    return out;
}

CTX_RUNTIME_WEAK DynArrayView arena_da_view(void *data, int64_t len, int64_t elem_size) {
    DynArrayView view;
    view.data = data;
    view.len = len < 0 ? 0 : len;
    view.elem_size = elem_size < 0 ? 0 : elem_size;
    if (view.data == NULL) {
        view.len = 0;
    }
    return view;
}

CTX_RUNTIME_WEAK DynArrayView arena_da_view_slice(DynArrayView view, int64_t start, int64_t end) {
    int64_t len = view.len < 0 ? 0 : view.len;
    int64_t elem_size = view.elem_size < 0 ? 0 : view.elem_size;
    if (view.data == NULL) {
        len = 0;
    }
    int64_t lo = llcontext_clamp_i64(start, 0, len);
    int64_t hi = llcontext_clamp_i64(end, lo, len);
    DynArrayView out;
    out.data = llcontext_offset_ptr(view.data, (size_t)lo * (size_t)elem_size);
    out.len = hi - lo;
    out.elem_size = elem_size;
    return out;
}
