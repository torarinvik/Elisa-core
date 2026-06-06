# 75 — Inferred region-polymorphic functions

> Status: design + staged implementation. Builds on the region memory model (docs/68), multi-stack
> regions (docs/71), default stack backing (docs/73), `new[auto]` (inferred-region struct
> allocation), and region-backed packed enums (docs/74). This is the cross-function half of `new[auto]`:
> it lets a function *return* a region-allocated value, and recursion build one.

## The problem

`new[auto]` places an allocation into the function's own innermost inferred region. That region dies
when the function returns, so a function that **returns** what it allocated is rejected:

```elisa
def make(depth: i64) -> Expr:
    if depth <= 0:
        return new[auto] Expr.Int(span: 0, value: 1)
    return new[auto] Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))
#   error: cannot return value: region dependency facts include local region "__auto_259801"
```

Two failures in one: (a) the returned handle dangles (its region is `make`'s, freed on return), and
(b) even if it didn't, each recursive call allocates into a *different* region, so a parent `Add`'s
children live in already-dead regions. The tree fragments one-region-per-call.

The fix is the same shape Rust reaches for with `fn make<'a>(...) -> Expr<'a>` and that `tree`
already does with its hidden store param: the function must allocate into the **caller's** region,
not its own. The region becomes a parameter. The decision here is to make that parameter **inferred**,
not spelled.

## The decision

A function that returns a region-allocated value (a `new[auto]` handle, or a value transitively built
from one) is **region-polymorphic**: it is implicitly parameterized over the region its result lives
in, exactly as it is already implicitly parameterized over nothing-you-write for ordinary type
inference. The region parameter is:

- **Inferred, not declared.** No `def make[region r]`. The signature stays `def make(depth) -> Expr`.
- **Threaded as a hidden parameter.** The caller passes its ambient region; `new[auto]` inside the
  callee resolves to that region; the returned handle's provenance is the caller's region.
- **Bound at the call site to the region the result flows into.** `root: Expr = make(21)` inside
  `in auto:` binds the hidden param to that `auto` region; the whole tree lands in one region.
- **Pinnable with `@r` when ambiguous.** The two-tier story (inferred default, `@r` to pin) extends
  here: `def make(depth) -> Expr @r` names the region when the callee juggles more than one and
  inference can't pick.

```elisa
packed enum Expr:                       # pure layout (docs/74)
    common:
        span: int
    Int(value: int)
    Add(left: Expr, right: Expr)

def make(depth: i64) -> Expr:           # region-polymorphic: hidden region param, inferred
    if depth <= 0:
        return new[auto] Expr.Int(span: 0, value: 1)
    return new[auto] Expr.Add(span: 0, left: make(depth - 1), right: make(depth - 1))

def build():
    in auto:                            # the tree's region
        root: Expr = make(21)           # hidden param ← this region; all nodes co-located here
        # ... match root, walk, etc. ...
```

This is `new[auto]`'s natural completion: `new[auto]` is "allocate into the region I'm in";
region-polymorphism is "the region I'm in can be my caller's."

## Why inferred, not explicit `[region r]`

The two-tier inference story already governs every other allocation axis; region-polymorphic
functions are the same table, one row down:

| | inferred (default) | pinned (when ambiguous) |
|---|---|---|
| container backing | `darray[T] = []` | `darray[T] @r = []` |
| struct alloc | `new[auto] Box(...)` | `new[r] Box(...)` |
| **region-poly fn** | `def make(...) -> Expr` | `def make(...) -> Expr @r` |

Going explicit-only here (the `def make[region r]` form) would be the one inconsistent axis in an
otherwise inference-first language — you don't write `make<T>` for a type-generic call, so you
shouldn't write `make[region r]` for a region-generic one. The region, like the type, is recovered
from the call site.

The honest cost (the reason Rust went explicit): cross-function region inference can be ambiguous,
and a wrong/surprising inferred region yields a confusing error. The mitigation is the `@r` escape
hatch — when the callee allocates into more than one candidate region and inference can't decide, it
is a **hard error that asks for the `@r` annotation**, never a silent guess. Inference is total or it
asks; it never picks wrong quietly.

## Inference rules (sketch)

A function `f` acquires a hidden region parameter `ρ` iff its result's `deps` set (the existing
`regionRefStateFromDependency` flow) includes a region that is **local to `f` and reachable from a
returned value**. Concretely:

