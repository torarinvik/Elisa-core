#include <assert.h>
#include <inttypes.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#include "mix_u64_kernel.h"

#ifndef ARRAY_LEN
#define ARRAY_LEN(xs) (sizeof(xs) / sizeof((xs)[0]))
#endif

typedef uint64_t (*mix_kernel_fn)(uint64_t *data, uintptr_t len, uintptr_t rounds, uint64_t seed);

typedef struct {
    const char *name;
    uint64_t best_ns;
    double ns_per_elem_round;
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

static uint64_t c_mix_u64(uint64_t *data, uintptr_t len, uintptr_t rounds, uint64_t seed) {
    uint64_t acc = seed;

    for (uintptr_t round = 0; round < rounds; ++round) {
        for (uintptr_t i = 0; i < len; ++i) {
            uint64_t value = data[i];
            value ^= (value >> 33);
            value *= UINT64_C(6364136223846793005);
            value ^= (value >> 29);
            value += acc + (uint64_t)i + (uint64_t)round + UINT64_C(1442695040888963407);
            data[i] = value;

            acc ^= value;
            acc *= UINT64_C(1442695040888963407);
            acc += UINT64_C(1);
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
                              mix_kernel_fn fn,
                              const uint64_t *base,
                              uint64_t *work,
                              size_t len,
                              uintptr_t inner_rounds,
                              uint64_t seed,
                              int samples) {
    bench_result out = {
        .name = name,
        .best_ns = UINT64_MAX,
        .ns_per_elem_round = 0.0,
        .checksum = 0,
    };

    for (int sample = 0; sample < samples; ++sample) {
        memcpy(work, base, len * sizeof(*work));
        uint64_t started = now_ns();
        uint64_t checksum = fn(work, (uintptr_t)len, inner_rounds, seed);
        uint64_t elapsed = now_ns() - started;
        if (elapsed < out.best_ns) {
            out.best_ns = elapsed;
            out.checksum = checksum ^ work[len / 2];
        }
        bench_sink ^= checksum ^ work[(sample * 131u) % len];
    }

    out.ns_per_elem_round = (double)out.best_ns / (double)(len * (size_t)inner_rounds);
    return out;
}

static void print_result(const bench_result *result) {
    printf("%-20s best=%8.3f ms  ns/(elem*round)=%8.3f  checksum=%" PRIu64 "\n",
           result->name,
           (double)result->best_ns / 1000000.0,
           result->ns_per_elem_round,
           result->checksum);
}

int main(void) {
    const size_t len = 1u << 20;
    const uintptr_t inner_rounds = 12;
    const int samples = 7;
    const uint64_t seed = UINT64_C(0x123456789abcdef0);

    uint64_t *base = (uint64_t *)malloc(len * sizeof(*base));
    uint64_t *work_elisa_core = (uint64_t *)malloc(len * sizeof(*work_elisa_core));
    uint64_t *work_c = (uint64_t *)malloc(len * sizeof(*work_c));
    if (base == NULL || work_elisa_core == NULL || work_c == NULL) {
        fprintf(stderr, "allocation failed\n");
        free(base);
        free(work_elisa_core);
        free(work_c);
        return 1;
    }

    fill_input(base, len, seed);

    memcpy(work_elisa_core, base, len * sizeof(*work_elisa_core));
    memcpy(work_c, base, len * sizeof(*work_c));
    uint64_t elisa_core_check = elisa_core_mix_u64(work_elisa_core, (uintptr_t)len, inner_rounds, seed);
    uint64_t c_check = c_mix_u64(work_c, (uintptr_t)len, inner_rounds, seed);
    if (elisa_core_check != c_check || memcmp(work_elisa_core, work_c, len * sizeof(*work_c)) != 0) {
        fprintf(stderr, "elisacore and C kernels produced different results\n");
        free(base);
        free(work_elisa_core);
        free(work_c);
        return 2;
    }

    bench_result results[] = {
        run_bench("elisacore", elisa_core_mix_u64, base, work_elisa_core, len, inner_rounds, seed, samples),
        run_bench("native C", c_mix_u64, base, work_c, len, inner_rounds, seed, samples),
    };

    printf("mix_u64 benchmark\n");
    printf("elements=%zu inner_rounds=%" PRIuPTR " samples=%d\n", len, inner_rounds, samples);
    for (size_t i = 0; i < ARRAY_LEN(results); ++i) {
        print_result(&results[i]);
    }
    printf("\nelisacore / C ratio: %.3fx\n", results[0].ns_per_elem_round / results[1].ns_per_elem_round);
    printf("bench sink=%" PRIu64 "\n", bench_sink);

    free(base);
    free(work_elisa_core);
    free(work_c);
    return 0;
}
