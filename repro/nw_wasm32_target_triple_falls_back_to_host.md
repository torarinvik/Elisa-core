# wasm32 builds believed they were macOS/arm64

## Status: FIXED (compiler + runtime). Verified by probe; full suites not yet re-run.

## The defect

`targetOSFromTriple` / `targetArchFromTriple` in `compiler/src/semantic/target_consts.go`
fell back to `runtime.GOOS` / `runtime.GOARCH` whenever they could not recognize a
triple -- including when a triple was EXPLICITLY given. So:

    elisac -target-triple wasm32-unknown-wasi ...

produced a module compiled with

    ELISA_TARGET_OS_MACOS    TRUE
    ELISA_TARGET_OS_POSIX    TRUE
    ELISA_TARGET_ARCH_ARM64  TRUE

on an arm64 Mac. Confirmed by compiling a `static if` probe to wasm and reading the
answers back out of the instance -- not inferred from the source.

Consequences:
  * every `static if ELISA_TARGET_ARCH_ARM64` branch in the runtime was selected for
    a target that is not arm64;
  * `ARENA_BACKEND_WASM_HEAPBASE` was unreachable dead code -- no triple could ever
    select it, because the OS always resolved to the host's;
  * cross-compiling to any unrecognized triple silently inherited the build machine's
    arch. `riscv64-unknown-linux-gnu` got the host arch with no diagnostic.

This is the same defect class as the `wordBits` bug fixed in ff42deed, where struct
layout was seeded from the HOST's pointer size. Same lesson: **the host is a default
only when nothing was specified.** When a triple is given, its components are the
truth, and a component we cannot parse is UNKNOWN -- never "same as this machine".

## The fix

`target_consts.go`:
  * recognize `wasm32`/`wasm64` arches and `wasi`/`emscripten` OSes;
  * a non-empty triple no longer falls back to the host: an unparsed arch is taken
    from the triple's first component (standard `<arch>-<vendor>-<os>-<abi>` layout),
    and an unparsed OS is "" rather than the host's;
  * new predicates `ELISA_TARGET_OS_WASI`, `ELISA_TARGET_ARCH_WASM32`,
    `ELISA_TARGET_ARCH_WASM64`, `ELISA_TARGET_ARCH_WASM`.

WASI deliberately keeps `ELISA_TARGET_OS_POSIX` TRUE: the only POSIX surface the
runtime uses is mmap/munmap/malloc/free/mem*/write, and both wasi-libc and the
browser host shim supply exactly that subset. That keeps wasm on the mmap arena
backend, which can actually RECLAIM (WASM_HEAPBASE's `free_region` is `pass` -- it
never frees, so switching to it would have made the leak below permanent).

## The second defect this exposed: the reservation is not free on wasm

`ARENA_DEFAULT_RESERVE_COMMIT_SLOTS` was a flat 33554432 slots, documented as
"256 MiB of contiguous virtual ADDRESS SPACE ... costs address space, not physical
memory". That is true on POSIX (demand paging) and Windows (MEM_RESERVE).

It is false on WebAssembly. `memory.grow` commits real pages immediately and linear
memory is capped at 4 GiB, so the "reservation" is a real allocation -- per region.
Traced against the browser shim, ONE `darray` cost 128 MiB:

    mmap(134217760) -> 65536          # 128 MiB for one darray's arena
    grow(2049) -> 2066                # buffer now 269 MB, for two 20-byte arrays

The reservation is now sized to the target's paging model
(`static if ELISA_TARGET_ARCH_WASM`), not to a fixed constant.

## Host-shim defects found by the same trace (nw-core/shells/web/elisa-wasm-runtime.js)

1. `grow()` ignored `memory.grow`'s return value. It returns -1 rather than throwing,
   and the shim advanced `heapEnd` anyway -- handing out addresses PAST the end of
   linear memory. The failure then surfaced as "memory access out of bounds" deep
   inside `arena_alloc`, pointing at the wrong subsystem entirely. It now throws
   where the cause is still legible.

2. `release()` could only rewind the single most recent block, and `alloc()` aligns
   `heapNext` UP before recording a block's start -- so the fallback test
   `start + length === heapNext` can never match once any padding has been inserted.
   Regions NEST (a frame opens a region, opens another inside it, frees innermost
   first), so the outer free was silently dropped every time. A frame leaked its
   whole outer region per tick. Replaced with a block stack that collects freed
   blocks when the block below them goes, keeping LIFO workloads exactly flat.

## Correcting the earlier diagnosis

`nw_wasm32_interleaved_darray_growth.elisa` claimed two darrays growing interleaved
"lose elements past the first relocation". That was WRONG, and the reasoning was
wrong in an instructive way: the test reused ONE instance across 20 calls, so it was
really measuring accumulated heap exhaustion. With a fresh instance per call the
interleaved case was already correct, and after the fixes above it is correct on a
shared instance too -- every call now maps at the same two addresses with no growth
at all. There was never a relocation bug.

The lesson is the one the wordBits repro already taught and this one had to learn
again: a symptom that varies with CALL INDEX is about accumulated state, not about
the operation in the call.

## Still open

The display-list string POOL is still garbled under wasm while geometry is correct,
on every frame including the first (native is correct on all frames -- verified with
the same code compiled both ways). That is a DIFFERENT bug from anything above and
is not yet diagnosed. The next discriminator to run is
`repro/nw_wasm32_mixed_elem_darray.elisa`: a 40-byte-element array and a byte array
growing interleaved, which is the shape a display list actually has and which the
equal-element-size probe did not cover.
