# AoS unified-arena layout — the "darray of (common, tag, data)" form

Tests the user's proposal: lower a recursive enum to ONE contiguous `darray` of tagged nodes with
**index** children (instead of the columnar SoA packed store, or pointer structs). binary-trees N=18,
identical checksum (68332206), `-O3`.

| Form | time (real) | of which sys | peak RSS |
|---|---:|---:|---:|
| struct + `new[auto]` + inferred region (AoS, **pointer** children, loop-reset) | **0.40 s** | ~0 | **22 MB** |
| AoS `darray` (**index** children) + **manual** arena (`arena_free` per tree, doubling) | 1.00 s | **0.44 s** | 68 MB |
| packed columnar SoA enum | 2.07 s | — | 88 MB |

## What this shows

The AoS-unified *layout* is **not** the problem — it's the right idea. The 1.00 s is dominated by
**0.44 s of sys time (mmap/munmap)**: the hand-written version uses an explicit `Arena` + `arena_free`
per tree, which can't ride the inferred-region **loop-reset** (zero-syscall block reuse) that
`in auto:` gives the struct form. The extra memory (68 MB) is mostly the `darray`'s
**capacity-doubling** over-allocating ~2× vs the struct form's **exact bump**.

**Conclusion:** the `struct + new[auto] + inferred region` form already *is* the unified-AoS layout
(contiguous bump-allocated nodes) — and it's optimal precisely because it rides the inference
(loop-reset, exact bump, no manual `free`, no syscalls). A user-facing "AoS enum" should lower to
*that* machinery (inferred region, exact bump, index or pointer handles), **not** to a manually-managed
`darray`. The layout choice and the allocation-inference are separate wins, and the fast path needs
both.

## Files
- `binary_trees_aos.elisa` — AoS darray with index children + manual arena (the un-inferred version)
