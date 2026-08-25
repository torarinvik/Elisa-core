# stage0: `usize` and pointer width are not target-relative on wasm32

Compile any program that touches the arena runtime for `wasm32-unknown-wasi`
and inspect the import signatures:

    elisac -emit obj -O2 -target-triple wasm32-unknown-wasi -o m.o prog.elisa
    wasm-ld --no-entry --export-dynamic --allow-undefined -o m.wasm m.o

The module then imports:

    mmap(i64, i64, i64, i64, i64, i64) -> i64      <-- pointer returned as i64
    malloc(i64) -> i32                             <-- size i64, pointer i32
    memcpy(i32, i32, i64) -> i32                   <-- pointers i32, size i64

Three problems, all the same root cause: the target's pointer/size width is not
consulted when lowering `usize` and pointer-typed externs.

1. **`usize` is hardcoded to 64 bits.** On wasm32 it must be 32. Every size
   argument crosses as i64.
2. **`mmap`'s return is i64 while `memcpy`'s pointer parameters are i32.** The
   same C pointer type lowers two different ways depending on the extern, so a
   host implementation cannot satisfy both with one convention.
3. Consequence for a host: `WebAssembly` rejects a JS function returning a
   Number where the module declared i64, with
   `TypeError: Cannot convert <n> to a BigInt`, at the first arena allocation.

## Impact

The wasm32 backend otherwise WORKS — plain code compiles, links and runs
correctly (see nw-core/test/wasm_diff.mjs: 421 cases identical to the Python
reference). This is the one thing standing between that and a clean wasm ABI.
The host shim in nw-core/shells/web/elisa-wasm-runtime.js currently works
around it by returning BigInt from `mmap` and Number elsewhere, matching the
inconsistency rather than fixing it.

## Suggested fix

Derive pointer and `usize` width from the target triple's data layout
(LLVM already knows it: `LLVMPointerSize` / the module data layout string)
rather than assuming 64-bit, and lower every pointer-typed extern through that
one width. A `-target-triple wasm32-*` build should then import
`mmap(i32,i32,i32,i32,i32,i32) -> i32` and `malloc(i32) -> i32`.

Worth adding a c-bind-check style assertion so a target whose pointer width
disagrees with the emitted externs fails the build rather than producing a
module no host can satisfy.
