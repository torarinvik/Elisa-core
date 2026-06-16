# Comprehensions and fused reductions

This note records the pruned data-shaping surface.

Elisa keeps direct, compiler-lowered forms that expand to one obvious loop:

- list/set/dict comprehensions over finite iterable sources
- fold comprehensions with an explicit accumulator
- `for ... where ...` filtered loops
- query predicates such as `any`, `all`, `count`, and `first` with an inline
  `where` filter
- an explicit `by par` marker on the comprehension or reduction to run it in
  parallel (SIMD vectorization is the default and needs no marker — see below)

The language does not expose a lazy `Sequence` protocol, adapter chains, or
first-class filtered views as the general user model. Local filters belong in
the loop/query/comprehension header; reusable multi-step transforms should be
ordinary named functions or explicit loops.

The implementation goal is deliberately narrow: a reader should be able to
predict the lowering as a single loop without inventing an iterator pipeline.
When a transformation stops being that simple, write the loop by hand.

## Vectorization is the default

Comprehensions are designed to vectorize without ceremony. There is **no
`by simd` marker** — it was removed. The compiler lowers an eligible
comprehension to the SIMD-friendly shape automatically:

- **Maps** (`[ f(x) for x in xs ]`) lower to a presized indexed-store loop
  (`result.resize(n); for i: result[i] <- f(xs[i])`) rather than per-element
  `push`. Each output element is independent, so the result is bit-identical to
  the scalar map.
- **Folds** (`( acc + f(x) for x in xs with acc = seed )`) reduce in a
  **vectorizable tree order, not strict left-to-right**. This is the *defined*
  reduction order. For the eligible fast path — an associative fold (`+`/`*`)
  over an indexable darray variable with a numeric-literal seed — the tree is
  **pinned**: the fold lowers to a fixed width of `W=8` independent **strict**
  lane accumulators plus a fixed strict pairwise combine, so the rounded result
  is **bit-identical across every target and optimization level**. The lanes are
  independent, so the vectorizer still packs them into SIMD registers, but no
  reassociation flag rides on them — it can never reorder the reduction. Folds
  that cannot be pinned (a range source, an `if` filter, head bindings, a
  non-literal seed, or a non-associative body) fall back to a **reassociating**
  accumulator (`reassoc`+`contract`): still vectorizable into a tree, but the
  exact bits then depend on the target's chosen vector width.

### Why folds are tree-ordered

Floating-point `+`/`*` are not associative, so a left-to-right reduction is a
sequential recurrence that *cannot* be vectorized without re-bracketing — and
re-bracketing changes the rounded result. Rather than promise left-to-right and
then forbid SIMD, Elisa **defines** a comprehension fold's reduction order as a
tree, not left-to-right. The result is as accurate as (often more accurate than)
the naive ordered sum, and it vectorizes for free. The reassoc tier leaves
NaN/Inf/signed-zero at IEEE; only the broader, value-changing `-ffast-math` /
`@fast_math` opt-ins relax those.

For the eligible fast path the tree shape is **pinned in the lowered source**
(`W=8` strict lanes + a fixed strict pairwise combine), so the exact bits are
identical across every target and every optimization level — including `-O0`,
where the same tree is computed scalar. For unpinnable shapes the tree shape is
still whatever the loop vectorizer picks for the target (stable per-target, not
yet identical across targets). Integer folds are unaffected (integer `+`/`*` are
associative). Code that needs an exact left-to-right FP accumulation should write
the explicit loop.

### The `-Wperf` contract

Because vectorization is the default expectation, a comprehension or fold that is
lowered for SIMD but fails to vectorize is a performance defect, not a silent
fallback. Both the indexed-store comprehension maps and the reassociating fold
reductions tag their build loop for a post-optimization verifier; any tagged loop
left un-vectorized at `-O2`/`-O3` emits a `-Wperf` warning that **names the
construct** ("comprehension map" or "fold reduction") and the most likely blocker
for it (an aliasing source, or a loop-carried dependency the vectorizer could not
disprove). Under `-Wperf` the warning is promoted to a hard error.

Only loops actually lowered for SIMD are tagged, so there are no false alarms: a
body containing a call, an `if` filter, or a dict/set build is *not* shaped for
SIMD and is left untagged rather than warned about. The pinned fast-path fold is
also untagged — its independent strict lanes are packed by SLP, not the loop
vectorizer, so the `isvectorized` signal the verifier keys on would not apply.
`by par` remains an explicit opt-in for thread-level parallelism — a separate
axis from SIMD.

