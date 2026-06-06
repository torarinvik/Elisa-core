# 74 — Region-backed packed enums (drop the bespoke store)

> **SUPERSEDED AS THE DEFAULT BY [docs/76](76-enum-layout-and-handles.md).** Benchmarks
> (`Code/benchmarks/binary_trees`, `Code/benchmarks/ast_traversal`) showed the columnar SoA store is
> *not* the right default for tree-shaped data: it loses ~2× on memory and ~1.4× on traversal to the
> contiguous AoS-in-arena form, intrinsically. docs/76 makes plain `enum` lower to **AoS-in-arena with
> opaque index handles** by default, and demotes the columnar store to an opt-in `enum … layout soa`
> for whole-store column scans. This doc's *region-backed store mechanics* (the implicit, threaded,
> region-backed `PackedStoreState`; storeless `match`; the implicit-store threading) remain the
> backing for the `layout soa` path and the cross-region forest escape hatch — read it for the
> store/columns machinery, but read docs/76 for the user-facing model and the index-handle ABI.
>
> Status: design + staged implementation. Builds on the orthogonality law (docs/10), store/handle
> unification (docs/69), the region memory model (docs/68), multi-stack regions (docs/71), default
> stack backing (docs/73), and `new[auto]` (inferred-region struct allocation).

## The decision

A `packed enum` is **layout only**. Its storage and lifetime come from the **region** system, not
from a self-owned `Store`. The region annotation is **not** part of the enum type and **not** part
of the handle type — it is supplied at the allocation site (`new[auto]` / `new[r]`) and rides on the
handle's value, exactly like `darray` backings and `new[auto] Box`.

```elisa
packed enum Expr:                       # PURE LAYOUT — no [region r], no @r anywhere
    common:
        span: int                       # dense column on every node — no annotation
    Int(value: int)
    Add(left: Expr, right: Expr)        # children are bare handles, same store

root: Expr = new[auto] Expr.Add(span: 3, left: l, right: r)   # inferred region; or new[r] ... in `region r(...):`

def eval(node: Expr) -> int:
    match node:                         # no `in store:`, no threaded store — store recovered from the handle
        Expr.Int(value): return value + node.span
        Expr.Add(left, right): return node.span + eval(left) + eval(right)
```

`@r` appears only as the **value** suffix (like `darray[T] @r`) when a handle must cross a function
boundary or live in a long-lived struct; never on the type, never on a variant.

## Why — the orthogonality law forbids the alternatives

`docs/10` names three axes — **layout / storage-provenance / usage** — and forbids any one from
secretly carrying another's meaning. `docs/69`: *"packedness is layout, regions/stores are
provenance… none should silently imply the others."* Therefore:

- **`packed enum Expr[region r]` is rejected** — it makes layout (the enum) carry provenance (the
  region). It is also *erased* (`Expr[r]` and `Expr[other]` are the same ABI word; the handle's
  pointer lane already carries the store, docs/24), so it changes nothing at runtime. Shipped code
  (`Code/benchmarks/packed_runtime_ml_expr_repro.elisa`) already declares packed enums with **zero**
  region params and bare recursive children — the bracket would regress against it.
- **`Int@r` / `Add@r` on variants is rejected** — a variant is a column-set *selector*; it owns no
  storage and no region. Per-stack placement of short-lived cohorts is the inference engine's job
  (docs/71 interval coloring), never a source annotation.
- **The struct precedent (`struct Node[region owner]` + `next: Node&? @owner`) does NOT transfer.**
  A `struct` field `next: Node&?` is a raw **pointer** whose region must be named. A packed child
  `left: Expr` is a **row index** that co-locates in the same store's columns by construction — there
  is no dangling pointer to type, so no `@owner` is needed. Index ≠ pointer. (docs/68 also retired the
  `[owner]` bracket form in favor of `@owner`.)

So the handle is bare `Expr`; provenance is inferred and carried by the value.

## How the columns become region-backed

