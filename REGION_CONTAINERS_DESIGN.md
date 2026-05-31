# Region-Parameterized Containers — Design

**Goal.** Collapse to *one* heap-container type (`darray[T]`, `dstr`, `dict`, …)
whose **region lives in the type**, with the backing arena threaded **by the
compiler** — never by the programmer and never via a runtime allocator field.
Result: no `in arena:` threading, no `trusted out[0].ref[static u8&]` surface,
no `darray` vs `DArrayBuilder` split, zero runtime overhead, and static escape
safety. Backwards-incompatible by design.

## Implementation status (2026-05-31)
The headline vision below — *region as a parameter in the container type* with the
arena threaded across the ABI (Phases 1–3) — is still the design ahead, not yet built.
What **is** implemented and verified so far, from the memory model:

- **Backing-allocator strategies (memory model §4).** `region r(cap) using malloc:`
  backs a region's arena blocks with libc malloc instead of the compile-time default
  (mmap on POSIX); the default path is behavior-preserving. The allocator is selected
  per-region at runtime via an `Arena.backend` tag.
- **Pool / free-list reclamation (memory model §3, third bullet).** `RegionPool[T]`
  + the affine `Pooled[T]` handle (`heap.elisa`): a region-anchored object pool whose
  same-size slots are recycled through a free list and are **pointer-stable** (never
  move; FFI-safe). `release` is pure slot reuse, never an obligation — a dropped handle
  is safe because the region bulk-frees the slab at teardown. Idiom: `pool.acquire()` /
  `pool.release(move h)` (see the "Canonical usage" doc in `heap.elisa`).
- **Object-granularity invalidation gating (memory model §5) for pool borrows.** A raw
  interior borrow taken out of a `Pooled` handle (`b = h.ptr`) and used after `release`
  is a **compile-time** use-after-free error — including when the borrow is stashed in a
  struct/aggregate field or laundered through a function that returns it (the three
  escape vectors). This composes the affine-consume checker with the borrowed-owner-ref
  alias machinery; scoped precisely to the pool handle (`Thread`/`Task`/`MutexGuard`,
  whose interior pointers reference externally-owned storage, are intentionally exempt).

**Safety verified.** An adversarial battery confirms the borrow checker, lifetimes, and
memory safety behave: rejected — unconsumed `linear`, affine double-move/double-release,
return-of-local-ref ("dangles"), local-arena collection returned or stored into a
non-local field ("use-after-free"), nested-region escape at var-decl init ("outlives"),
darray interior borrow used after a reallocating `push`, pool interior borrow used after
release (in-function / struct-stored / cross-function), and use of a `= zeroed` value
whose zero is invalid (a non-optional ref); accepted — affine drop, use within the
region, `= zeroed` reads where zero is a valid value, and assign-before-read. No holes
found. (Regression coverage lives in `semantic/*_test.go`: `linear_affine`,
`return_borrow_escape`, `darray_region_escape`, `darray_interior_borrow`, `region_pool`,
etc.)

The pool/allocator pieces are the reclamation + strategy layers of the model; the
container-type region-parameterization and its ABI threading (Phases 1–3) remain the
larger, still-unbuilt body of this design.

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

## Memory model (agreed)
Decisions that constrain the whole design:

1. **Address stability is a core invariant.** A pointer/`@r` reference into a
   region stays valid for the region's entire lifetime. Objects are **never
   silently moved.** This is what makes raw `@r` pointers sound, keeps FFI/guest
   interop working, and lets the checker reason about lifetimes only (never moves).
