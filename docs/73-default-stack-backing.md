# 73 — Default stack backing: `reserve_commit`, not `chained`

> Status: design. Builds on multi-stack regions (docs/71), per-stack strategy (docs/72), and the
> region memory model (docs/68). Phases A (auto-reservation), B1 (multi-stack), B2 (early
> reclamation), C (reserve_commit inference), D (aliasing) have landed.

## The change in one line

Make the **default** per-stack backing `reserve_commit` (a contiguous bump stack that grows in
place) instead of `chained` (linked blocks that relocate a growable on growth, leaving a dead
hole). Overflow of a stack's reservation is a **panic**, not a silent fallback. `chained` is
demoted to the explicit/transient case (scratch).

## Why

Today an inferred growable gets `chained` by default (docs/72 selects `reserve_commit` only when a
hard bound is proven *and* an interior ref is taken across a growth). `chained` has two costs that
contradict the memory model's "arenas as stacks, fragmentation isn't allowed" goal:

- **Relocate-on-grow.** A growable that outgrows its buffer allocates a new one; the old buffer is
  a dead hole until region exit. That *is* fragmentation, merely bounded by the region lifetime.
- **Unstable base.** Interior pointers into a chained growable dangle across a growth, so the
  storage-view checker must reject them — a real expressiveness tax.

`reserve_commit` removes both: one contiguous virtual reservation, bump within it, pages commit on
first touch, base never moves.

## Fragmentation becomes structurally impossible (the proof)

A single bump stack can fragment in exactly two ways; the existing discipline closes each, and
`reserve_commit` makes the second one *real* rather than aspirational:

