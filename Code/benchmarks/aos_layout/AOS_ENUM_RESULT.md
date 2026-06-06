# AoS enum — the result (docs/76 Slice 2 complete)

A plain recursive `enum` now defaults to AoS-in-arena storage (one contiguous {tag, common, payload}
record per node, dense index handle). binary-trees N=18, identical checksum 68332206, -O3:

| form | time | RSS |
|---|---:|---:|
| **AoS `enum` (zero-ceremony default)** | **0.38 s** | **22 MB** |
| struct (`new[auto]` + pointers) | 0.41 s | 22 MB |
| Rust index-arena | 0.44 s | 22 MB |
| C++ bump arena | 0.45 s | 26 MB |
| Rust Box / C++ unique_ptr (idiomatic safe) | ~1.4 s | 18 MB |
| SoA `enum` (the OLD default) | 2.09 s | 88 MB |

**The AoS enum is the fastest implementation tested** — ~5.5× faster and ~4× lighter than the old SoA
enum, and it edges out the struct form and hand-written C++/Rust arenas, all while being a fully safe,
zero-ceremony `enum` + `match`. `enum X layout soa` opts back into columnar for whole-store column
scans; explicit packed enums (JSON DOM, ML AST) are unaffected.
