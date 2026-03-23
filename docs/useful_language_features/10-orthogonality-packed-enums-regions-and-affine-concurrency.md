# Orthogonality Rules For Packed Enums, Regions, And Affine Concurrency

This document defines the sound unified model for low-level parallel features in
`llcontext`.

The language goal here is not “high-level concurrency made fashionable”. It is:

- keep packed enums cheap enough for hot compiler IR and AST work
- keep region allocation explicit and fast
- make thread/task/guard protocols statically one-shot
- make publication between threads explicit
- avoid forcing any one subsystem to secretly carry another subsystem’s meaning

That last point matters most. The system stays understandable only if these
axes stay separate.

## The Three Axes

Every value in the language should be understood across three orthogonal axes.

### 1. Representation / Layout

This answers:

- how is the value physically represented?
- does it use packed enum layout?
- what is the ABI?
- what tag/payload decode is needed?

Examples:

- `struct`
- packed enums
- fixed arrays
- runtime carrier structs

### 2. Storage / Provenance

This answers:

- where does the storage live?
- who owns allocation and reclamation?
- does the value carry references into a region or packed store?
- can those references survive rewind/reset/destroy?

Examples:

- `stack`, `heap`, `static`
- named regions
- packed enum stores
- region checkpoints

### 3. Usage / Protocol

This answers:

- is the value copyable or affine?
- does it represent a protocol state?
- what effects are required to operate on it?
- may it cross a thread boundary?

Examples:

- `Thread[T, Joinable]`
- `Task[T, Pending]`
- `MutexGuard[Held]`
- `can[...]`
- `sendable(T)` / `shareable(T)`

The core rule is:

> packedness is layout, regions/stores are provenance, and affine protocol
> state is usage. None of those should silently imply the others.

## Why This Matters For Compiler Workloads

Compiler pipelines naturally want this shape:

1. build AST/IR in a thread-local arena or packed store
2. checkpoint and rewind aggressively while parsing/lowering
3. freeze the finished graph
4. publish readonly handles to parallel analysis/codegen workers

If packed enums are allowed to become “the concurrency mechanism”, or if region
ownership is allowed to blur into thread protocol state, the model quickly
stops being predictable.

The language should instead make this flow explicit:

```context
region parse_arena(1_000_000u)
store: Expr.Store[Local] = Expr.Store(parse_arena)

in store:
	root: Expr = parse_module(tokens)

frozen: Expr.Store[Frozen] = freeze(move store)
left: Thread[Stats, Joinable] = spawn1(analyze_stats, root)
right: Thread[Code, Joinable] = spawn1(lower_module, root)
```

The important thing is not the spelling. It is the boundary:

- mutable construction is local
- publication is explicit
- readonly packed handles become shareable only after freeze

## Unified Statement-Oriented Surface

These forms all follow the same design direction:

- `region scratch`
- `mark scratch as cp`
- `restore scratch from cp`
- `in store:`
- `lock mu as g:`
- `move value as alias`
- `move holder as Holder(thread, count)`
- `move job as Job.Run(thread, priority)`
- `move node in store as Expr.Add(left, right)`

They are all statement-oriented capability binders.

That is a good fit for `llcontext` because the language already prefers explicit
control points over hidden control flow. New parallel features should keep using
that style instead of introducing a second, expression-heavy resource dialect.

## Packed Enums

Packed enums remain a layout and store feature.

They should obey these rules.

### Packed lowering contract (v1)

The v1 compiler lowering contract is intentionally simple:

- packed enums are always handle-based values
- the canonical compiler-graph lowering is `index-soa`
- frozen stores are the intended flat-scan / publication form
- `Store[Frozen]` is a semantic publication and readonly gate, not a second
  packed-handle representation

The key invariance rule is explicit:

> one packed enum gets one runtime handle representation across all store
> states in a compilation unit

That means `Expr.Store[Local]` and `Expr.Store[Frozen]` may differ in what
operations are legal and profitable, but not in the runtime handle type used
for `Expr` values. The semantic `Frozen` state controls readonly bulk forms
such as `parallel for`, `frozen.tags`, and published scans. It does not select
an alternate packed ABI.

The public/default compiler story in v1 is therefore:

- compiler-shaped packed graphs canonically lower as handle-based
  `index-soa`
- frozen stores are the intended publication form for readonly scans and bulk
  operations
- legacy packed ABI selection is temporary, for debugging and compatibility,
  rather than the primary model users are expected to think in

### Packed enum values are handles, not ownership modes

A packed enum value like `Expr` is a cheap typed handle into an `Expr.Store`.

The value itself should not become affine just because it is packed.

What matters for thread crossing is the backing store capability, not the tag
encoding.

### Packed stores are the real capability

The backing store type is what determines whether a packed value is:

- mutable
- thread-local
- frozen
- publishable
- shareable

So the store type, not the packed enum handle type, should carry state like:

