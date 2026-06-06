# 76 — Enum layout & handles (the canonical model)

> Status: design + phased implementation. **This doc is the source of truth for how `enum` values are
> laid out and referenced.** It supersedes the user-facing model in docs/74 (region-backed packed
> enums) and the packed-store framing in docs/69, and builds on docs/75 (region-polymorphic
> functions), docs/73 (default stack backing), docs/71 (multi-stack regions), docs/68 (region memory
> model), docs/10 (orthogonality), and the shipped `layout` struct grammar (docs/01).

## The decision, in one sentence

**An `enum` is a value; a *recursive* one automatically lives in the current arena behind an opaque
index handle — you write the `enum` and the `match`, and the fast / safe / serializable path is the
only one you can see. `layout soa` is a deliberate expert step sideways, never something that happens
to you.**

This replaces the `packed enum` + `Store` + `in store:` surface as the *default*. The columnar store
does not disappear — it becomes an opt-in layout for the workloads it genuinely wins (§5).

## Why (grounded in measurement)

`Code/benchmarks/binary_trees` and `Code/benchmarks/ast_traversal` (all checksums identical, `-O3`):

| Form | binary-trees | repeat-traverse | note |
|---|---|---|---|
| struct + `new[auto]` + inferred region (AoS, contiguous bump) | **0.40 s / 22 MB** | **0.49 s / 85 MB** | beats hand-written C++/Rust arenas |
| region-backed **packed columnar** enum | 2.07 s / 88 MB | 0.68 s / 183 MB | loses on memory (~2×, intrinsic) and pure traversal (~1.4×) |
| idiomatic safe C++ `unique_ptr` / Rust `Box` | ~1.4 s | — | the pointer-forest baseline |

Two findings drove the model:

1. **The struct + `new[auto]` form *is* the unified Array-of-Structs layout** (contiguous,
   bump-allocated nodes) — and it won not by layout cleverness but because it rides the
   inferred-region allocator (loop-reset, exact bump, no syscalls, no manual free, region-poly
   threading, docs/75). A hand-written `darray`-of-tagged-nodes hit 1.0 s — **0.44 s of it pure
   mmap/munmap** — because a manually-managed container can't ride that inference. *Layout and
   allocation-strategy are separable wins; the fast path needs both.*
2. **Columnar SoA's loss on trees is intrinsic, not a bug.** Each row carries constant per-node
   bookkeeping (handle/index/tag columns + variant-row indirection) regardless of variant count, and
   depth-first traversal chases children in scattered tree order so SoA's streaming-column locality
   never materializes. SoA wins *linear* whole-store column scans, which is not tree traversal (§5).

So the default lowers an enum to the AoS-in-arena machinery the struct form already proves optimal.

## The default layout

**Rule (local & structural — never inferred from usage):**

- An enum that **references `Self`** (directly or mutually) → **AoS node in the inferred arena**:
  one contiguous record per node `{common fields, variant tag, payload}`, children referenced by an
  **opaque index handle**, allocated via the inferred region (docs/73/75) — exact bump, loop-reset,
  region-poly threaded.
- A **non-recursive** enum (`Option`, `Result`, a C-style tag) → **inline value** (no arena, no
  handle). The compiler picks this silently.

Children are **indices, not pointers** — this is settled (docs/74 "index ≠ pointer"). One
representation then serves construction, mutation, growth (`reserve_commit` stable bases, docs/73),
freeze, and serialization with **zero usage-driven representation switch**: it kills the
dangling-pointer-on-growth hazard (a pointer into a growing bump arena dangles the instant you mutate
an existing node) and the freeze cliff in a single move.

## The beginner surface

The vocabulary is `enum`, the variant constructors, `match`, and (only when variants share fields)
`common(...)`. No `store`, no `columns`, no `packed`, no `&?`/`null`, no `new[...]` bracket, no arena:

```elisa
enum Expr:
    common(span: int)                  # fields every variant carries (one line)
    Int(value: int)
    Add(left: Expr, right: Expr)       # a recursive child is the bare type

def make(depth: int) -> Expr:
    if depth <= 0: return Expr.Int(span: 0, value: 1)
    return Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))

def eval(node: Expr) -> int:
    match node:                        # storeless match — no [0], no &, no `in store:`
        Expr.Int(value: v): return v
        Expr.Add(left: l, right: r): return eval(l) + eval(r)
```

**Hidden until an expert asks:** AoS-vs-SoA (default AoS); inline-vs-AoS (compiler picks inline for
non-recursive enums); the region/arena/loop-reset/exact-bump provenance; the index width and null
sentinel; and `new[auto]` itself (a bare `new`/constructor with an inferred region is the ceiling —
the `[auto]`/`[scratch]`/`[store]` bracket never appears in intro code).

**The node type is an opaque handle from day one — compiler-enforced.** A beginner can never extract
a raw `Expr&` from a handle (no `&`, no borrow-returning accessor, no field-on-handle that yields a
pointer). This single rule is what makes "graduate to serialization without a rewrite" true; it must
be a *checked* rule in the existing borrowed-owner-ref / interior-borrow machinery — if a raw pointer
can leak, the no-cliff property is false.

### Shared fields: `common(...)`

Fields carried by *every* variant are written with `common(...)` — a colon-less, parenthesized form,
canonical:

```elisa
enum Expr:
    common(span: int, metadata: cstr)   # one line; uniform with a variant line
    Int(value: int)
    Add(left: Expr, right: Expr)
```

`common(...)` is chosen over the alternatives on a consistency rule already in the language: **`:`
introduces an indented block; `(...)` is an inline, colon-less group** (`Int(value: int)`, `f(a, b)`).
So `common: (...)` — colon *and* parens — is the one spelling that fights the grammar; `common(...)`
reads exactly like a variant line (it *is* the "always-present variant"), parses trivially (the reader
and the parser never have to disambiguate a bare field from a variant), and scales to many fields with
multi-line parens, no new syntax:

```elisa
enum Expr:
    common(
        span: int,
        line: int,
        col: int,
    )
    Int(value: int)
```

This supersedes the earlier "shared fields are just bare fields (no keyword)" idea: the explicit
grouping and the trivial parse are worth one reserved word inside an enum body. The legacy `common:`
indented block keeps working (back-compat / a verbose multi-line option), with `common(...)` the
documented canonical form. (Considered and rejected: the header form `enum Expr(span: int):` — it
collides with the `[T]` generic and `layout …` suffixes and shoves a long shared list far from the
`:`.)

## The opaque index handle

A handle is an unsigned integer index relative to its store. Because it is **opaque** (no arithmetic,
the integer is never visible), the compiler owns its representation:

- **Width is a density ⇄ capacity dial:** `u8 | u16 | u32 | u64`, default **`u32`**. The index is a
  **node-index (row number), not a byte-offset**, so `u32` caps a store at ~4.3 billion *nodes*
  (a >64 GB AST) — RAM runs out first — and the cap is **per-store**, not global. Decode is
  `base + index*stride` (fixed-width AoS) or `column[index]` (SoA): one multiply-add against a
  region-stable base. `u16` (65 535 nodes) is the sweet spot for many small bounded stores — e.g. one
  store per function AST in a self-hosting compiler — halving every child edge to 2 bytes.
- **Free null sentinel:** the top value at each width (`0xFF`/`0xFFFF`/`0xFFFFFFFF`/…) is reserved to
  mean *absent*, so an optional child (`Expr?` — an empty subtree, an `else` branch, an end-of-list
  link) costs the **same** as a required one. This is the index-world equivalent of pointer niche
  optimization (`Option<&T>` == `&T`): a pointer gets address 0 for free from the hardware; an index
  *manufactures* an invalid value by reserving one slot (losing exactly one representable node).
- **Overflow is a loud panic** at the allocation site (same discipline as `reserve_commit`
  exhaustion), never a silent wrap — so sizing down to `u16`/`u8` is *safe*: the compiler enforces the
  ceiling for you.