2. **No automatic compaction.** Moving a live object changes its address and
   dangles every pointer to it; doing it safely requires a precise *moving
   garbage collector* (root tracking / barriers / handle rewriting). That is the
   opposite of an arena and incompatible with raw pointers — so it is **not** the
   default and not built first. ("Copy the survivor to the bottom and bump the
   region pointer" = a one-object copying collector; rejected for pointer regions.)
3. **Reclaiming dead objects mid-lifetime** (the "3 allocated, 1 survives" case)
   is done *without moving*, in this preference order:
   - **nested / child regions** — short-lived objects in a sub-region freed when
     they die; the survivor lives in the parent. (The idiomatic arena answer:
     lifetime structure via nesting, not compaction.)
   - **`mark` / `reset-to-mark`** — LIFO savepoint + rollback of the bump pointer
     (Elisa already has region marks). O(1), no moving.
   - **pool / free-list allocator** — per-object reuse of same-size slots; slots
     don't move.
   - **explicit promote-copy** — programmer copies a survivor to a longer-lived
     region before reset. Explicit beats hidden moving.
4. **Allocator is an interface; region = lifetime + backing allocator.** Separate
   the *lifetime model* (static, allocator-agnostic: `@r` shares/can't-outlive
   `r`) from the *allocation strategy* (runtime, pluggable: bump default, pool,
   malloc-backed). Strategy decides reclamation granularity; lifetime semantics
   are uniform. Ship bump → pool → malloc-backed.
5. **Invalidating operations are statically gated.** `destroy` / `reset` /
   `reset-to-mark` are legal only when no live borrow points into the invalidated
   range — the same no-live-borrows check, at bucket granularity. This is the
   "...unless it has some mechanism" intuition made concrete: it's the static
   check, not a runtime guard.
6. **Compaction, if ever, is a separate opt-in flavor.** A *handle-based* region
   (`Handle[T]` indexes a table the allocator owns) can compact by rewriting the
   table — but gives up raw interior `@r` pointers (no FFI of interior pointers).
   Two flavors: **pointer-stable regions** (default, FFI-safe, never move) vs
   **handle regions** (opt-in, movable). Deferred; never the default.

## Mental model: Rust lifetimes, but the unit is the region (not the object)
The goal is Rust-grade static memory safety, with the **region/bucket** as the
lifetime, not the individual RAII object. The correspondence:

| Rust | Elisa regions |
|---|---|
| lifetime `'a` | region `r` (a named `in r:` scope / `'heap`) |
| `&'a T` | `T& @r`, `darray[T] @r`, … (value tagged with its region) |
| `fn f<'a>(x: &'a T)` | `def f[r](x: darray[T] @r&)` (region param) |
| `'a: 'b` (outlives) | region `r` outlives region `s` (lexical nesting / `'heap` outlives all) |
| borrow checker / NLL | region-escape checker (`@r` value may not outlive `r`) |
| `Drop` per object | **no per-object drop** — the whole bucket is freed at region end |

**Why region-granularity is *easier* than Rust NLL** (this shapes the build):
- The lifetime lattice is the set of **live regions** — few, and lexically
  nested via `in` scopes — not one lifetime per borrow. Far smaller than NLL's
  per-statement region inference.
- **No per-object drop / drop-order / reborrow-splitting.** A region is one
  bump-allocated bucket freed atomically at scope end (`destroy`/block exit).
  Most of Rust's borrow-checker subtlety (drop glue, partial moves, two-phase
  borrows) simply doesn't arise — there's nothing to drop mid-region.
