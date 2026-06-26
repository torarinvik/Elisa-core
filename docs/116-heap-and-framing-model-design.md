# 116 — Heap and framing: decision record

Status: **decision record (no build plan).** Mandated by [docs/115](115-verification-foundation-and-extension-prereqs.md)
§"Heap/framing is the one real frontier — design before building." This records *what framing model
Elisa should grow toward* and the corners to avoid — not an implementation schedule. The mechanics
are deliberately left to be designed when a real case demands them.

## Decision

Elisa will **not** adopt a global aliasable heap (Dafny dynamic frames) or separation logic
(F\*/Steel) as its framing baseline. Those exist to tame unrestricted aliasing; Elisa's
region + affine-ownership + borrow model already forbids it, so framing is **structural** for the
common cases. Baseline = *ownership-as-frame*. A heap is introduced, if ever, only **per-region**
and only for the one residual case below.

## Why ownership already gives framing (the durable insight)

For the cases ownership covers, "what does `f` preserve?" is answered by typing, with no
`modifies`/`reads` clause and no heap map:

- **The parameter list is the modifies bound.** If `f` does not receive `x` (by value, by ref, or
  through a reachable owned field), it has no path to mutate it (affine consumption,
  `analyzer_flow_affine_consumption.go`).
- **A `mutable T&` borrow is exclusive**, so a call's writes are bounded by that borrow's reachable
  subgraph — which `changes r.fields` already names.
- **Region identity partitions the heap statically** ([docs/68](68-region-memory-model.md)): values
  in different regions cannot alias, so a function touching only region `frame` preserves `perm` for
  free — the disjointness Dafny pays a lemma for, Elisa gets from provenance.

## The one residual ownership does NOT cover

Genuinely aliasable mutable graphs — `Pooled[T]`/`Handle` (copyable indices), or intra-region
shared/cyclic `@owner` graphs — where the *same cell is reachable by two live paths*. Ownership
can't prove two handles into one pool disjoint. **If** this ever needs verifying, model a heap
*fragment scoped to that one region* (an SMT array keyed by region identity) — never a global heap —
with recursive heap predicates gated by the existing verified-`decreases` termination machinery
([docs/115](115-verification-foundation-and-extension-prereqs.md)). Design the specifics then; do
not build speculatively (docs/91 discipline).

The likely first step toward *interprocedural* framing (the [docs/87](87-frame-conditions.md) gap),
when we get there: reuse the existing `changes`/`preserves` path algebra (`framePathsOverlap`,
`analyzer_frame_changes.go`) as a *separation oracle* for the fact layer — keep a fact alive across
a call when its place is provably disjoint from the callee's write-set — which needs no heap model.
Prerequisite: unify the fact layer first (docs/115).

## Do NOT (corners to avoid)

- **No single global `Heap` threaded through specs** — it re-introduces the aliasing the type system
  spends its budget eliminating and makes region provenance verification-invisible. Any heap is
  per-region.
- **No object-reference-set `modifies`** — path-form (`framePath`) is more precise and alias-safe
  here; keep `changes` as the spelling and the path algebra as the representation.
- **A heap model must not contradict region provenance** — never claim two cross-region places may
  alias (provenance proves they cannot).
- **Recursive heap predicates only behind the verified-`decreases` gate** (docs/115) — the
  F\*/Dafny soundness invariant. No `decreases *`.
- **Do not couple the path-algebra representation to SMT** — consume its `framePathsOverlap` verdict
  as an oracle; don't rewrite `framePath` into SMT terms.
- **Do not lift frames to propositions before the fact layer is unified** — doing so on top of the
  still-divergent fact mechanisms re-creates the invalidation-divergence risk docs/115 calls the
  load-bearing retrofit risk.

## See also

[87-frame-conditions.md](87-frame-conditions.md) (the path algebra) ·
[68-region-memory-model.md](68-region-memory-model.md) (region identity = ambient separation) ·
[10-orthogonality-packed-enums-regions-and-affine-concurrency.md](10-orthogonality-packed-enums-regions-and-affine-concurrency.md)
(affine ownership) · [115-verification-foundation-and-extension-prereqs.md](115-verification-foundation-and-extension-prereqs.md) (prereqs).