## Nested side-tables & cross-region edges

A variant payload may itself hold a heap-shaped value — a `darray`, a `dict`, a string buffer (a
"side-table"). The question is *which region that side-table lives in*, and how (if ever) you name a
different one.

```elisa
enum FooEnum:
    Foo(value: int)
    Baz(rofl: u64, mao: darray[u16])     # a side-table inside a variant
```

### Same region — the default, fully inferred

`mao`'s backing is inferred to live in **the node's own region**: co-located with the `FooEnum` node,
freed in bulk when that region goes. No annotation, no region parameter. There is no separate "heap" —
the side-table is a region-allocated buffer sitting next to the node (this is the AoS generalization
of what the columnar store calls a "side table"). The node is a fixed record (`rofl` + a region-stable
handle to the buffer) plus that buffer, both in one arena. This is just the §"opaque index handle"
co-location guarantee applied one level down: a node's *contents* — child handles **and** containers —
default to the node's region.

### The binding rule (why a declared `@region` can't work)

A region name written in a **field declaration** has nowhere to be bound — *unless* it is a parameter
of the type (`enum FooEnum[region shared]`) or the variant (`Baz[region shared](...)`). There is no
third option: a bare `mao: darray[u16] @shared` on the declaration references an unbound name and is
**rejected**. So "annotate the field" collapses into "add a region parameter," and the real decision
is parameter-vs-inference.

### Cross-region — inferred from the argument, type stays region-free

When a side-table must live in a *different, longer-lived* region than the node, **do not name it in
the type.** The cross-region edge is inferred from the **argument's provenance at construction**:

```elisa
region shared(reserve_commit):
    side: darray[u16] = [1, 2, 3]            # `side` lives in `shared`
    in nodes:
        node = FooEnum.Baz(rofl: 5, mao: side)
        # compiler records: node.mao points into `shared`; checks `shared` outlives `nodes`
```

`node`'s type is just `FooEnum` — no `@shared`, no `[region shared]`. The compiler records a
provenance fact ("`mao` is in `shared`") on the value and the existing outlives/escape checker enforces
`shared` outlives `nodes`. Same machinery as same-region; it just remembers a second region for that one
field. Where the edge must cross a **function** boundary, the second region is threaded on the
*function* (docs/75 region-polymorphism, its multi-region tail), never on the enum type:

```elisa
def build(side: darray[u16]) -> FooEnum:     # `side`'s region threaded by inference, like docs/75
    return FooEnum.Baz(rofl: 5, mao: side)    # node in the inferred region; mao in side's region
```

`build` is polymorphic over *two* regions (the node's and the side-table's); both inferred, both
threaded; `FooEnum` stays clean.

### Why not a region parameter on the type

`enum FooEnum[region shared]` is **rejected** for two reasons — distinct from why docs/74 rejected
`packed enum Expr[region r]`. (That earlier rejection was about an *erased, redundant* region — the
handle already carried it, so `Expr[r]` and `Expr[other]` were the identical ABI word. A cross-region
*field* names a **genuine second provenance**, so the redundancy argument does not apply here.) The
reasons it is still rejected:

1. **It re-creates the threading wall.** The moment `FooEnum[shared]` exists, every function building or
   consuming one needs `[region shared]` threaded through it — the exact `undefined identifier shared`
   wall region-polymorphic functions (docs/75) exist to remove.
2. **It splits the type by provenance.** `FooEnum[a]` and `FooEnum[b]` would be different types with
   byte-identical layout, differing only in where one field points — provenance leaking into type
   identity, which docs/10 forbids.

**Fallback (only if value-level inference proves too costly):** a region parameter on the **variant**
(`Baz[region shared](...)`), scoped to the one variant that actually crosses — never smeared across the
whole type. This is the explicit escape hatch, not the model.

> Correction to docs/74: that doc's `ty: TypeExpr @types` field annotation only works because `types`
> names a **globally-known store** (the ML-AST forest has a distinct program-level `TypeExpr` store).
> For an *ad-hoc local* region there is no such global name, so a declared `@region` cannot bind — the
> construction-time inference above is the mechanism. docs/74's "cross-region edges" section is
> superseded by this one.

