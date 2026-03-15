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
    if (list == NULL) {
        list = ctx_stage0_list_alloc(4, resolved_elem_size);
        if (list == NULL) {
            return NULL;
        }
    }
    if (list->elem_size <= 0 && elem_size > 0) {
        list->elem_size = elem_size;
        resolved_elem_size = elem_size;
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
    out->inline_boxes = NULL;
    out->inline_box_stride = 0;
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
