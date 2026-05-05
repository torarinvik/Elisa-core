#define ARENA_IMPLEMENTATION
#include "../../compiler/runtime/arena_reference.h"

#include <stdint.h>
#include <stdio.h>
#include <string.h>
#include <time.h>

#ifndef ARRAY_LEN
#define ARRAY_LEN(xs) (sizeof(xs) / sizeof((xs)[0]))
#endif

typedef struct {
    int *items;
    size_t count;
    size_t capacity;
} bench_i32_array;

typedef struct {
    const char *name;
    uint64_t best_ns;
    double ns_per_op;
    size_t checksum;
} bench_result;

static volatile size_t bench_sink = 0;

static uint64_t now_ns(void) {
#ifdef __APPLE__
    return clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
#endif
}

static void elisa_core_da_append_i32(Arena *a, bench_i32_array *da, int item) {
    if (da->count >= da->capacity) {
        size_t new_capacity = da->capacity == 0 ? ARENA_DA_INIT_CAP : da->capacity * 2;
        size_t old_size = da->capacity * sizeof(*da->items);
        size_t new_size = new_capacity * sizeof(*da->items);

        if (da->items == NULL) {
            da->items = (int *)arena_alloc(a, new_size);
        } else {
            da->items = (int *)arena_realloc(a, da->items, old_size, new_size);
        }

        da->capacity = new_capacity;
    }

    da->items[da->count] = item;
    da->count += 1;
}

static void elisa_core_da_append_many_i32(Arena *a, bench_i32_array *da, const int *items, size_t item_count) {
    size_t needed = da->count + item_count;

    if (needed > da->capacity) {
        size_t new_capacity = da->capacity;
        if (new_capacity == 0) {
            new_capacity = ARENA_DA_INIT_CAP;
        }
        while (needed > new_capacity) {
            new_capacity *= 2;
        }

        size_t old_size = da->capacity * sizeof(*da->items);
        size_t new_size = new_capacity * sizeof(*da->items);

        if (da->items == NULL) {
            da->items = (int *)arena_alloc(a, new_size);
        } else {
            da->items = (int *)arena_realloc(a, da->items, old_size, new_size);
        }

        da->capacity = new_capacity;
    }

    arena_memcpy(da->items + da->count, (void *)items, item_count * sizeof(*da->items));
    da->count += item_count;
}

static bench_result run_append_macro(size_t n, int rounds) {
    bench_result out = {.name = "baseline macro append", .best_ns = UINT64_MAX, .ns_per_op = 0.0, .checksum = 0};

    for (int round = 0; round < rounds; ++round) {
        Arena arena = {0};
        bench_i32_array da = {0};
        uint64_t started = now_ns();
        for (size_t i = 0; i < n; ++i) {
            arena_da_append(&arena, &da, (int)i);
        }
        uint64_t elapsed = now_ns() - started;
        if (elapsed < out.best_ns) {
            out.best_ns = elapsed;
        }
        out.checksum += da.count + (da.count ? (size_t)da.items[da.count / 2] : 0);
        bench_sink += out.checksum;
        arena_free(&arena);
    }

    out.ns_per_op = (double)out.best_ns / (double)n;
    return out;
}

static bench_result run_append_elisa_core(size_t n, int rounds) {
    bench_result out = {.name = "elisacore-lowered append", .best_ns = UINT64_MAX, .ns_per_op = 0.0, .checksum = 0};

    for (int round = 0; round < rounds; ++round) {
        Arena arena = {0};
        bench_i32_array da = {0};
        uint64_t started = now_ns();
        for (size_t i = 0; i < n; ++i) {
            elisa_core_da_append_i32(&arena, &da, (int)i);
        }
        uint64_t elapsed = now_ns() - started;
        if (elapsed < out.best_ns) {
            out.best_ns = elapsed;
        }
        out.checksum += da.count + (da.count ? (size_t)da.items[da.count / 2] : 0);
        bench_sink += out.checksum;
        arena_free(&arena);
    }

    out.ns_per_op = (double)out.best_ns / (double)n;
    return out;
}

static bench_result run_append_many_macro(size_t n, size_t chunk_size, int rounds) {
    bench_result out = {.name = "baseline macro append_many", .best_ns = UINT64_MAX, .ns_per_op = 0.0, .checksum = 0};

    for (int round = 0; round < rounds; ++round) {
        Arena arena = {0};
        bench_i32_array da = {0};
        int chunk[256] = {0};
        uint64_t started = now_ns();
        for (size_t base = 0; base < n; base += chunk_size) {
            size_t take = n - base;
            if (take > chunk_size) {
                take = chunk_size;
            }
            for (size_t i = 0; i < take; ++i) {
                chunk[i] = (int)(base + i);
            }
            arena_da_append_many(&arena, &da, chunk, take);
        }
        uint64_t elapsed = now_ns() - started;
        if (elapsed < out.best_ns) {
            out.best_ns = elapsed;
        }
        out.checksum += da.count + (da.count ? (size_t)da.items[da.count / 3] : 0);
        bench_sink += out.checksum;
        arena_free(&arena);
    }

    out.ns_per_op = (double)out.best_ns / (double)n;
    return out;
}

static bench_result run_append_many_elisa_core(size_t n, size_t chunk_size, int rounds) {
    bench_result out = {.name = "elisacore-lowered append_many", .best_ns = UINT64_MAX, .ns_per_op = 0.0, .checksum = 0};

    for (int round = 0; round < rounds; ++round) {
        Arena arena = {0};
        bench_i32_array da = {0};
        int chunk[256] = {0};
        uint64_t started = now_ns();
        for (size_t base = 0; base < n; base += chunk_size) {
            size_t take = n - base;
            if (take > chunk_size) {
                take = chunk_size;
            }
            for (size_t i = 0; i < take; ++i) {
                chunk[i] = (int)(base + i);
            }
            elisa_core_da_append_many_i32(&arena, &da, chunk, take);
        }
        uint64_t elapsed = now_ns() - started;
        if (elapsed < out.best_ns) {
            out.best_ns = elapsed;
        }
        out.checksum += da.count + (da.count ? (size_t)da.items[da.count / 3] : 0);
        bench_sink += out.checksum;
        arena_free(&arena);
    }

    out.ns_per_op = (double)out.best_ns / (double)n;
    return out;
}

static void print_result(const bench_result *result) {
    printf("%-28s best=%8.3f ms  ns/op=%8.3f  checksum=%zu\n",
           result->name,
           (double)result->best_ns / 1000000.0,
           result->ns_per_op,
           result->checksum);
}

int main(void) {
    const size_t n = 1000000;
    const size_t chunk = 64;
    const int rounds = 8;

    bench_result results[] = {
        run_append_macro(n, rounds),
        run_append_elisa_core(n, rounds),
        run_append_many_macro(n, chunk, rounds),
        run_append_many_elisa_core(n, chunk, rounds),
    };

    printf("manual interim arena benchmark\n");
    printf("items=%zu rounds=%d chunk=%zu\n", n, rounds, chunk);
    for (size_t i = 0; i < ARRAY_LEN(results); ++i) {
        print_result(&results[i]);
    }

    printf("\nappend speed ratio (macro / elisacore-lowered): %.3fx\n",
           results[0].ns_per_op / results[1].ns_per_op);
    printf("append_many speed ratio (macro / elisacore-lowered): %.3fx\n",
           results[2].ns_per_op / results[3].ns_per_op);
    printf("bench sink=%zu\n", (size_t)bench_sink);
    return 0;
}