## Expert tweaks (the `layout` suffix, reused from structs)

The shipped struct `layout` grammar (`struct Name layout soa:`, modes `aos|soa|c|packed`) extends to
enums — **one vocabulary, no new `@annotation`:**

```elisa
enum Expr layout soa:                  # columnar SoA — for whole-store column scans (§5)
    span: int
    Int(value: int)
    Add(left: Expr, right: Expr)

enum Expr layout soa(sparse):          # + variant-sparse payload columns — wide, disjoint payloads
    ...

enum Huge layout soa(index: u64):      # widen the handle for a genuine giant store
    ...

enum Small layout aos(index: u16):     # narrow the handle for max density on a bounded store
    ...
```

Three hard rules:

- **`layout` is layout only — axis 1 (docs/10).** It carries no region (`enum Expr layout soa[region
  r]` is rejected, exactly as docs/74 rejects `packed enum Expr[region r]`) and says nothing about
  freeze/affine usage (axis 3). Orthogonality is physical, not nominal.
- **Pointers-vs-indices is *not* a dial.** Both AoS and SoA are index-backed; the representation never
  depends on usage. (A `aos(handle: pointer)` escape hatch is reserved only for a future cross-region
  forest; not shipped speculatively.) `freeze`/serialize is a representation no-op precisely because
  the handle is already an index — so there is no `freeze ⇒ index` inference, no spooky cross-axis
  dependency.
