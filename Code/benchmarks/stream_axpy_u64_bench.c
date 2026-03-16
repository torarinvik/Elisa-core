#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "stream_axpy_u64_kernel.h"

#ifndef ARRAY_LEN
#define ARRAY_LEN(xs) (sizeof(xs) / sizeof((xs)[0]))
#endif

typedef uint64_t (*stream_kernel_fn)(uint64_t *dst, uint64_t *src, uintptr_t len, uintptr_t rounds, uint64_t scale);

typedef struct {
    const char *name;
    uint64_t best_ns;
    double gib_per_s;
    uint64_t checksum;
} bench_result;

static volatile uint64_t bench_sink = 0;

static uint64_t now_ns(void) {
#ifdef __APPLE__
    return clock_gettime_nsec_np(CLOCK_UPTIME_RAW);
#else
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (uint64_t)ts.tv_sec * 1000000000ull + (uint64_t)ts.tv_nsec;
#endif
}

static uint64_t c_stream_axpy_u64(uint64_t *dst, uint64_t *src, uintptr_t len, uintptr_t rounds, uint64_t scale) {
    uint64_t acc = scale ^ UINT64_C(0x9e3779b97f4a7c15);

    for (uintptr_t round = 0; round < rounds; ++round) {
        for (uintptr_t i = 0; i < len; ++i) {
            uint64_t value = dst[i] + src[i] * scale + acc;
            dst[i] = value;
            acc ^= value + (uint64_t)i + (uint64_t)round;
        }
    }

    return acc;
}

static void fill_input(uint64_t *data, size_t len, uint64_t seed) {
    uint64_t state = seed;
    for (size_t i = 0; i < len; ++i) {
        state ^= state >> 12;
        state ^= state << 25;
        state ^= state >> 27;
        data[i] = state * UINT64_C(2685821657736338717);
    }
}

static bench_result run_bench(const char *name,
                              stream_kernel_fn fn,
                              const uint64_t *base_dst,
                              const uint64_t *base_src,
                              uint64_t *work_dst,
                              uint64_t *work_src,
                              size_t len,
                              uintptr_t rounds,
                              uint64_t scale,
                              int samples) {
    bench_result out = {
        .name = name,
        .best_ns = UINT64_MAX,
        .gib_per_s = 0.0,
        .checksum = 0,
    };

    for (int sample = 0; sample < samples; ++sample) {
        memcpy(work_dst, base_dst, len * sizeof(*work_dst));
        memcpy(work_src, base_src, len * sizeof(*work_src));
        uint64_t started = now_ns();
        uint64_t checksum = fn(work_dst, work_src, (uintptr_t)len, rounds, scale);
        uint64_t elapsed = now_ns() - started;
        if (elapsed < out.best_ns) {
            out.best_ns = elapsed;
            out.checksum = checksum ^ work_dst[len / 2];
        }
        bench_sink ^= checksum ^ work_dst[(sample * 257u) % len];
    }

    double bytes_moved = (double)len * (double)rounds * (double)(sizeof(uint64_t) * 3u);
    out.gib_per_s = bytes_moved / ((double)out.best_ns / 1000000000.0) / (1024.0 * 1024.0 * 1024.0);
    return out;
}

static void print_result(const bench_result *result) {
    printf("%-20s best=%8.3f ms  GiB/s=%8.3f  checksum=%" PRIu64 "\n",
           result->name,
           (double)result->best_ns / 1000000.0,
           result->gib_per_s,
           result->checksum);
}

int main(void) {
    const size_t len = 1u << 22;
    const uintptr_t rounds = 16;
    const int samples = 5;
    const uint64_t seed_dst = UINT64_C(0x13579bdf2468ace0);
    const uint64_t seed_src = UINT64_C(0xfedcba9876543210);
    const uint64_t scale = UINT64_C(6364136223846793005);

    uint64_t *base_dst = (uint64_t *)malloc(len * sizeof(*base_dst));
    uint64_t *base_src = (uint64_t *)malloc(len * sizeof(*base_src));
    uint64_t *work_llctx_dst = (uint64_t *)malloc(len * sizeof(*work_llctx_dst));
    uint64_t *work_llctx_src = (uint64_t *)malloc(len * sizeof(*work_llctx_src));
    uint64_t *work_c_dst = (uint64_t *)malloc(len * sizeof(*work_c_dst));
    uint64_t *work_c_src = (uint64_t *)malloc(len * sizeof(*work_c_src));
    if (base_dst == NULL || base_src == NULL || work_llctx_dst == NULL || work_llctx_src == NULL || work_c_dst == NULL || work_c_src == NULL) {
        fprintf(stderr, "allocation failed\n");
        free(base_dst); free(base_src); free(work_llctx_dst); free(work_llctx_src); free(work_c_dst); free(work_c_src);
        return 1;
    }

    fill_input(base_dst, len, seed_dst);
    fill_input(base_src, len, seed_src);

    memcpy(work_llctx_dst, base_dst, len * sizeof(*work_llctx_dst));
    memcpy(work_llctx_src, base_src, len * sizeof(*work_llctx_src));
    memcpy(work_c_dst, base_dst, len * sizeof(*work_c_dst));
    memcpy(work_c_src, base_src, len * sizeof(*work_c_src));

    uint64_t llctx_check = llctx_stream_axpy_u64(work_llctx_dst, work_llctx_src, (uintptr_t)len, rounds, scale);
    uint64_t c_check = c_stream_axpy_u64(work_c_dst, work_c_src, (uintptr_t)len, rounds, scale);
    if (llctx_check != c_check || memcmp(work_llctx_dst, work_c_dst, len * sizeof(*work_c_dst)) != 0) {
        fprintf(stderr, "llcontext and C kernels produced different results\n");
        free(base_dst); free(base_src); free(work_llctx_dst); free(work_llctx_src); free(work_c_dst); free(work_c_src);
        return 2;
    }

    bench_result results[] = {
        run_bench("llcontext", llctx_stream_axpy_u64, base_dst, base_src, work_llctx_dst, work_llctx_src, len, rounds, scale, samples),
        run_bench("native C", c_stream_axpy_u64, base_dst, base_src, work_c_dst, work_c_src, len, rounds, scale, samples),
    };

    printf("stream_axpy_u64 benchmark\n");
    printf("elements=%zu rounds=%" PRIuPTR " samples=%d\n", len, rounds, samples);
    for (size_t i = 0; i < ARRAY_LEN(results); ++i) {
        print_result(&results[i]);
    }
    printf("\nllcontext / C throughput ratio: %.3fx\n", results[0].gib_per_s / results[1].gib_per_s);
    printf("bench sink=%" PRIu64 "\n", bench_sink);

    free(base_dst); free(base_src); free(work_llctx_dst); free(work_llctx_src); free(work_c_dst); free(work_c_src);
    return 0;
}
