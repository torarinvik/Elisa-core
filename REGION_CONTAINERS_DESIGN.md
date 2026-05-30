# Region-Parameterized Containers — Design

**Goal.** Collapse to *one* heap-container type (`darray[T]`, `dstr`, `dict`, …)
whose **region lives in the type**, with the backing arena threaded **by the
compiler** — never by the programmer and never via a runtime allocator field.
Result: no `in arena:` threading, no `trusted out[0].ref[static u8&]` surface,
no `darray` vs `DArrayBuilder` split, zero runtime overhead, and static escape
safety. Backwards-incompatible by design.

## What we build on (today's substrate)
- **Regions are already named strings.** `RefType{ … Region string }`. Refs are
  region-tracked, with region-assignability checks (`refRegionAssignable`). So
  region polymorphism for refs effectively exists.
- **`in arena:` already establishes an ambient region/arena**
  (`currentTreeAllocOwner{Kind: Arena|Region|Perm|Store}`), and container ops
  gate on it (`darray push requires an active in <arena>: scope`).
- **The compiler already threads an arena into allocation.** `v.push(x)` lowers
  to `arena_da_append(arena, v, x)`; the runtime append takes the arena as a
  normal argument. The *mechanism* for implicit arena passing exists — it's just
  keyed off the **ambient scope**, not off the **container's region**. This is
  the single most important fact: we are re-pointing an existing wire, not
  inventing one.
- **Containers lack a region.** `DArrayType{Elem, Shape, SurfaceName}` — no
  `Region`. `DStrType`, `DictType`, `ViewType` likewise. This is the gap.
- **`DArrayBuilder` already stores `owner: Arena&`** — i.e. option (2), the
  runtime-field approach. We are deliberately *not* generalizing that; we fold
  its capability into plain `darray` via the type-level region instead.

## The model
A **region** is a *static* identity (a name, like a lifetime). Its **backing
arena** is an *implicit capability* the compiler supplies wherever allocation in
that region happens — exactly how `can Memory.Allocate` effects are already
threaded. The programmer's only obligations: open a region (`in r:`) and respect
that `@r` values can't outlive `r`. Everything else is inferred/threaded.

### 1. Region as an associated parameter on containers
Add `Region string` (+ a `RegionParam string` for polymorphism, mirroring
`StateParam`/`StorageParam`) to `DArrayType`, `DStrType`, `DictType`,
`ViewType`, `DArrayViewType`, store types.
- Surface stays `darray[T]` (region inferred). Explicit form `darray[T] @ r`
  only at boundaries that need it.
- Default region = the enclosing `in` scope's region; top level / globals = a
  distinguished `'heap` (process-lifetime) region.

### 2. Region inference
- A literal/empty container created inside `in r:` gets `darray[T] @ r`.
- Region variables unify like the existing shape/storage params. Add a
  `RegionParam` that participates in `collectTypeBindings` / `substituteType`.
- Functions become region-polymorphic by inference:
  `def push_hex(out: mutable darray[u8] @ r, …)` — `r` inferred from the call.
  Most signatures need *no* written region; it rides along like shape does.

### 3. Implicit allocator threading (the crux)
Maintain a compile-time **region environment**: `region-name → arena value`
(SSA value in codegen / a hidden parameter at function boundaries).
- `v.push(x)` for `v: darray[T] @ r` lowers to `arena_da_append(arena_of(r), v, x)`
  where `arena_of(r)` is resolved from the region env — **not** from ambient
  scope, **not** from a stored field.
- A region-polymorphic function receives the arenas for its region params as
  **hidden capability parameters**, inserted at call sites by the compiler
  (same shape as effect-capability passing). `push_hex(out @ r, …)` gets
  `arena_for_r` for free; the caller, which *has* `r` live, supplies it.
- `in r:` binds `r → <that arena>` in the env for the lexical scope.

This is the whole game: **regions are implicit capability parameters; the
compiler sources the arena from the region env.** The `arena_da_append(a, …)`
runtime ABI is unchanged — only *where the compiler gets `a`* changes.

### 4. Liveness & escape (safety, mostly reused)
- `darray[T] @ r` may not outlive `r` — identical rule to `T& @ r`; reuse the
  ref region-escape machinery (`refRegionAssignable`, region provenance).
  `return (darray @ local)` is an escape error.
- `.push` is legal iff `r` is live at that point — provable from holding an
  `@r` value whose region is in scope or a region param. **The
  `requires an active in <arena>: scope` check is replaced by region-liveness.**
- **Region outlives / subtyping:** a longer-lived region is usable where a
  shorter is expected (`'heap` outlives any local `r`). This is the subtle part
  (covariance of region, à la lifetime subtyping). Start *invariant* (exact
  region match) and add `outlives` subtyping only where needed.

### 5. Kill the unsafe C-string idiom
`out[0].ref[static u8&]` exists only because there was no safe "darray → string
pointer that stays in the region" op. Add a first-class
`darray[u8] @ r → cstr @ r` (and `→ sview`) conversion the compiler proves stays
within `r`. Removes a whole class of `trusted Unsafe.PointerCast/UncheckedIndex`.

### 6. Collapse `DArrayBuilder`
Its only reason to exist was carrying the allocator. `darray[T] @ r` now does
that statically. Remove it; migrate uses to plain `darray`. (`InlineVec` is
about *inline storage*, orthogonal — keep, but it too gets a region for its
spill arena.)

## Honest hard parts
1. **Region polymorphism + inference.** Region vars, unification, and especially
   **region subtyping/outlives** — this is Rust-lifetime territory and where the
   real subtlety lives. Mitigation: ship invariant-region first; add outlives
   incrementally.
2. **Codegen ABI for hidden arena params.** How region-arenas are passed across
   function boundaries (one hidden ptr per region param). Needs a clear ABI and
   interplay with the existing effect-capability passing.
3. **Multi-region functions.** A function allocating into two regions takes two
   arena params; inference must keep them distinct.
4. **Global / `'heap` region.** A real process-lifetime arena (or malloc-backed)
   for top-level and escaping allocations; its arena is always "in env".
5. **Interaction with existing region features** — `regionRefState`,
   region provenance, owned/`move`d regions, `destroy`. Region-in-container must
   compose with these, not duplicate them.

## Phasing (each phase compiles + passes the suite)
1. **Types:** add `Region`/`RegionParam` to container types; infer region from
   the `in` scope at creation. Keep ambient-arena codegen (region is
   carried but not yet *used* to source the arena). Low risk, type-only.
2. **Checks:** replace the `requires in <arena> scope` append/extend/reserve/
   dict checks with **region-liveness**. Allow `push` through any `@r` whose
   region is live; source the arena from the env (ambient where unambiguous).
3. **Threading:** region environment in codegen + hidden arena params for
   region-polymorphic functions. This is the hard ABI phase.
4. **Escape:** wire container `@r` into the existing ref escape/outlives checks;
   add region subtyping (`'heap` outlives) as needed.
5. **Cleanup:** safe `darray @ r → cstr/sview @ r`; collapse `DArrayBuilder`;
   sweep the codebase (verbatim_port becomes the canary).

## Recommendation
Phases 1–2 are tractable type-system/check work and deliver most of the
ergonomic win (no threading; region in the type) while leaving the runtime ABI
untouched. **Phase 3 is the genuinely hard, research-flavored piece** (region as
a capability parameter threaded across the ABI) and deserves its own focused
build + a small spec of the calling convention before coding. Do 1→2 first,
prove it on `verbatim_port`, then commit to 3.
