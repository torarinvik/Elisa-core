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

The entire vocabulary is `enum`, the variant constructors, and `match`. No `store`, no `columns`, no
`packed`, no `common:`, no `&?`/`null`, no `new[...]` bracket, no arena:

```elisa
enum Expr:
    span: int                          # shared fields are just fields (was `common:`)
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

- `common:` blocks lower to plain top-level fields.
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
| 0 | **Close region-poly threading gaps** so storeless builders never hit "requires an active in <arena>" — container ops (`darray.push`) and constructors in helper functions thread the caller's region like `new[auto]` already does (docs/75). | docs/75 |
| 1 | **`layout` on enum declarations**: parse `enum … layout soa|aos(...)`, carry a layout mode + `index:` width on the enum type; default (no `layout`) = AoS-in-arena. Reject `layout …[region r]`. | struct `layout` grammar |
| 2 | **Opaque index handle ABI**: index width `u8…u64` (default `u32`, node-index), free null sentinel at each width, loud overflow panic; opaque-handle check (no raw `&` leak). | 1 |
| 3 | **Plain recursive `enum` ⇒ AoS-in-arena default** with storeless `new`/`match`; non-recursive ⇒ inline value. Retire `common:` → fields. | 0, 2 |
| 4 | **`packed enum` → deprecation error**; keep explicit-store path; map Cohort A to `layout soa`. | 3 |
| 5 | **First-class column scan** (`Expr.column(.field)`) + `soa(sparse)` + the static memory lint + the `-Wperf` SoA hint. SoA becomes a real, discoverable expert path. | 1, 3 |
| 6 | Auto-narrow index width from a local `reserve_commit(N)`; cross-region forest `aos(handle: pointer)` / `@field` edges. | 2, 5 |

Land 0→4 as the milestone (the beginner default is fast, safe, ceremony-free; `packed enum` retired
cleanly). 5 keeps SoA a first-class expert path. 6 is the tail.

## Non-goals

- No layout selection from non-local usage (only declaration-site `layout`, or a `-Wperf` hint).
- No region or freeze/usage meaning on `layout` (axes stay orthogonal, docs/10).
- No pointer-vs-index dial in ordinary code; no `store`/`columns`/`packed`/`common:` in the beginner
  surface.
