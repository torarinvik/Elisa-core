# binary-trees benchmark (N=18)

The classic [binary-trees](https://benchmarksgame-team.pages.debian.net/benchmarksgame/description/binarytrees.html)
allocation stress test: build many trees, checksum each, build+free in bulk. All implementations
produce the identical checksum **68332206**, so they do the same work.

Measured with `/usr/bin/time -l` on the same machine; Elisa built `-emit test -O3`, C++ `clang++ -O3`,
Rust `rustc -O`. Single-threaded.

| Implementation | model | time (real) | peak RSS |
|---|---|---:|---:|
| **Elisa — struct, inferred region, `new[auto]`** | region bump-alloc, bulk free | **0.40 s** | 22 MB |
| Rust — index arena (`Vec<Node>`) | bump indices, drop per tree | 0.44 s | 22 MB |
| C++ — bump arena | bump, free per tree | 0.45 s | 26 MB |
| Rust — `Box<Tree>` (idiomatic) | per-node alloc/free | 1.37 s | 18 MB |
| C++ — `unique_ptr` (idiomatic) | per-node alloc/free | 1.47 s | 18 MB |
| Elisa — packed enum, region-backed | columnar SoA store per region | 2.07 s | 88 MB |

## Takeaways

1. **Elisa's idiomatic region allocation is the fastest** — the struct + inferred-region + `new[auto]`
   version (zero manual memory management, fully memory-safe) **beats hand-written arena code** in
   both C++ and Rust, with competitive memory. The region is inferred and threaded by the compiler
   (docs/75); the programmer writes no arena, no free, no lifetime annotation.
2. The idiomatic *safe* C++/Rust (`unique_ptr`/`Box`) are ~3.4× slower than the arena/region versions
   — the per-node alloc/free cost that regions eliminate.
3. **The region-backed *packed enum* path is currently slow** (2.07 s, 88 MB). Each per-iteration
   tree creates its own `PackedStoreState` whose columnar `darray`s are heap-backed and not reclaimed
   by the region reset — pathological for the many-tiny-trees pattern (the columnar store was
   designed for a few large forests). Making the store's columns arena-backed (true region
   column-stacks, docs/74's original intent) is the clear next optimization; until then, prefer the
   struct form for many small short-lived trees.

## Files
- `binary_trees_struct.elisa` — Elisa struct/region version (fastest)
- `binary_trees.elisa` — Elisa region-backed packed-enum version
- `bt_unique.cpp`, `bt_arena.cpp` — C++ idiomatic + arena
- `bt_box.rs`, `bt_arena.rs` — Rust idiomatic + index-arena
