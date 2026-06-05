# 74 — Region-backed packed enums (drop the bespoke store)

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

Same-region is the default and needs no notation (the four-enum ML AST forest lives in one region,
unified by inference). A genuine cross-region edge annotates the **one field** that crosses, reusing
the struct field-provenance suffix: `Name(symbol: int, ty: TypeExpr @types)`. The checker treats it
as an independent outlives obligation; codegen decodes that field against region-`types` columns and
bare children against the parent's region. The entire escape/outlives layer (`deps(v)` flow,
`regionRefStateFromDependency`, return-escape) is reused unchanged.

## Staging

| Step | Delivers | Risk |
|------|----------|------|
| 1 | Semantic: `new[auto]/new[r] Expr.V(...)` recognized as a packed allocation into the inferred/named region (route packed constructors out of the struct `new[auto]` path); result is a bare `Expr` handle with region provenance. | medium |
| 2 | Backend: lower it to push a row onto the region's per-enum column-stacks (implicit per-region store backed by the region arena to start, reusing the column machinery). | high |
| 3 | `match node:` recovers the store from the handle's provenance (`resolvePackedNodeStoreRoot`), dropping `in store:` and the threaded store param. | medium |
| 4 | Reclaim via `destroy r`/`reset r`/loop-reset; keep the legacy `Expr.Store(arena)` + `in store:` path alive during migration. | medium |
| 5 | Cross-region `@field` edges; `freeze`/publish over region-backed columns. | low |

Land 1→2→3→4 as the milestone (a packed node becomes "just another allocation in a region", store
deleted from every recursive signature). Step 5 is the multi-arena/forest tail.

## Non-goals

- No region parameter on the enum type or handle; no `@r` on variants. Ever.
- No new vocabulary: `@r` provenance, `new[r]`/`new[auto]`, and field-level `@other` all already exist.
- The legacy explicit `Store` path is retained during migration, not removed in step 1.
