# AST traversal benchmark — packed (columnar SoA) vs struct (pointer/region)

Goal: find a workload where the **packed enum** (columnar struct-of-arrays) beats the **struct**
(pointer + inferred region) form. The hypothesis was: build a large AST once, traverse it many times,
and let SoA cache-locality pull ahead.

Setup: one depth-20 `Expr` tree (~2.1M nodes, fields `span`/`value` + two children), traversed 100
times summing `span + bias` per node (a per-pass `bias` defeats loop-hoisting so all 100 passes run
for real). Both produce the identical sum. `-O3`, `/usr/bin/time -l`.

| Form | repeated-traversal time | peak RSS |
|---|---:|---:|
| **struct + inferred region (`new[auto]` + pointers)** | **0.49 s** | **85 MB** |
| packed enum, region-backed (columnar SoA) | 0.68 s | 183 MB |

## The hypothesis was wrong — struct wins here too

The struct form is **~1.4× faster on traversal** and uses **~2.2× less memory**, even in the scenario
that was supposed to favor columnar layout. Two reasons:

1. **Tree traversal is inherently pointer-chasing / scattered, not linear.** The classic SoA win
   comes from *streaming one column linearly* across all rows. A tree walk visits children in
   tree-order, so even dense columns are read out of order — no streaming benefit.
2. **The packed node carries more per-node metadata, not less.** Each row adds a handle column
   (8 B), an index column (4 B), and a tag column (4 B), plus variant-row indirection — on top of the
   payload. So packed is *heavier* per node than a 32-byte struct, not denser. (Hence 183 MB vs 85 MB.)

## Conclusion (across this and the `binary_trees` benchmark)

For **tree-shaped data** — build and walk — the **struct + inferred-region** form wins on every axis
(build, traversal, memory) and beats hand-written C++/Rust arenas. The packed/columnar enum does not
win any of these.

Where the packed form's value actually lies is **semantic, not raw speed**: a handle is a plain `u32`
index (no raw pointer — serializable, relocation-stable, freeze/publish-able), and variant-sparse
layout stores only the fields a variant actually has. Those are real properties for persistent /
shared / frozen ASTs, but they cost per-node bookkeeping that makes columnar slower and heavier for
ordinary in-memory tree traversal. A genuinely columnar-favorable workload would be a **linear
analytical pass over one field across all rows** (e.g. "tag histogram over the whole store"), which
is not how you traverse a tree — and is not currently exposed as a first-class linear column scan to
user code.

**Takeaway:** reach for the struct + `new[auto]` + inferred-region form for tree-shaped data; reach
for packed enums when you need stable serializable handles / freeze-publish semantics, accepting the
per-node overhead as the price of those properties.

## Files
- `repeat_packed.elisa`, `repeat_struct.elisa`