- `Expr.Store[Local]`
- `Expr.Store[Frozen]`

or an equivalent capability split.

### Packed destructuring must lower through existing packed machinery

Implemented packed destructuring uses:

```context
move expr in store as Expr.Add(left, right)
```

that must lower through the same tag-read and payload decode path already used
by packed `match` / `in store:` operations.

It must not invent a second “destructure packed payload” subsystem.

### No implicit boxing to satisfy concurrency

Parallel features must not force packed enum payloads through heap boxing,
closure boxing, or generic runtime object wrappers just to make thread APIs
look nicer.

The runtime seam may erase typed entry points or results. That is acceptable.
But the packed payload path itself should stay explicit and cheap.

### Phase-1 restriction: no affine payloads inside packed enums

Packed enums are essential for compiler graphs. Affine protocol values are
essential for one-shot concurrency capabilities.

Do not mix those in the first slice.

So:

- packed enums may contain copyable payloads
- packed enums should not contain `Thread[...]`, `Task[...]`, `MutexGuard[...]`, user-declared `affine struct` values, or aggregates containing them

That avoids needing packed-pattern partial-move rules before the ordinary affine
system is fully settled.

## Regions And Region Dependencies

Regions are storage/provenance capabilities, not concurrency capabilities.

### Default thread locality

Named regions are thread-local by default.

So are:

- references allocated from them
- mutable packed stores backed by them
- mutable values that carry those references

These are not `sendable` and not `shareable`.

### Region checkpoints act on dependency sets

The sound model is:

every value has a latent region dependency set:

```text
deps(v) = { (region, generation), ... }
```

Meaning:

- this value may contain one or more references into those region generations

Operations propagate dependencies structurally:

- `new[scratch] x` produces dependency `{(scratch, current_generation)}`
- copy preserves the dependency set
- `move` preserves the dependency set
- struct/array/enum construction unions dependency sets of their components
- destructuring distributes dependency sets to the bound pieces
- casts and parens preserve dependency sets

Checkpoint operations invalidate matching dependencies:

- `restore scratch from cp` invalidates values depending on allocations newer than `cp`
- `reset scratch` invalidates all values depending on current generations of `scratch`
- `destroy scratch` invalidates all values depending on `scratch`

The important point is that this is not an affine rule. It is provenance.

### Region dependency tracking should be structural

Current direct-reference invalidation is a good conservative start, but the
unified model wants structural propagation.

Examples that should conceptually preserve region dependencies:

```context
value: any Token&
holder: Holder = Holder(value, 1)
move holder as Holder(alias, n)
```

Here:

- `holder` depends on the region behind `value`
- `alias` depends on that same region
- `n` does not

That is the correct target model.

The implemented compiler now tracks direct and simple structural provenance
through aggregate construction and `move ... as ...` destructuring, but it
should still stay conservative rather than pretending to prove more than it
does through arbitrary aliasing.

### Region refs are not ownership

A region-backed reference can be copyable and still be non-publishable.

That is not a contradiction:

- copyability answers “may this value duplicate within one thread?”
- region provenance answers “what storage lifetime does it depend on?”
- send/share answers “may this cross thread visibility boundaries?”

Keeping those questions separate makes the rules much easier to predict.

## Affine Types And Protocol State

Affine protocol values live on the usage axis.

Examples:

- `Thread[T, Joinable]`
- `Task[T, Pending]`
- `MutexGuard[Held]`

### Affine means one-shot use by type

Affine values:

- cannot be copied
- must be transferred with explicit `move`
- can be rebound or destructured only through consuming forms

Examples:

```context
result: i64 = join(move worker)
move job as Job(thread, priority)
move node in store as Expr.Add(left, right)
detach(move thread)
```

### Structural affine propagation

If a value contains an affine field, the containing value is affine.

That rule applies independently of:

- whether the aggregate is packed or unpacked
- where it is stored
- whether it also carries region refs

The type system should not special-case concurrency carriers here.

### Protocol state belongs in the type

Examples:

- `Thread[T, Joinable]`
- `Task[T, Pending]`
- `MutexGuard[Held]`

Operations consume one state and produce another or produce a result:

- `join(move Thread[T, Joinable]) -> T`
- `detach(move Thread[T, Joinable]) -> void`
- `unlock(move MutexGuard[Held]) -> void`

This stays orthogonal to region/storage provenance.

## Publication, Sendability, And Sharing

The language needs two different questions.

### `sendable(T)`

Can a value move to another thread by ownership transfer?

This should reject values that depend on:

- thread-local regions
- mutable local packed stores
- non-sendable runtime capabilities
- guards tied to a current thread or lock state

### `shareable(T)`

Can a value be observed from multiple threads concurrently without transfer of
unique ownership?

This should accept:

- immutable plain data
- frozen packed handles whose backing stores are frozen/shareable
- readonly views into published immutable storage

This should reject:

- mutable region-backed refs
- mutable packed stores
- affine one-shot protocol carriers