1. **Detect.** During the region-provenance pass, if a `return` expression's `deps` include a local
   inferred region (`__auto_*`) — i.e. exactly the case that today raises "cannot return value:
   region dependency facts include local region" — mark `f` region-polymorphic over `ρ` instead of
   erroring, and rewrite those local-region deps to `ρ`.
2. **Unify.** All returned values must share one region. If two returns carry deps from two distinct
   local regions that can't be unified (they're used in non-overlapping ways), that's the ambiguous
   case → demand `-> Expr @r` (and a second `@s`, etc., for genuinely multi-region returns).
3. **Bind at call.** A call `make(...)` whose result flows into region `R` (assignment into an `R`
   binding, an argument position annotated `@R`, or another region-poly call already bound to `R`)
   binds `ρ := R`. The ambient `in R:` is the default `R`. If the result flows nowhere region-bound
   (e.g. a bare temporary), `ρ` binds to the caller's innermost inferred region — the same region
   `new[auto]` would have chosen in the caller.
4. **Thread.** Lower `ρ` to a hidden leading parameter (an `Arena&`, reusing the `tree` store-param
   plumbing and `treeAllocOwner`), passed at every call site, consumed by `new[auto]` in the body.
5. **Escape-check across `ρ`.** A returned `ρ`-value is sound because `ρ` outlives the call by
   construction (it's the caller's region). A value whose deps include *both* `ρ` and a still-local
   region is rejected as today. `freeze`/affine ride on the value unchanged (docs/69).

Recursion falls out for free: `make` calls `make` with the same `ρ` (the body is already inside
`ρ`), so every node in the recursion shares one region and the tree is coherent.

## Relationship to `tree`

`tree X:` already does (1)–(4) for a hidden **store** param (`__tree_store_X`, auto-threaded through
every recursive call, recovered in `match`). This design generalizes that machinery from "implicit
store" to "implicit region" so it works for any region-polymorphic function — packed enums (docs/74),
`new[auto]` structs, and container-returning helpers alike — without the `tree` keyword. `tree` then
becomes sugar expressible in this lower layer rather than a bespoke mechanism.

## Staging

| Step | Delivers | Risk | Status |
|------|----------|------|--------|
| 1 | Detect: a value-returning path whose result carries a synthesized `__auto_*` region marks `f` `RegionPolymorphic` on its `FuncType`. | medium | **DONE** |
| 2+3 | Pre-pass `classifyRegionPolymorphicFunctions` (fixpoint, before any body is analyzed) injects a hidden `__region_auto` Arena& param; call sites thread the caller's region (recursive → own `__region_auto`, root → active `in auto:`); the body's synthesized `__auto_*` region ADOPTS the threaded arena (no per-call arena, no premature free); escape error suppressed for region-poly fns. | high | **DONE** — verified end-to-end on a depth-100 recursive struct builder |
| 4 | Packed-enum payoff: the docs/74 recursive `make` compiles and runs — region-backed `new[auto] Expr.V` + storeless `match`, store auto-threaded across `make`/`eval`. | medium | **DONE** — verified: make(10) builds a 1024-leaf tree, eval sums to 1024, zero ceremony |
| 5 | Explicit `-> T @r` pin form for multi-region returns; ambiguous-multi-region detection; `tree` reframed as sugar over this layer. | low | TODO |

Steps 1-3 are the milestone for *any* region-polymorphic value (structs work today). Step 4 — the
recursive packed-enum tree (the binary-trees benchmark) — needs the docs/74 region-backed packed
store (alloc into region column-stacks + `match` recovering the store from the handle). The two
features compose: docs/75 threads the region; docs/74 lowers a packed node into it.

## What is verified (structs) vs pending (packed enums)

`new[auto] Box(...)` (a struct) lowers via the region arena and threads correctly — a recursive
builder runs and every node lives in the one caller-threaded region. `new[auto] Expr.V(...)` (a
packed enum) currently type-checks but does not codegen: it is still misroutable to the struct alloc
path and there is no per-region packed store to push columns onto. That is docs/74 step 2; storeless
`match node:` is docs/74 step 3. Both are required before the recursive packed tree builds and the
binary-trees benchmark can run.

## Non-goals

- No `[region r]` parameter syntax on functions. The region is inferred; `@r` pins only when
  inference is ambiguous, and ambiguity is an error-with-a-fix, never a silent guess.
- No change to the enum/handle types (docs/74): the region rides on the value, never the type.
- No reuse vocabulary beyond what exists: `@r`, `new[auto]`/`new[r]`, and the `tree` store-threading
  plumbing already cover it.
