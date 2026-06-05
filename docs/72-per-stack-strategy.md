# 72 — Per-stack strategy inference (lifetime inference, Phase C)

> Status: design. Builds on multi-stack regions (docs/71) and the region memory model
> (docs/68). Phases A (auto-reservation) and B1 (multi-stack regions) have landed.

## Goal

Choose each inferred stack's arena **backing strategy** automatically, so interior references into
a growing darray stay valid without the programmer writing `using reserve_commit`. Concretely:
a growable stack whose elements have interior references taken across a growth gets the
**relocation-free** `reserve_commit` backing (the base never moves) when a size bound is known;
otherwise it stays `chained` (grows forever by chaining) and the honest "view invalidated" error
remains for the genuinely-unbounded-with-interior-refs case.

## Background: safety is already strategy-driven

The storage-view invalidation checker already keys off the backing strategy
(`analyzer_storage_views.go`, `containerBackingIsStable`):

```
reserve_commit / fixed  -> base never moves -> interior refs survive growth -> no invalidation
chained / heap          -> buffer relocates on growth -> interior ref dangles -> invalidate (error)
```

It reads the strategy from the darray's region (`dt.Region` -> region state -> backing). An
**inferred** darray has `Region == ""`, so it is treated as relocatable (chained) and any interior
ref taken across a growth is rejected. The explicit `region big using reserve_commit:` form already
compiles such code because its region carries the stable backing. Phase C makes the *inference*
select that backing.

## Two obstacles (and why this is not a quick increment)

### 1. Chicken-and-egg ordering

The invalidation error fires **inline during expression analysis** (when a push is analyzed). The
stack assignment and its strategy are decided in `checkRegionLifetimes`, a **post-pass**. So at the
moment the checker would error, the strategy is not yet known. Unlike A/B1 (layout-only,
lifetime-neutral, provably unable to introduce unsafety), Phase C **intercepts a memory-safety
checker** — suppress an invalidation without actually making the backing stable and you have a
use-after-free. The analysis→codegen coupling must be airtight.

### 2. Reservation sizing

`reserve_commit` reserves a fixed contiguous virtual range and **panics on overflow**; `chained`
grows without bound by chaining blocks. So `reserve_commit` is only *safe* with a size bound. The
bounded case (Phase A's inferred bound, or an explicit `reserve(n)`) gives one; the unbounded case
does not, and there is no safe reservation size for it.

Note the bounded case is also where the checker is currently **over-conservative**: `reserve(n)` +
a fill that stays within `n` never relocates under chained, yet the checker still invalidates
(it cannot prove "pushes stay within capacity" statically). Phase C fixes that false positive by
giving such a darray a `reserve_commit(n)` backing whose stability the checker already trusts.

## Design: deferred-error resolution

Resolve the ordering with a **deferred invalidation** flow instead of erroring inline:

1. **Collect, don't error (analysis).** When an interior ref is taken into an inferred-region
   darray and a later growth would invalidate it, record a *pending invalidation* keyed by the
   darray (and the borrow site) instead of emitting the error immediately. Also record the simple
   fact "an interior ref is taken into darray X" (already available via
   `storageViewDependencyForBorrowedPlace`).

2. **Decide strategy (post-pass, in `checkRegionLifetimes`).** Extend `RegionStackAssignment` with a
   per-stack `Strategy`. For a growable own-stack whose darray has a pending invalidation:
   - if a **size bound** is known (Phase A's inferred bound or an explicit `reserve(n)`) ->
     `Strategy = reserve_commit`, reservation = that bound (rounded up); clear the pending
     invalidation (sound: the backing is now stable).
   - else (unbounded) -> keep `Strategy = chained` and **emit** the pending invalidation error
     (the honest "this needs an explicit `reserve_commit` region or a bound" signal).

3. **Honor it (codegen, B1b extended).** When emitting a stack's arena, use its `Strategy`: a
   `reserve_commit` stack is created with `emitRegionInit(reserve_commit, capacity)` instead of the
   lazy zero-init. `containerBackingIsStable` must report the inferred darray as stable — so the
   inferred darray's effective backing has to be visible to the checker (via the assignment), which
   is exactly what the post-pass computes.

**Soundness invariant:** a pending invalidation is cleared *only* when the post-pass has assigned a
`reserve_commit`/`fixed` strategy to that darray's stack, and codegen is driven by the same
assignment. The error is suppressed iff the backing is actually stable. No third path.

## Strategy rule (summary)

```
own-stack growable, interior ref taken across growth, bound known   -> reserve_commit(bound)
own-stack growable, interior ref taken across growth, no bound      -> chained + keep the error
own-stack growable, no interior ref                                 -> chained (default; cheapest)
shared/fixed stack                                                  -> chained (no growth)
```

`fixed` (single block, no growth, panic on overflow) is a future refinement for stacks proven to
never grow past their reserved size.

## Staging

| Step | Delivers | Risk |
|------|----------|------|
| C1a  | `Strategy` field on `RegionStackAssignment` + post-pass rule (bounded case only) | medium |
| C1b  | Deferred-invalidation flow (collect inline, resolve in post-pass)                | high (safety) |
| C1c  | Codegen: emit `reserve_commit` stacks from the assignment                        | medium |
| C2   | Unbounded residual: keep the error, point at explicit `reserve_commit`           | low   |

C1a→C1b→C1c is the milestone (bounded interior-ref-across-growth Just Works). C2 is the honest
fallback for the unbounded case.

## Reservation sizing + panic contract

- Bounded case: reservation = the known bound (rounded up to a block/page multiple). No overflow
  possible within the proven bound, so no panic.
- A `reserve_commit` reservation is virtual; pages commit on first touch, so an over-estimate costs
  address space, not physical memory.
- The unbounded case is never auto-`reserve_commit`'d (no safe size) — it keeps the error, so the
  panic-on-overflow path is never reached by inference.

## Interactions

- **Phase A:** supplies the size bound that makes `reserve_commit` safe for bounded fills, and
  removes the current false-positive invalidation on `reserve(n)` + bounded fill.
- **Phase B1:** supplies the per-stack arenas C drives; `Strategy` rides on the existing
  `RegionStackAssignment` and the B1b codegen routing.
- **Phase D:** unaffected; cross-stack aliasing is orthogonal to per-stack strategy.

## Non-goals

- No auto-`reserve_commit` for unbounded growth (no safe size) — that stays an explicit-region
  decision.
- No `fixed`-backing inference in C1 (a C-follow-on).
- No attempt to statically prove "pushes stay within capacity" for chained darrays; the bound is
  carried as a `reserve_commit` reservation instead.
