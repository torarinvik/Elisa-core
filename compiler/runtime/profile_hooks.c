#include <stddef.h>
#include <stdint.h>

#if defined(__GNUC__) || defined(__clang__)
#define ELISA_WEAK __attribute__((weak))
#else
#define ELISA_WEAK
#endif

/* Optional profiler ABI fallback for standalone stage0 archives and programs. */
ELISA_WEAK uint32_t elisa_profile_allocation_negotiate(uint32_t version) {
    (void)version;
    return 0;
}

ELISA_WEAK uint32_t elisa_profile_region_layout_negotiate(uint32_t version) {
    (void)version;
    return 0;
}

ELISA_WEAK void elisa_profile_region_layout_v1(
    uintptr_t arena,
    size_t region,
    uintptr_t header,
    uintptr_t data,
    size_t capacity
) {
    (void)arena;
    (void)region;
    (void)header;
    (void)data;
    (void)capacity;
}

ELISA_WEAK void elisa_profile_allocation_event_v1(
    uint32_t kind,
    uintptr_t address,
    size_t size,
    uintptr_t old_address,
    size_t old_size,
    uintptr_t arena,
    size_t region
) {
    (void)kind;
    (void)address;
    (void)size;
    (void)old_address;
    (void)old_size;
    (void)arena;
    (void)region;
}