- **Never infer SoA from usage.** A distant column scan silently flipping a tree to SoA is a non-local
  cliff with miserable errors. It is a `-Wperf` *hint* only ("this looks column-analytical; `layout
  soa` on Expr may help"), accepted by the human typing the keyword. Likewise auto-narrowing the index
  width is allowed *only* when a small fixed capacity is declared in the same `reserve_commit(N)` (a
  local, structural fact), never from distant usage.

## §5 — Where SoA stays, and the primitive that makes it a real path

Do not throw SoA away. It has two genuine win regimes, and the opt-in is a **dead end** unless both
are served:

1. **Whole-store linear column scan** — tag histograms, "rewrite every `Add`", whole-AST analytical
   passes (a type-checker doing dozens of passes; a query engine scanning a DOM for one tag). The
   repeat-traverse data (SoA gap *shrinks* under repeated linear passes) is where columnar streaming
   starts paying. This path **requires a first-class column-scan operation shipped with the keyword**,
   or experts hand-walk rows and the opt-in buys nothing:

   ```elisa
   enum Expr layout soa: ...
   for n in Expr.column(.tag): ...     # first-class column iteration — MUST ship with `layout soa`
   ```

2. **Wide-variant memory determinism** — a 30–80-variant AST where AoS pads every node to the union's
   widest payload; `soa(sparse)` pays per-variant instead. Reachable via `layout soa(sparse)`, but
   **undiscoverable** without help: add a **static declaration-site lint** (compile-time, no profiling)
   — high payload-width variance × high variant count ⇒ *"`layout soa(sparse)` would cut ~N% memory."*
   This is the one discoverable home for the "many variants" intuition, on the *memory* axis where it
   is actually true.

**Sequencing rule: make column scans first-class *before* demoting the columnar default**, or the
JSON-DOM / ML-AST authors that motivated SoA end up strictly worse off than today.

## Migration

Today's `packed enum` is index-SoA (`PackedEnumABIIndexSOA`; the JSON DOM, the ML AST, explicit-store
callers). **Two cohorts wrote `packed enum` for opposite reasons**, and a silent alias traps one:

- Cohort A wanted **SoA column behavior** → should land on `enum … layout soa` (representation kept).
- Cohort B wanted only docs/74's **storeless ceremony-free surface** → should *delete* the keyword and
  get the fast AoS default.

The syntax can't tell them apart, so **do not auto-alias `packed enum → layout soa`** — that silently
freezes Cohort B on the slow path and deletes Cohort A's column performance with no diagnostic.
Instead:

> **`packed enum` becomes a deprecation *error* that forces the choice:** *"`packed enum` is retired.
> For whole-store column scans, write `enum … layout soa`. For the default fast tree, write plain
> `enum`."*

- `common(...)` is the canonical shared-field form; the legacy `common:` indented block keeps working
  and lowers to the same shared-field node prefix.
- The explicit-store path (`Expr.Store(arena)`, `in store:`, `match node in store:`) is **kept
  verbatim** as the escape hatch for the cross-region forest (docs/74 already preserves it) — never
  the default.
- `packed` survives only as `layout packed struct` (honest bit-tight struct layout), never on enums.

## The gating prerequisite (the real wall)

The AoS default is frictionless **only if region-poly threading is total** for any function that
returns or builds a node. Today a bare `darray.push` / storeless-`match` store recovery / `new[auto]`
**inside a helper** can still error `requires an active in <arena>: scope`, and docs/75 step 4's
machinery is the foundation. Recursive builders are exactly where beginners live; that error on a
beginner's first recursive function is the precise outcome this design exists to prevent. **Total
region-poly threading + retargeting the storeless surface onto AoS index handles is the gating
deliverable** — everything else is downstream.

## Implementation roadmap (phases)

| Phase | Delivers | Depends on |
|------|----------|-----------|
| 0 | **Close region-poly threading gaps** so storeless builders never hit "requires an active in <arena>" — container ops (`darray.push`) and constructors in helper functions thread the caller's region like `new[auto]` already does (docs/75). Note: enum *construction* + *match* threading is already done (docs/74/75); this phase is the remaining explicit-container gap. | docs/75 |
| 1 | **DONE** — `layout` on enum declarations: parses `enum … layout soa\|aos(sparse, index: uN)`, carries Layout/LayoutSet/LayoutSparse/IndexWidth onto EnumDecl + EnumType; bad index widths rejected at parse. `common(...)` canonical shared-field form also DONE. | struct `layout` grammar |
| 2 | **Opaque index handle ABI**: index width `u8…u64` (default `u32`, node-index), free null sentinel at each width, loud overflow panic; opaque-handle check (no raw `&` leak). **Semantic foundation DONE** (`ResolvedIndexWidthBits`/`NullSentinelValue`/`MaxNodeCount` + layout-option validation). **Codegen DEFERRED into Phase 3**: the current handle is hard-coded `u32` across ~dozens of backend sites + the runtime `PackedStoreState`, so the width is built fresh in the AoS handle, not retrofitted onto the demoted SoA store. | 1 |
| 3 | **Plain recursive `enum` ⇒ AoS-in-arena default** with storeless `new`/`match`; non-recursive ⇒ inline value. Build the AoS node record with the parameterized index-handle ABI (Phase 2 width/sentinel/overflow) from the start. | 0, 1 |
| 4 | **`packed enum` → deprecation error**; keep explicit-store path; map Cohort A to `layout soa`. | 3 |
| 5 | **First-class column scan** (`Expr.column(.field)`) + `soa(sparse)` + the static memory lint + the `-Wperf` SoA hint. SoA becomes a real, discoverable expert path. | 1, 3 |
| 6 | **Cross-region side-table edges** via construction-time provenance inference (the edge is inferred from the argument's region; type stays region-free; functions thread the second region — docs/75 multi-region tail). Auto-narrow index width from a local `reserve_commit(N)`. | 2, 5, docs/75 |

Land 0→4 as the milestone (the beginner default is fast, safe, ceremony-free; `packed enum` retired
cleanly). 5 keeps SoA a first-class expert path. 6 is the tail.

## Non-goals

- No layout selection from non-local usage (only declaration-site `layout`, or a `-Wperf` hint).
- No region or freeze/usage meaning on `layout` (axes stay orthogonal, docs/10).
- No pointer-vs-index dial in ordinary code; no `store`/`columns`/`packed` in the beginner
  surface.
