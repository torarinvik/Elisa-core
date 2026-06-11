# Comprehensions and fused reductions

This note records the pruned data-shaping surface.

Elisa keeps direct, compiler-lowered forms that expand to one obvious loop:

- list/set/dict comprehensions over finite iterable sources
- fold comprehensions with an explicit accumulator
- `for ... where ...` filtered loops
- query predicates such as `any`, `all`, `count`, and `first` with an inline
  `where` filter
- explicit `by simd` / `by par` markers only on the comprehension or reduction
  being optimized

The language does not expose a lazy `Sequence` protocol, adapter chains, or
first-class filtered views as the general user model. Local filters belong in
the loop/query/comprehension header; reusable multi-step transforms should be
ordinary named functions or explicit loops.

The implementation goal is deliberately narrow: a reader should be able to
predict the lowering as a single loop without inventing an iterator pipeline.
When a transformation stops being that simple, write the loop by hand.