- Aliasing within a region is fine (no XOR-mutability requirement for *liveness*
  safety; that's a separate concern Elisa already handles via ref mutability).

**What we still must get Rust-right (the real work):**
1. **Escape.** A `@r` value must not outlive `r` — via return, store into a
   longer-lived object, or capture. This is THE safety property. Largely the
   existing ref-escape machinery, generalized to containers and to region params.
2. **Outlives lattice.** `'heap` outlives every local region; an outer `in`
   scope outlives an inner one; a region param's bound is its caller's region.
   Start invariant (exact match), add `outlives` only where forced.
3. **Variance.** `darray[T] @r` should be covariant in `r` (a longer-lived region
   usable where a shorter is expected) and behave like `&'a` in elem variance.
   Decide variance explicitly per type constructor.
4. **Region-in-struct / nested containers.** `darray[darray[T] @r] @s` requires
   `r` outlives `s`; a struct holding `@r` fields is itself `@r`-bounded. Needs a
   well-id rule for "the region(s) a type is parameterized by."
5. **Elision.** Like Rust lifetime elision: infer region params at boundaries so
   they're almost never written; `@r` is surface only where inference can't.

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

## IMPORTANT: Phase 2 and Phase 3 are coupled (verified in codegen)
`emitBuiltinDArrayPushCall` (src/backend) resolves the arena via
`lookupTreeAllocOwner()` — i.e. the **ambient `in arena:` scope** — and errors
`darray push requires an active in <arena>: scope` if there is none. The runtime
darray header does **not** store its arena. Therefore the static check is
load-bearing *in lockstep with codegen*: relaxing it (allow push through a
borrow with no ambient scope) without changing codegen would compile and then
fail/UB at the backend. **The check-relax (Phase 2) and the arena-sourcing
(Phase 3) MUST land together.** Phase 1 (carry the region) remains a safe,
independent prerequisite, already done.

## Phase 2+3 calling convention (spec to implement)
The single mechanism: **a region is an implicit `Arena&` capability parameter.**

Codegen / runtime env:
- Maintain a codegen **region environment**: `region-name -> arena ref`. Today
  `lookupTreeAllocOwner()` gives exactly one entry (the ambient scope). Generalize
  it to a map populated from (a) `in <arena/region>:` scopes and (b) **hidden
  arena parameters**.
- `da.push(x)` for `da : darray[T] @ r` lowers to ensure-capacity against
  `regionEnv[r].arenaRef` — not `lookupTreeAllocOwner()`. If `r` is the ambient
  scope, behavior is identical to today (backwards path preserved).

ABI:
- A function gains **one hidden `Arena&` param per distinct region** it needs an
  allocator for — i.e. every region that (a) appears in a `@r` container param it
  mutates/grows, or (b) it allocates into and is not a local `in` scope. Order:
  appended after explicit params, sorted by region-param introduction order.
- Functions with no such regions get **no** hidden params (ABI unchanged — most
  functions, so the blast radius is bounded to allocator-touching APIs).
- The `'heap` (process-lifetime) region's arena is a known global, never a hidden
  param.

Semantic side:
- Region vars: add `RegionParam` to container types; a `darray[T] @ r` *parameter*
  introduces region var `r`. Calls unify `r` with the argument's region
  (`collectTypeBindings`/`substituteType` gain a region axis, mirroring shape/
  storage params).
- The append/extend/reserve/dict checks change from "ambient arena active" to
  "the container's region is **available** in the current region env" (an ambient
  `in` scope OR an in-scope hidden region param).
- Escape stays as today's ref machinery: `@r` value can't outlive `r`.

Open decisions (resolve before coding):
1. **Region subtyping/outlives** — start invariant (exact match); add `'heap`-
   outlives-local only where forced.
2. **Multi-region functions** — N hidden params; keep region vars distinct in
   unification.
3. **Return values** — a returned `@r` container forces `r` to be a caller-
   provided region (can't be a local `in` scope); enforce via the escape check.
4. **Generic + region interaction** — region params compose with `[T]` generics;
   ensure specialization keys include region bindings.

Suggested execution order (one coupled change, but staged internally):
a. Semantic: `RegionParam` + region unification in calls (types only, still
   inert at codegen — keep ambient sourcing). Suite stays green.
b. Codegen: generalize `lookupTreeAllocOwner` to a region env; add hidden-arena
   param emission + call-site passing for region-param functions.
c. Flip the checks to region-availability; route push/extend/etc. through
   `regionEnv[r]`.
d. Sweep: delete `DArrayBuilder`; add safe `darray@r -> cstr/sview@r`.

## Codegen implementation plan (grounded in the backend)
Traced facts:
- `functionState.treeAllocOwner` holds the *single* ambient arena (`arenaRef`),
  saved/restored at `in`-scope boundaries.
- **`functionState.regions []regionBinding{name, ptr, typ}` already IS a region
  environment** — `region NAME(...)` registers `{name, ptr=arena alloca}`. This
  is the map we need; it just isn't consulted by container ops.
- `emitBuiltinDArrayPushCall` resolves the arena via `lookupTreeAllocOwner()`
  (ambient) and errors with no active scope. Same for extend/reserve/dict.

Steps (must land together — not independently green):
1. **Region-aware sourcing.** In the push/extend/reserve/dict codegen, if
   `darrayType.Region` names an entry in `s.regions`, source the arena from that
   entry's `ptr` instead of `lookupTreeAllocOwner()`. (Behavior-identical today,
   since a container's region == its ambient scope; becomes load-bearing once
   helpers push through a borrow.)
2. **Hidden-arena params.** For a function with `RegionParams`, append one hidden
   `Arena&` param per region param (deterministic order). Register each into
   `s.regions{name: regionParam, ptr: hiddenParamValue}` at function entry.
3. **Call-site passing.** At a call to a region-param function, for each region
   param look up the bound region (from `regionBindings` produced by
   `collectTypeBindings`) and pass that region's arena `ptr` from the caller's
   `s.regions`. Indirect/function-value/extern calls need the same ABI or a ban.
4. **Flip the checks.** Replace the semantic `requires an active in <arena>:
   scope` checks (analyzer_expr_builtin_collections.go ×N, dict, comprehension)
   with region-availability: allowed if the container's region is an ambient `in`
   scope OR an in-scope region param. Backend mirrors via `s.regions`.
5. **Escape + cleanup.** Generalize ref escape to container `@r`; delete
   `DArrayBuilder`; add safe `darray@r -> cstr/sview@r`.

⚠️ Risk note: steps 1–3 mis-wired = **wrong arena at runtime = memory
corruption**, the worst failure class. This is why it must be executed with a
fresh, careful pass and heavy runtime testing (every container op through a
region param, multi-region functions, nested regions), not rushed.

## Recommendation
Phases 1–2 are tractable type-system/check work and deliver most of the
ergonomic win (no threading; region in the type) while leaving the runtime ABI
untouched. **Phase 3 is the genuinely hard, research-flavored piece** (region as
a capability parameter threaded across the ABI) and deserves its own focused
build + a small spec of the calling convention before coding. Do 1→2 first,
prove it on `verbatim_port`, then commit to 3.
