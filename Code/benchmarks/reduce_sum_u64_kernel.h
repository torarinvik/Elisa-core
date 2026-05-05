#ifndef REDUCE_SUM_U64_KERNEL_H
#define REDUCE_SUM_U64_KERNEL_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

uint64_t elisa_core_reduce_sum_u64(uint64_t *arg0, uintptr_t arg1, uintptr_t arg2, uint64_t arg3);

#ifdef __cplusplus
}
#endif

#endif