### Freeze / publish boundary

For compiler workloads, the implemented publication bridge is:

```context
frozen: Expr.Store[Frozen] = freeze(move store)
```

The semantic effect is:

- mutable local construction capability becomes immutable published capability
- packed handles derived from that capability become `shareable`
- the remap is structural and recursive through aggregates, arrays, views,
  helper returns, and destructuring binders
- mutable region/store operations stop being legal through the published handle

### Opaque helper provenance contracts

Opaque helpers sometimes preserve provenance without exposing their body. The
implemented extern contract family is:

```context
@borrows_return(path)
@borrows_return_field(field, path, ...)
@borrows_return_rebased(path)
@borrows_return_field_rebased(field, path, ...)
```

These mean:

- exact borrow from a source path
- exact borrow into a struct return field path such as `meta.items`
- rebased borrow that preserves provenance but widens indexed element state to
  wildcard element state
- the same rebased rule, attached to a struct return field path such as
  `meta.items`

The rebased forms are deliberately provenance-only. They do not prove exact
slice offsets, lengths, or index arithmetic.

## Sound Phase-1 Restrictions

To keep the model small and honest, the first slice should keep these
restrictions.

### Allowed

- affine `Thread`, `Task`, and `MutexGuard`
- user-declared `affine struct`
- explicit `move`
- `move ... as ...` for whole-value rebinding, simple struct destructure, ordinary enum variant destructure, and packed enum variant destructure with explicit `in store`
- packed enums with copyable payloads
- region checkpoints with conservative invalidation

### Disallowed or intentionally deferred

- affine payloads inside packed enums
- implicit partial moves from aggregates
- references to affine-containing values
- sending mutable regions or mutable packed stores across threads
- structural region dependency proofs through arbitrary aliases until the
  compiler really tracks them

When the compiler cannot currently preserve soundness precisely, it should
reject or conservatively approximate. It should not silently downgrade the rule.

## Lowering Principles

The backend should keep the same separation.

### Packed lowering

- packed tags and payloads use the existing store-aware ABI
- canonical compiler lowering uses handle-based `index-soa` by default for
  packed enums, including compiler-graph workloads built first in
  `Store[Local]` and later published through `Store[Frozen]`
- packed pattern sugar lowers to the same packed decode helpers as `match`
- first-class packed variant witnesses should surface as `packedview[Enum.Variant]`
  so they can be passed, returned, and stored without inventing a second proof
  vocabulary
- a successful packed pattern should refine the scrutinee itself to that
  `packedview[Enum.Variant]` for the whole matching branch or arm, making
  explicit `view`/`open` forms optional sugar instead of required ceremony

### Region lowering

- regions, marks, restore, reset, and destroy lower to arena helpers
- region dependency tracking is compile-time only and does not change runtime
  layout

### Concurrency lowering

- typed `Thread` / `Task` / `MutexGuard` wrappers lower to explicit runtime
  carriers
- affine typing changes source legality, not carrier layout
- explicit function-value erasure at runtime seams is acceptable

## Practical Compiler Pipeline Model

The target compiler-friendly workflow is:

1. allocate mutable scratch and output graphs in local regions/stores
2. use checkpoints aggressively during parse/lower/typecheck phases
3. keep packed enum handles cheap and ordinary during local construction
4. freeze completed graphs before cross-thread publication
5. send or share only published immutable handles into worker threads
6. keep thread/task/guard protocols affine and explicit

That combination gives:

- predictable data layout
- explicit memory lifetime
- no GC pressure
- easy hot-path profiling
- enough static checking to stop the worst protocol and rewind mistakes

## Current Compiler Status

The current implementation is partway to this model:

- affine thread/task/guard-style usage exists
- explicit `move` and `move ... as ...` exist
- protocol-state handles exist as `Thread[T, Joinable]`, `Task[T, Pending]`,
  and `MutexGuard[Held]`
- visible `affine struct` declarations exist
- stateful packed stores exist as `Store[Local]` and `Store[Frozen]`
- `freeze(move store)` exists and is the publication boundary for packed stores
- canonical backend lowering now treats packed compiler graphs as
  handle-based `index-soa` by default, with one handle representation per packed
  enum across all store states in a compilation unit
- compiler-internal send/share checks are enforced at transfer seams such as
  `spawn1` and `pool_submit1`
- `sendable/shareable` currently remain compiler-internal derived judgments
- region and packed-store provenance tracking is structural through aggregates,
  arrays, views, helper returns, and destructuring binders
- `freeze(move store)` remaps packed-store provenance structurally, not only at
  root bindings
- extern provenance contracts exist in exact and rebased forms
- packed enums already use explicit store-aware lowering

The main remaining semantic gaps are the more precise ones:

- richer opaque transform contracts beyond exact or rebased provenance
- more precise alias reasoning for patterns that still fall back to
  conservative rejection
- continued validation against larger compiler-shaped parallel fixtures
