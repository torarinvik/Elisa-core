# Proof-Carrying Views And Optimization Legality

This document proposes the next optimization-oriented design direction for
`llcontext`.

The goal is **not** to add explicit SIMD syntax, target intrinsics, or pragma
style optimization hints.

The goal is to make the language expressive enough that the compiler can
**prove when transformations are legal**.

That includes transformations such as:

- vectorization
- bounds-check elimination
- loop fusion
- unrolling
- load/store reordering within a kernel
- attaching stronger alias/alignment metadata to lowered IR

The core move is:

> model optimization-relevant memory and dependence facts in the compiler's
> internal view/provenance system, and expose only the safe constructions that
> produce those facts.

## Design Goals

The first optimization-proof slice should:

- stay aligned with existing pointer provenance, region, shape, and effect work
- keep legality facts compiler-internal by default
- avoid exposing `disjoint(...)`, `noalias(...)`, or `@vectorize` syntax to users
- let safe view-producing APIs imply stronger internal facts
- give the compiler enough information to optimize simple map/zip/reduction kernels
- keep target-specific vector lowering decisions in the backend, not in source syntax

## Non-Goals

Still intentionally deferred:

- explicit SIMD/vector value syntax like `vec[8, f32]`
- target intrinsics as a first-class language feature
- user-authored proof assertions for aliasing/disjointness
- general theorem proving about arbitrary offset/length arithmetic
- automatic vectorization of all arbitrary loops in phase 1
- exposing optimization contracts as effect families or protocol-state syntax

## Core Principle

The language should distinguish three questions.

### 1. Is a transformation legal?

This is a semantic question.

Examples:

- do two mutable views overlap?
- are loop iterations independent?
- is a reduction associative in the approved sense?
- can a bounds check be discharged from shape facts?

The type/provenance/effect system should answer this.

### 2. Is a transformation profitable?

This is a compiler/backend question.

Examples:

- should this lower to SIMD at this width?
- should the loop be unrolled?
- should the backend prefer scalar code on this target?

The optimizer should answer this.

### 3. How should it lower?

This is a target-lowering question.

Examples:

- NEON vs AVX vs scalar
- masked tail vs scalar remainder loop
- metadata on loads/stores/calls

The backend should answer this.

So the source language should mostly prove **legality**, not request specific
vector instructions.

## Internal Legality Facts

The compiler should learn a small internal vocabulary of optimization facts.

Recommended first set:

- `exclusive(v)` — `v` is the unique writable path used by the kernel
- `readonly(v)` — `v` is read-only for the duration of the kernel
- `disjoint(a, b)` — the memory ranges of `a` and `b` do not overlap
- `contiguous(v)` — elements of `v` are laid out contiguously in memory
- `unit_stride(v)` — advancing by one logical element advances by one physical element
- `same_extent(a, b)` — `a` and `b` have the same iteration length
- `aligned(v, N)` — `v` is known to have at least `N`-byte alignment
- `reduction(acc, op)` — `acc` participates in an approved reduction operation

These are **compiler-internal facts**, not user-written surface syntax.

## How Facts Are Produced

The compiler should derive these facts from existing language mechanisms:

- pointer provenance
- region/storage qualifiers
- shape-typed arrays/views/strings
- layout declarations
- effect/permission information
- restricted helper APIs that preserve or refine view facts

That means users should normally obtain optimized code by writing safe code with
well-designed APIs, not by writing proof annotations.

## Surface Design Rule

The surface language should expose **capabilities and constructions**, not proof
jargon.

Preferred:

- readonly vs mutable view APIs
- exact chunking APIs
- disjoint split APIs
- zip/same-length combinators
- restricted kernel forms later
- packed variant witness values spelled as `packedview[Enum.Variant]` when a
  packed-pattern proof needs to survive as a first-class value

Avoid by default:

- `disjoint(a, b)`
- `noalias(dst)`
- `@vectorize`
- `@simd`
- target-specific pragmas

Only expose a new source form when one of these is true:

