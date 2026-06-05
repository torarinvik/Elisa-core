# 71 — Multi-stack regions (lifetime inference, Phase B)

> Status: design. Builds on the region memory model (docs/68) and performance-friction
> design (docs/70). Phase A (auto-reservation) has landed; this document specifies Phase B
> (multiple bump-stacks per lifetime) and sketches C/D.

## Motivation

Region inference today wraps a scope in **one** arena (`__auto_<offset>`). One arena is a single
bump-stack, so it can only reclaim objects in LIFO order and can only grow its **tail**
allocation. Two consequences leak to the user:

- **Interleaved lifetimes are a hard error.** `a` born, `b` born, `a` dies, `b` dies — `a` is
  buried under `b`, so the stack cannot pop `a` early. Today: a compile error telling the user to
  add an explicit region.
- **Interleaved growth bloats or panics.** Growing `a` after `b` sits on top relocates `a`
  (a dead hole under CHAINED, a panic under RESERVE_COMMIT). Today: the tail-growth warning
  (committed) flags it, but the only fixes are manual (reserve / reorder / explicit region).

Both are symptoms of *one stack per lifetime*. The fix is to let a lifetime own **several**
parallel stacks.

## The model

A lifetime (one inferred region) lowers to **K parallel bump-stacks** — K arenas, all opened at
region entry and all freed together at region exit. Allocations are partitioned across the K
stacks. Freeing remains O(stacks), not O(objects); there is no tracing and no per-object free.

```
inferred region __auto_N            (one lifetime, freed together)
 ├─ stack 0  [ fixed / reserved objects … ]          packed densely
 ├─ stack 1  [ growable A …  (tail — grows freely) ]
 └─ stack 2  [ growable B …  (tail — grows freely) ]
```

Generational-GC intuition without a GC: short-lived cohorts go in tight stacks reset often;
long-lived ones in a coarser stack — but the cohorts are inferred statically and reclaimed by a
single bump-pointer reset, not a runtime collector.

## Per-stack validity (the two constraints)

A single bump-stack is valid iff both hold:

1. **Tail-growth.** At most one *unreserved* growable per stack, and it is the last allocation
   (the tail). Nothing may be allocated in that stack after the growable begins growing. (This is
   the discipline the committed tail-growth check already models; a `reserve`d growable is
   fixed-footprint and exempt.)
2. **LIFO-reclaimability.** Objects in one stack must be reclaimable in reverse allocation order,
   so their lifetimes must **nest or be disjoint** — never cross. (This is what the current
   interleaved-lifetime error is really protecting.)

Assignment is therefore **interval-graph coloring**: crossing lifetimes are conflict edges and
must land in different stacks; nesting/disjoint lifetimes may share. The minimum stack count is
the maximum number of simultaneously-crossing lifetimes — in practice 1–2.

## Assignment algorithm

### B1 (first milestone) — simple and sound

- Each **unreserved growable** → its **own stack** (it is the tail; grows freely).
- All **fixed + reserved** objects → one **shared stack**.

Trivially satisfies both constraints (each growable is alone; fixed objects never grow and only
need region-exit freeing). Dissolves the interleaved-lifetime error for the common case. Phase A
maximises the "reserved" set, shrinking the number of separate stacks needed.

**Over-split guard.** Each stack costs a block (8 KiB min for CHAINED; a reservation for
RESERVE_COMMIT). Beyond a small cap of auto-stacks per region, fall back to merging excess
growables into one CHAINED stack (accepting relocation holes) and emit a `-Wperf`-style note. A
function with many tiny growables must not allocate many 8 KiB blocks.

### B2 (follow-on) — interval coloring + early reclamation

Color the lifetime-crossing graph to pack fixed objects into the minimum stacks **and** pop a
stack's tail early when its top object dies (the auto-tightening docs/68 anticipates). B1 frees
only at region exit; B2 reclaims within the region.

## Codegen

Today an allocation's arena is chosen by `regionArenaOwner(container.Region)` — a string region
tag keys into the arena-owner map. That lever already exists; Phase B drives it:

1. **Tag allocations.** The assignment pass tags each container with its stack:
   `__auto_N#0`, `__auto_N#1`, … (set on the container type's `Region`, or a side-table).
2. **Region hosts K parallel arenas.** Generalise `emitRegionDeclImpl` so a synthesised region
   opens an *array* of arenas (one per stack tag) at entry and frees them all at exit. These are
   **parallel** (all span the region), not nested — nesting would impose LIFO across stacks,
   which is exactly what we are escaping.
3. **Route by tag.** `regionArenaOwner("__auto_N#k")` returns the k-th arena. push / reserve /
   resize already go through this owner lookup, so they are unchanged.

The infra for *multiple* arenas exists (side-by-side explicit `region ra:` / `rb:` already lower
to two `with arena`s). The new piece is **multiple parallel arenas under one scope**, freed
together.

## Subsuming the interleaved-lifetime error

With B1, two crossing-lifetime objects land in different stacks, so the LIFO conflict disappears.
The current `errorf` in `analyzeRegionLifetimes` becomes auto-splitting: instead of rejecting, the
assignment pass puts the crossing objects in separate stacks. The error survives only as a
fallback if the over-split guard is hit *and* an explicit region is genuinely required (rare).

## Staging

| Step  | Delivers                                                        | Risk  |
|-------|-----------------------------------------------------------------|-------|
| B1a   | Assignment pass: tag allocations (growable→own, fixed→shared)   | low   |
| B1b   | Codegen: region opens/frees K parallel arenas, route by tag     | high  |
| B1c   | Replace interleaved-lifetime error with auto-split              | medium|
| B2    | Interval coloring + early per-object reclamation                | medium|

Land B1a → B1b → B1c as the Phase-B milestone; B2 follows.

## Interactions with the other phases

- **Phase A (auto-reservation, landed):** every auto-reserved growable becomes fixed-footprint and
  packs into the shared stack — fewer separate stacks.
- **Phase C (per-stack strategy):** a growable-tail stack whose elements get interior refs across a
  growth → RESERVE_COMMIT (stable base); else CHAINED. The tail/interior facts are already
  computed by the tail-growth check.
- **Phase D (aliasing):** cross-stack pointers (stack B points into stack A, A frees first) are the
  only case separate stacks do not solve → conservative **merge** by default, `copy-out`/promotion
  opt-in.

## Non-goals / open questions

- B1 does not attempt fixed-object interval-coloring (that is B2); it uses one shared fixed stack.
- The over-split cap value and the CHAINED-merge fallback heuristics are tunable and will be set
  empirically.
- Aliasing across stacks (Phase D) is out of scope here.
