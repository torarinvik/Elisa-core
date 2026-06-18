#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "reduce_sum_u64_kernel.h"

#ifndef ARRAY_LEN
#define ARRAY_LEN(xs) (sizeof(xs) / sizeof((xs)[0]))
#endif

typedef uint64_t (*reduce_kernel_fn)(uint64_t *data, uintptr_t len, uintptr_t rounds, uint64_t bias);

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

static uint64_t c_reduce_sum_u64(uint64_t *data, uintptr_t len, uintptr_t rounds, uint64_t bias) {
    uint64_t acc = bias;
    for (uintptr_t round = 0; round < rounds; ++round) {
        uint64_t partial = (uint64_t)round;
        for (uintptr_t i = 0; i < len; ++i) {
            partial += data[i];
        }
        acc += partial;
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
                              reduce_kernel_fn fn,
                              uint64_t *data,
                              size_t len,
                              uintptr_t rounds,
                              uint64_t bias,
                              int samples) {
    bench_result out = {
        .name = name,
        .best_ns = UINT64_MAX,
        .gib_per_s = 0.0,
        .checksum = 0,
    };

    for (int sample = 0; sample < samples; ++sample) {
        uint64_t started = now_ns();
        uint64_t checksum = fn(data, (uintptr_t)len, rounds, bias);
        uint64_t elapsed = now_ns() - started;
        if (elapsed < out.best_ns) {
            out.best_ns = elapsed;
            out.checksum = checksum;
        }
        bench_sink ^= checksum;
    }

    double bytes_read = (double)len * (double)rounds * (double)sizeof(uint64_t);
    out.gib_per_s = bytes_read / ((double)out.best_ns / 1000000000.0) / (1024.0 * 1024.0 * 1024.0);
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
    const size_t len = 1 << 24;
    const uintptr_t rounds = 24;
    const int samples = 6;
    const uint64_t seed = UINT64_C(0x0123456789abcdef);
    const uint64_t bias = UINT64_C(0x9e3779b97f4a7c15);

    uint64_t *data = (uint64_t *)malloc(len * sizeof(*data));
    if (data == NULL) {
        fprintf(stderr, "allocation failed\n");
        return 1;
    }
    fill_input(data, len, seed);

    uint64_t elisa_core_check = elisa_core_reduce_sum_u64(data, (uintptr_t)len, rounds, bias);
    uint64_t c_check = c_reduce_sum_u64(data, (uintptr_t)len, rounds, bias);
    if (elisa_core_check != c_check) {
        fprintf(stderr, "elisacore and C kernels produced different results\n");
        free(data);
        return 2;
    }

    bench_result results[] = {
        run_bench("elisacore", elisa_core_reduce_sum_u64, data, len, rounds, bias, samples),
        run_bench("native C", c_reduce_sum_u64, data, len, rounds, bias, samples),
    };

    printf("reduce_sum_u64 benchmark\n");
    printf("elements=%zu rounds=%" PRIuPTR " samples=%d\n", len, rounds, samples);
    for (size_t i = 0; i < ARRAY_LEN(results); ++i) {
        print_result(&results[i]);
    }
    printf("\nelisacore / C throughput ratio: %.3fx\n", results[0].gib_per_s / results[1].gib_per_s);
    printf("bench sink=%" PRIu64 "\n", bench_sink);

    free(data);
    return 0;
}