1. the fact cannot be inferred reliably
2. the user is choosing between semantically distinct modes
3. a safe API must visibly guarantee the stronger fact
4. a restricted kernel form needs an explicit syntactic boundary

## First Concrete Target

The best first optimization target is the semantic shape behind elementwise
kernels such as:

```text
dst[i] = x[i] + y[i]
dst[i] = a * x[i] + dst[i]
sum += x[i]
```

For the compiler to optimize these confidently, it should be able to establish:

- `exclusive(dst)`
- `readonly(x)` and `readonly(y)`
- `same_extent(dst, x)` and `same_extent(dst, y)`
- `disjoint(dst, x)` and `disjoint(dst, y)`
- `contiguous(...)` and ideally `unit_stride(...)`
- `reduction(sum, +)` for the reduction case

This is the highest-value first slice because it enables both simple vectorized
maps and restricted reductions without inventing a large new language surface.

## View-Producing APIs

The first surface-facing work should be safe APIs whose semantics imply stronger
internal legality facts.

Examples of the intended shape:

- splitting one mutable view into two non-overlapping subviews
- constructing a readonly borrow of a view for a kernel scope
- exact chunking into lane-friendly pieces
- zipping views only when extents match
- refining alignment when the compiler/runtime can prove it

The exact spelling can be decided later. The important rule is that these APIs
must **produce facts**, not merely look optimization-friendly.

## Restricted Kernel Forms

Only after the internal facts and helper APIs exist should the language add a
restricted data-parallel form.

Good phase-1 candidates:

- `zip_map`
- `map` over an exclusive destination and readonly sources
- `reduce`
- exact chunk iteration

The form should be accepted only when the compiler can prove the required facts.

That preserves the language principle:

> source proves legality; backend chooses profitability and lowering.

## Relation To Existing Systems

This work should remain orthogonal to existing features.

### Pointer/region provenance

Optimization facts should be derived from provenance, not replace it.
A disjoint split of a view is meaningful only because the underlying provenance
system already knows where the memory comes from.

### Shape typing

Shape facts already provide the right starting point for:

- same extent
- exact chunk counts
- bounds-check elimination

The optimization-proof work should build on the shape system, not fork it.

### Effects and permissions

Effects help prove a kernel body is semantically well-behaved:

- no hidden I/O
- no unexpected synchronization
- no opaque mutation through unrelated globals

So effect information should participate in legality checks, especially for
restricted kernel forms.

### Affine/protocol types

Affine concurrency capabilities are a separate subsystem.
They should not be reused as the primary mechanism for optimization legality.
Optimization facts are mostly about views, memory layout, and loop dependence,
not thread/task protocol states.

## Recommended Staging

### Stage 1 — Internal fact lattice

Implement compiler-internal tracking for:

- exclusivity
- readonly
- disjointness
- contiguity
- same extent

No new syntax required.

### Stage 2 — Fact-producing helper forms

Add or refine safe APIs that produce those facts:

- disjoint split
- zip with matching extents
- exact chunking
- readonly borrow/view wrappers

### Stage 3 — One restricted kernel form

Add one narrow kernel construct or library-level form that requires these facts
and lets the backend attach stronger optimization metadata.

### Stage 4 — Reductions

Add restricted reduction legality and lowering once map/zip legality is stable.

### Stage 5 — Explicit vector values only if still needed

Only after the legality model and kernels prove useful should the language
consider explicit vector types or target intrinsics.

## Implementation Notes

The first implementation slice should prefer conservative correctness over
aggressive inference.

That means:

- if disjointness cannot be derived, do not assume it
- if contiguity is uncertain, keep the weaker model
- if a kernel body performs opaque calls or surprising effects, reject the
  restricted optimization form
- reuse existing provenance/view/shapes where possible instead of building a
  separate optimizer-only fact graph

## Summary

The right next move for `llcontext` optimization support is:

- no explicit SIMD syntax yet
- no user-facing alias proofs by default
- compiler-internal legality facts for views and kernels
- safe view constructors that imply those facts
- one narrow data-parallel kernel form after the fact system exists

This keeps the language principled, composable, and aligned with the rest of the
proof-oriented design.