1. **A dead object buried under a live one** (can't pop LIFO). Closed by the multi-stack
   assignment (docs/71): crossing lifetimes land in *different* stacks, so within any one stack
   lifetimes only nest or are disjoint — always LIFO-reclaimable.
2. **A growable relocating, leaving its old buffer dead.** Closed by the tail-growth discipline:
   an unreserved growable is the *sole tail of its own stack*, so it grows by committing the next
   page — in place, no relocation. A pre-reserved growable is fixed-footprint and never moves.
   Under `chained` "grows in place" was a lie (it relocated); under `reserve_commit` it is true.

**Invariant:** every stack holds at most one unreserved growable, as its tail, backed by a
contiguous reservation; all other occupants are fixed-footprint. No layout in which a live hole
can form is representable.

**The one boundary** is the over-split case (>4 growables in one scope, docs/71 cap): they can't
all be tails. That case already **errors** (the interleaved-lifetime error, enriched to name the
fix). So even there, "no fragmentation" is preserved by a compile error, never a silent hole. The
model is closed.

## Free consequence: interior pointers are always stable

With `reserve_commit` as the default, the base of every growable is stable, so interior references
survive growth. The storage-view invalidation checker (docs/72) can treat the default backing as
stable, dropping a whole class of false-positive "view invalidated across growth" errors. Sound:
the base genuinely does not move — on overflow the stack *panics* (dies) rather than relocating.

## The two knobs (implementation, not design)

### Default reservation size

When no bound is inferred (Phase A) and none is written (`reserve(n)`), the tail stack reserves a
**default** virtual range. Too small → spurious overflow panics on legitimately large data; too
big → address-space pressure. On 64-bit this is cheap: reserve generously (GiB-scale `PROT_NONE`),
only touched pages cost physical memory, and regions are mostly sequential so peak reservation
stays low. The default is a tunable constant (start ~256 MiB–1 GiB virtual per unbounded tail
stack) — set empirically.

### Lazy reservation

Today `emitReserveCommitStackInit` reserves **eagerly at region entry**. As the default that would
make every empty/tiny region pay an `mmap` for nothing — and there are thousands of regions. The
default path must reserve **on first allocation** (or commit a small first page eagerly and grow
the commit). The loop-reset reuse (a region in a loop resets, not re-reserves) already amortizes
setup across iterations and must keep doing so.

### Overflow contract

A stack that exhausts its reservation **panics** with a message pointing at the remedy: `reserve(n)`
to size it, or restructure so the growth is bounded. This is the intended performance friction
(docs/70): unbounded growth is possible but must be made explicit.

## Staging

| Step | Delivers | Risk |
|------|----------|------|
| 1 | Default strategy: assign `reserve_commit(default_cap)` to an unreserved growable tail stack with no inferred bound (was `chained`). Analysis-only. | low |
| 2 | Codegen: emit the default-capacity `reserve_commit` init; overflow panic message. | medium |
| 3 | Lazy reservation (reserve on first alloc) so empty/tiny regions cost nothing. | medium |
| 4 | Storage-view checker: treat the default backing as stable (drop false invalidations). | medium (safety) |
| L | Loop-body regions: also default `reserve_commit`, reserved once and reused via `arena_reset` per iteration (lazy makes this trivial — set strategy, reserve on first touch, reset keeps it). | medium |
| 5 | Non-overcommit targets keep `chained`: the default is gated on `ELISA_TARGET_OS_POSIX`. | low |

Steps 1→2→3→4, L, and 5 have LANDED — the default is an in-place `reserve_commit` bump stack
everywhere (function-scope and loop-body) on POSIX, reserved lazily and reused across loop
iterations (20M-iteration hot loop stays at ~0.17 s and 1 MB RSS — no per-iteration mmap). Routing
loop-body `reserve_commit` through the lazy default also closed a latent gap where a bounded
reserve_commit in a loop was marked stable but ran chained — now every reserve_commit the checker
trusts is actually reserve_commit at runtime.

**§5 (portability).** The lazy 256 MiB reservation relies on anonymous-mmap **overcommit** (pages
commit on first touch) — true on POSIX (Linux/macOS/FreeBSD), but NOT on Windows `VirtualAlloc` or
the libc-malloc backend, where the reservation would commit eagerly. So the default flip is gated
on `ELISA_TARGET_OS_POSIX` *in the analyzer* (`targetMmapOvercommit`), which keeps the storage-view
checker and codegen reading the same strategy — sound on every target. On the libc-malloc backend the
growable keeps the prior `chained` default (correct, just without the no-fragmentation guarantee).
32-bit is moot: only 64-bit arches (x86_64, arm64) are targeted.

**Windows commit-on-touch — IMPLEMENTED (was future work).** The Windows `VirtualAlloc` backend now
runs reserve_commit/fixed as a true lazy reservation: `new_region_reserve` takes the range with
`MEM_RESERVE` only (no physical backing), and `arena_region_ensure_committed` commits pages with
`MEM_COMMIT` on demand (in 64 KiB chunks, `WIN_COMMIT_CHUNK`) as the bump pointer advances — the
explicit equivalent of POSIX mmap demand-paging (`arena.elisa`). A `committed` watermark on `Region`
skips redundant commits. So the 256 MiB default reservation costs only address space on Windows too,
and the in-place no-fragmentation default can extend there. NOTE: implemented + reviewed but not yet
run on a Windows host/CI — needs a Windows execution check before relying on it in production.

## Interactions

- **Phase A:** an inferred bound sizes the reservation exactly; the default is only for the
  genuinely-unbounded residual.
- **Phase B1/B2:** unchanged — the multi-stack assignment and early-free still drive which stacks
  exist; this only changes each stack's *backing*.
- **Phase C:** subsumed — `reserve_commit` is no longer a narrow special case but the default;
  the bound-proving logic just refines the reservation size.
- **`scratch`:** becomes the home of `chained`-style block reuse — a thread-local pool of blocks
  for transient allocation (docs note, separate work).

## Non-goals

- No auto-`reserve_commit` *sizing* magic beyond Phase A's bound — unbounded growth uses the
  default reservation and panics on overflow by design.
- No `chained` removal: it remains for `scratch` and for targets where reservation is scarce.
- No change to the over-split cap or the interleaved-lifetime error.