A packed enum is struct-of-arrays: a dense **tags** column, dense **common** columns (one slot/row),
and variant-**sparse** payload columns (slots only for that variant's rows). Under this design those
columns become the **region's per-enum column-stacks**: `new[r] Expr.Add(...)` pushes one tag slot +
one slot per common column + one slot in the Add-payload columns onto region `r`'s stacks;
`destroy r` / `reset r` (or loop-reset, docs/73) reclaims them all by region lifetime — no `Store`
object, no `destroy store`. `reserve_commit` stable column bases (docs/73) keep row-index handles
valid across growth; loop-reset reuse gives a per-iteration tree zero churn.

Orthogonality stays physical, not just nominal:
- **Layout** = the `packed enum` body (region-free, declared once; one ABI serves all regions).
- **Provenance** = the erased region bound at `new[r]`/`new[auto]`, tracked as a `deps(v)` set per
  allocation — never in the type.
- **Usage** = `freeze`/affine, on the value; the handle representation is invariant across
  `Local`/`Frozen` (docs/69), so freezing rewrites nothing.

## Cross-region edges (the rare case)

> **SUPERSEDED by [docs/76](76-enum-layout-and-handles.md) §"Nested side-tables & cross-region
> edges".** The `@types` field annotation below only binds because `types` is a *globally-known store*
> (the ML-AST forest has a distinct program-level `TypeExpr` store). For an *ad-hoc local* region a
> declared `@region` has nowhere to bind — so the general mechanism is **construction-time provenance
> inference** (the edge is inferred from the argument's region; the enum type stays region-free;
> functions thread the second region, not the type). Read docs/76 for the model.

Same-region is the default and needs no notation (the four-enum ML AST forest lives in one region,
unified by inference). A genuine cross-region edge annotates the **one field** that crosses, reusing
the struct field-provenance suffix: `Name(symbol: int, ty: TypeExpr @types)`. The checker treats it
as an independent outlives obligation; codegen decodes that field against region-`types` columns and
bare children against the parent's region. The entire escape/outlives layer (`deps(v)` flow,
`regionRefStateFromDependency`, return-escape) is reused unchanged.

## Revision: the store is made implicit, not eliminated (handle-representation constraint)

The original framing ("drop the bespoke store; the region IS the store; provenance rides on the
handle's pointer lane") assumed a store-carrying handle. The current implementation uses two
columnar ABIs (`packedEnumABIIndexSOA`, `packedEnumABIVariantSparse`) and **both represent a handle
as a bare `u32` row-index** — the store pointer is *not* in the handle. A `u32` index is only
meaningful against one specific `PackedStoreState` (the object holding the column `darray`s), so a
handle returned by `make` is readable in `build`/`eval` only if they share that **same** state
object.

Two ways to honor that:
- **Store-carrying handle** (`{store_ptr, u32}` carrier): handle is self-describing, storeless match
  recovers the store from it, nothing threads. But the handle grows from 4 to ~16 bytes — every
  child edge 4× larger, worse cache behavior — which **defeats the density that is the entire point
  of packed enums**. Rejected on efficiency.
- **Implicit, region-backed, threaded store** (chosen): keep the dense `u32` handle. Keep a
  `PackedStoreState`, but make it *implicit* — created once at the region scope, its columns backed
  by the region arena — and *auto-thread* it across region-polymorphic calls (the same mechanism
  `tree` uses for its hidden store param, and docs/75 uses for the region). The store object lives
  under the hood; the source surface stays ceremony-free: `new[auto] Expr.V(...)` and `match node:`
  with no `Store`, no `in store:`, no threaded param written by hand.

So "region-backed packed enum" means: **the store is implicit and region-backed (lifetime = the
region; backing = the region arena), and threaded by inference** — not that the store object ceases
to exist. The handle stays a dense index; *which* store it indexes is carried by inference
(active-store binding + region-poly threading), never by the handle bits. This is the robust and
efficient reading of the decision and supersedes the "pointer lane" language above.

## Staging

| Step | Delivers | Risk | Status |
|------|----------|------|--------|
| 1 | Semantic: `new[auto] Expr.V(...)` recognized as a packed allocation; result is a bare `Expr` handle with region provenance. | medium | **DONE** |
| 2 | Backend: lower it into an implicit per-region store (PackedStoreState backed by the region arena, created on first `new[auto]`, reusing the column machinery — `getOrCreateRegionPackedStore`). | high | **DONE** |
| 3 | Storeless `match node:` resolves the implicit region store via the active-store binding (semantic gate + backend Path-4), dropping `in store:`. Cross-function: an implicit `__packed_store_E` is threaded by inference (gated to enums actually built with `new[auto]`), so a recursive `make`/`eval` share one store. | medium | **DONE** |
| 4 | Reclaim via the region (the store's columns are arena-backed → freed with the region); legacy `Expr.Store(arena)` + `in store:` path unaffected. | medium | **DONE** (region-reclaim inherited; explicit path untouched) |
| 5 | Cross-region `@field` edges; `freeze`/publish over region-backed columns. | low | TODO |

Steps 1–4 are verified end-to-end: a recursive `make(depth) -> Expr` builds a region-backed tree
with `new[auto]`, a separate `eval(node: Expr)` matches it storelessly, the store is auto-created at
the root and threaded by inference, and the legacy explicit-store packed code (JSON parser, packed ML
AST) is unaffected. Step 5 (multi-region/forest + freeze) is the tail.

Land 1→2→3→4 as the milestone (a packed node becomes "just another allocation in a region", store
deleted from every recursive signature). Step 5 is the multi-arena/forest tail.

## Non-goals

- No region parameter on the enum type or handle; no `@r` on variants. Ever.
- No new vocabulary: `@r` provenance, `new[r]`/`new[auto]`, and field-level `@other` all already exist.
- The legacy explicit `Store` path is retained during migration, not removed in step 1.
