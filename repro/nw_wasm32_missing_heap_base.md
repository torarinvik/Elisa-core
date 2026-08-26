# The wasm display-list string pool was corrupted by a missing __heap_base

## Status: ROOT-CAUSED AND FIXED. Two one-line changes, both in the WEB SHELL,
## none in the compiler. Verified against the real display-list object file.

## Symptom

A display-list frame built under wasm32 rendered correct GEOMETRY but garbled
TEXT. The string pool came back as e.g.

    F???C??4C??? =Tri?FC???C???@        (want: Dual N-BackN =Trialframe-end)

with recognizable FRAGMENTS of the real strings embedded in float garbage.
Native builds of the same source were correct on every frame.

## Root cause

The module never exported `__heap_base`, and the host shim silently guessed:

    const base = instance.exports.__heap_base;
    heapNext = typeof base === "number" ? base : 65536;      // <-- the bug

Two things are wrong with that line, and it needs BOTH fixed:

1. `wasm-ld` does not export `__heap_base` under `--export-dynamic`; it is a
   linker-synthesized symbol and needs `--export=__heap_base` explicitly. So the
   lookup always returned undefined.

2. Even once exported, `__heap_base` comes back as a `WebAssembly.Global`, NOT a
   number -- so `typeof base === "number"` is false and the fallback fires
   anyway. Adding the link flag alone does NOT fix this; it was verified to
   still corrupt.

The guess of 65536 put the arena's first region directly on top of the module's
static data. In the failing build `__heap_base` was 134720 while the shim started
allocating at 65536, so the arena overwrote ~69 KB of static data -- including
the string literals the display list pools.

That is why the corruption looked like nonsense: `"Dual N-Back"` at address 65609
became `"Dua\0..."`, so the pool-append loop (`while bytes[index] != 0`) stopped
after FOUR bytes. Counts came out 0/3/4/0/1 instead of 11/3/5/0/9 -- not lost
pushes, just literals truncated by a NUL that the allocator had written over them.

## The fix

  * link with `--export=__heap_base` (scripts/build_wasm.sh);
  * read it as a `WebAssembly.Global` and REFUSE to guess when it is absent --
    a wrong heap base silently overwrites static data, and the failure then
    presents as corrupt program logic rather than as a bad heap base.

## Verification

Relinking the UNCHANGED display-list object file with the export, and running it
against the corrected shim, produced the correct pool on all five frames:

    pool=Dual N-BackN =Trialframe-end   (x5)

The reduction in nw_wasm32_missing_heap_base.elisa also goes from
`pool.count by step: 0,0,3,7,7,8` to the correct `0,11,14,19,19,28`.

## What this was NOT

Three earlier diagnoses of this symptom were wrong, and each cost real time:

  * "darray returns stale storage on the second call" -- no; that was the
    128 MiB-per-region heap leak (fixed separately).
  * "interleaved darray growth loses elements past relocation" -- no; there is
    no relocation bug at all. WITHDRAWN, see that file.
  * "aliasing between the command array and the pool array" -- no; mixed
    element sizes, struct-held arrays, and captured-loop mutation were each
    probed in isolation and are all correct.

The thing that actually found it was dumping the string literal's BYTES and
ADDRESS after each allocation, instead of reasoning about the data structures.
The literal was at 65609 and the heap started at 65536; nothing about the darray
implementation was ever involved.
