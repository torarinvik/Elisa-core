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
3. **The region-backed *packed enum* path is slower here (2.07 s, 88 MB) — by design, not by bug.**
   Investigated thoroughly: the store's columns are *already* arena-backed (metadata `darray`s grow
   via the region arena, column data via `arena_alloc`), and there is **no leak** — a tight loop of
   2,000,000 tiny region-backed packed trees holds steady at **1.4 MB** RSS. The cost is the
   *inherent* columnar (struct-of-arrays) overhead: every node carries a separate tag column, index
   column, and handle column, plus variant-row indirection — versus the struct form's single 16-byte
   bump per node. Peak RSS is dominated by the one biggest tree's columnar metadata, not by churn.
   Columnar SoA pays off for **large, persistent ASTs traversed many times** (cache locality across
   passes — the JSON DOM / ML-AST use case), *not* for build-once-check-once tiny trees. For this
   workload the **struct form is the right tool, and it wins**.

   **Honesty note on the 4× number.** This 2.07 s vs 0.40 s gap is *not* a pure-layout verdict — it
   mixes the columnar overhead with an allocation-path difference (the packed store builds rows via
   its own `PackedStoreState`/region machinery, not the plain inferred bump the struct form uses). The
   *allocator-independent* layout verdicts are cleaner and still favor the struct form: **memory** is
   ~2× (88 MB vs 22 MB, intrinsic per-node metadata) and **pure traversal** — the `ast_traversal`
   benchmark, where the build is amortized over 100 passes — is ~1.4× (0.68 s vs 0.49 s). So SoA loses
   on memory and on traversal *by layout*, and loses additionally on build/alloc by path; lead with
   the 2× / 1.4× when attributing the loss to the layout itself.

## Files
- `binary_trees_struct.elisa` — Elisa struct/region version (fastest)
- `binary_trees.elisa` — Elisa region-backed packed-enum version
- `bt_unique.cpp`, `bt_arena.cpp` — C++ idiomatic + arena
- `bt_box.rs`, `bt_arena.rs` — Rust idiomatic + index-arena
