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
