# 116 — Heap and framing model: reconciling regions + ownership with a logical heap

Status: **design note (no code).** Prerequisite mandated by [docs/115](115-verification-foundation-and-extension-prereqs.md)
§"Heap/framing is the one real frontier — design before building." This note answers: *what
heap/framing logic should Elisa grow toward F\*/Dafny scale, given that it already has a
region + affine-ownership + borrow model that Dafny and F\* do not?* The headline claim is that
**ownership gives Elisa most of dynamic-frames' framing for free**, and the build order should
exploit that rather than bolt a general separation-logic heap onto a language that already
forbids the aliasing such a heap exists to tame.

## 0. Where we are today

- **Framing** is `changes`/`preserves` ([docs/87](87-frame-conditions.md)): a *path-set algebra*
  over param-rooted field chains (`framePath{root, fields}` in
  `compiler/src/semantic/analyzer_frame_changes.go`). It enforces, **intraprocedurally**, that
  every caller-visible write (direct assignment through a ref param — channel 1; mutable-ref
  argument — channel 2; mutating builtin method — `checkFrameForMutatingBuiltinMethod`) lands in
  the declared set, and `framePathsOverlap` derives `preserves`. Interprocedural *consumption* is
  partial: a callee's frame *summary* refines a caller's channel-2 conservatism
  (`resolveFrameSummary` / `calleeFrameSuffixesForParam`, 87-3), but a frame fact is **not** yet a
  proposition the SMT/fact layer can use (`analyzer_frame_changes.go` is entirely disjoint from
  `analyzer_vc_ir.go` / `analyzer_fact.go`). This is the gap docs/115 flagged.
- **Aliasing** is constrained *before* it ever reaches the verifier, by three cooperating systems:
  - **Regions** ([docs/68](68-region-memory-model.md)): every dynamic value lives in exactly one
    region; `@r` provenance is a type parameter; reclamation is whole-region; interior refs are
    rejected the moment the backing *can* relocate (§4 there). Region identities are static, so a
    `reset` of one region provably cannot touch another's data.
  - **Affine ownership / borrows** ([docs/10](10-orthogonality-packed-enums-regions-and-affine-concurrency.md),
    `analyzer_flow_affine_consumption.go`, `analyzer_flow_borrowed_owner_*`): an owner is consumed
    once; a `mutable T&` borrow is exclusive for its extent; the mutable-alias checker
    (`analyzer_alias_access.go`) tracks borrow provenance even when laundered through a
    ref-returning call (`ReturnIsolation.AliasParamIndices`, [[alias-laundering-hole]]).
  - **Escape analysis** (`function_return_isolation.go`, `checkNestedRegionStoreEscape`,
    `forwarded_ref_store_escape_test.go`): a borrow cannot outlive its referent or escape into a
    region that does not outlive it.

The crucial observation: **Dafny needs a heap map and `reads`/`modifies` clauses precisely
because its references are unrestricted-ly aliasable.** Elisa's references mostly are not. So the
design question is not "which heap logic do we port" but "*for which residual cases* does
ownership fail to give framing, and what is the minimal heap reasoning those cases need."

## 1. The key insight: ownership = framing, for the cases ownership covers

Dynamic frames (Dafny) and separation logic (F\*/Steel, implicit dynamic frames) both exist to
answer "after I call `f`, what do I still know about state `f` didn't touch?" in a setting where
*any* reference might alias *any* other. They pay for generality with an explicit heap (`Heap`
map, `old(heap)`, `reads`/`modifies` sets of object references) threaded through every spec.

Elisa already answers that question structurally for a large class:

1. **An owned value with no live borrow is unaliased.** If `f` does not receive `x` (by value,
   by ref, or transitively through a reachable owned field), `f` *cannot* mutate `x` — there is no
   path to it. This is the affine guarantee, checked by `analyzer_flow_affine_consumption.go`. No
   `modifies` clause is needed to know `f` preserves `x`: the parameter list **is** the modifies
   bound.
2. **A `mutable T&` borrow is exclusive.** While `r: mutable T&` is borrowed, no other live path
   reaches `*r`. So "what does `f(r)` change" is bounded by `r`'s reachable owned subgraph, and
   `changes r.fields` already names exactly the caller-visible slice of it.
3. **Region identity partitions the heap statically.** Two values in different regions cannot
   alias (provenance is a type parameter; `analyzer_flow_region_state_provenance.go`). A function
   that only touches region `frame` provably preserves everything in `perm` — Dafny would need a
   `modifies` set and a disjointness lemma; Elisa gets it from the type.

**Therefore: Elisa should NOT adopt a global aliasable heap as its baseline framing model.** The
baseline should be *ownership-as-frame*: the frame of a call is computed from (param list) ∩
(declared `changes` paths), and everything else is preserved **by typing**, not by an explicit
preservation proof. This is strictly cheaper than dynamic frames and it is sound for cases 1–3.
The residual — §3 — is the only place a heap-flavored mechanism is justified.

## 2. `modifies`/`reads` in terms of the existing path algebra — and lifting a frame to a proposition

We already have the syntax (`changes`) and the algebra (`framePath`, `framePathCovered`,
`framePathsOverlap`). What is missing is the bridge to the proposition layer so framing becomes
*interprocedural fact*, not just an intraprocedural write-site check. The design:

### 2.1 `changes` is the `modifies`; `reads` is deferred

`changes S` **is** Dafny's `modifies S`, in path form rather than object-set form. We do not need
a separate `modifies` keyword. A `reads` clause (the dual, bounding what a *pure* spec/`ghost`
function may observe) is a real future need for heap-dependent specs, but it is **not** required
for the framing bridge and should be deferred (it only matters once §3 recursive heap predicates
exist; until then function purity is already enforced by the effect set,
`analyzeSpecClauseExpr`, docs/115 hardening #1).

### 2.2 The frame proposition: `unchanged(p)` over `old`

Connect framing to the SMT layer with **one** new derived proposition, emitted at call sites, not
a new clause surface:

> For a call `f(args)` whose callee frame summary (`resolveFrameSummary` → `FrameWrites`,
> `FrameBounded`) bounds its writes to places `W`, the caller may assume, for every place `p` it
> held a fact about that is **provably disjoint** from every `w ∈ W`:  `p == old(p)`
> (i.e. `unchanged(p)`), where `old(p)` is the pre-call value already representable now that
> `vcTerm` has `vcApply`/`old` machinery (docs/115 #3).

Concretely the bridge is:

1. At a call site, take the callee's `FrameWrites` (already computed). Rebind each `{ParamIndex,
   Fields}` to the *actual argument place* at this site (the same rebinding
   `checkFrameMutableRefArg` already does to get `arg ⊕ suffix`).
2. The set of *rebound written places* `W` is the modifies set **for this call**, expressed in
   the caller's own `framePath` vocabulary.
3. For each hypothesis fact `φ(p)` live across the call (range fact, refinement, guard fact —
   once docs/115's fact unification lands, all of these are `hypothesisFact`), test
   `framePathsOverlap(p, w)` for every `w ∈ W`. **No overlap ⇒ `φ(p)` survives the call**
   unchanged; record it as still-live instead of invalidating it.
4. **This is the same disjointness test the algebra already implements** — we are reusing
   `framePathsOverlap`/`framePathCovered` as the *frame separation oracle* for the fact layer.
   The novelty is consuming its verdict to *preserve* facts, where today a call conservatively
   invalidates them (`factInvalidatedBy`).

This makes a frame fact "first-class" exactly in the sense docs/87 §4/87-3 wanted: a callee's
`changes` set becomes a *premise a caller's reasoning relies on*, discharged by the path algebra,
flowing into the SMT hypothesis set — **without** modeling memory as an SMT array. The heap stays
implicit; only the *separation verdict* crosses the boundary. This is the smallest possible
coupling and it does not touch the path algebra's representation.

### 2.3 Why path-form, not object-set, is right here

Dafny's `modifies` is a *set of heap objects* because Dafny cannot name field paths through
aliases statically. Elisa *can* — `r.px`, `level.entities` are stable static paths because
ownership/region forbids the aliasing that would make them ambiguous. Path-form `modifies` is
**more precise** than object-set `modifies` (it frames at field granularity, not whole-object) and
costs no heap map. Keep it.

## 3. The residual: aliasable mutable graphs ownership does NOT cover

Ownership/region gives framing for tree-shaped owned data and exclusive borrows. It does **not**
cover the case where the *same mutable cell is reachable by two live paths* — and that case does
exist in Elisa:

- **`Pooled[T]` + `Handle[T]` graphs** (docs/68 §9.3): two handles into the same pool, a node that
  references another node by handle. Handles are not affine — they are copyable indices — so the
  affine checker does not prove `pool.get(a)` and `pool.get(b)` disjoint.
- **Intra-region cyclic / shared structures**: a graph in one `chained` region where node `n1`
  and `n2` both point (by `@owner` ref) at `n3`. Region identity proves `n3 ∉ perm`, but **within
  `loading`** it does not prove `n1.next` and `n2.next` are different cells.
- **Recursive predicates over the above**: "this list is sorted", "this graph is acyclic",
  "every node's `parent` back-edge is consistent" — properties that quantify over an
  unbounded, internally-aliased heap fragment.

For these, the §2 disjointness oracle is *too coarse*: two handles into one pool have the *same*
`framePath` root vocabulary collapse (both are `pool.<slot>`), so the algebra correctly says "may
overlap" and conservatively invalidates — which is sound but defeats framing exactly where it is
hardest.

**Minimal heap reasoning for the residual (and no more):**

1. **A `region`-scoped logical heap, not a global one.** Model a heap *fragment* indexed by region
   identity: `heap[loading]` is an SMT array from (abstract handle/address) to value, existing
   **only for regions that contain an aliasable graph** (detected: a struct with a self/peer
   `@owner` ref field, or a `Pooled[T]`). Regions without such a structure never get a heap map —
   they stay in the §1 ownership-frames world. This is the hybrid: *dynamic-frames-style reasoning,
   scoped to the region that needs it, riding region identity as the natural disjointness frame.*
2. **`modifies` for the graph case names region + footprint.** `changes pool` (a whole aliasable
   structure) lowers to "modifies `heap[r]` at the footprint reachable from `pool`", and
   cross-region disjointness (`heap[frame]` vs `heap[perm]`) is discharged by region identity for
   free — the part Dafny pays a disjointness lemma for, Elisa gets from provenance.
3. **Recursive heap predicates as `ghost`/`law` functions over the region heap**, gated by the
   *existing* termination machinery (verified `decreases`, docs/115 — the F\*/Dafny soundness
   invariant we already have). No new soundness foundation; reuse `analyzer_termination.go`.

This is deliberately *not* full separation logic: no separating conjunction `*`, no frame rule as
a primitive. The region partition is the ambient separation; we only need an SMT array *inside* a
region, and only for regions flagged as containing shared mutable graphs.

## 4. Recommendation

**Hybrid, ownership-first.** Ordered by docs/elisa-four-principles (reject-unsafe first, then make
the safe path ergonomic):

| approach | verdict | why |
|---|---|---|
| pure dynamic frames (Dafny) | **no** as baseline | pays a global heap map + disjointness lemmas for aliasing Elisa's type system already forbids; fights region provenance (§5) |
| pure separation logic (F\*/Steel) | **no** | `*`/frame-rule machinery is overkill when regions already give ambient separation; huge surface, slow to verify |
| ownership-as-frame (§1–2) | **yes, baseline** | framing for free for tree/owned/borrow/region-disjoint cases; reuses `framePathsOverlap` as the separation oracle; zero heap map |
| region-scoped logical heap (§3) | **yes, only for the residual** | the minimal heap needed for `Pooled`/shared-graph cases; rides region identity for cross-region disjointness |

### Incremental path (no breaking change to the path algebra)

- **116-1 — Frame facts survive disjoint calls.** Implement §2.2: at call sites, rebind callee
  `FrameWrites` to argument places, and *preserve* (rather than invalidate) any `hypothesisFact`
  whose place is `framePathsOverlap`-disjoint from the written set. **Prereq:** docs/115 fact
  unification (guard/refinement facts behind `hypothesisFact`) must land first, so one
  invalidation predicate governs preservation too. Pure addition; `analyzer_frame_changes.go`
  representation untouched.
- **116-2 — `unchanged(p)`/`old(p)` propositions into the VC IR.** Emit the surviving facts as
  `vcApply`/`old`-backed equalities (the nodes exist, docs/115 #3) so SMT can *use* a frame fact,
  not just the fact lattice. Still no heap map.
- **116-3 — `reads` clause (deferred until needed).** Only when heap-dependent `ghost` specs
  appear (§3.3).
- **116-4 — region-scoped heap for the aliasable-graph residual.** §3: a per-region SMT array,
  introduced only for flagged regions, recursive predicates gated by the existing termination
  prover. This is the only step that adds a heap representation, and it is contained.

Ship 116-1/116-2 — they deliver interprocedural framing (the docs/87 87-3 endgame) with no new
heap model. Defer 116-4 until a real dogfooding case (a verified `Pooled` invariant) demands it;
per docs/91's discipline, do not build the heavy machinery speculatively.

## 5. Do NOT do X (corners to avoid)

- **Do NOT introduce a single global heap `Heap` threaded through all specs.** It would re-encode
  the unrestricted aliasing Elisa's type system spends its whole budget *eliminating*, and it
  would make region provenance verification-invisible — every spec would re-prove disjointness the
  region partition already guarantees. Heap, if any, is **per-region** (§3.1).
- **Do NOT make `modifies` an object-reference set.** Path-form (`framePath`) is more precise and
  alias-safe here; an object-set form throws away the field granularity and would need an aliasing
  model to interpret. Keep `changes` as the spelling and the path algebra as the representation.
- **Do NOT let the heap model contradict region provenance.** A heap address must carry / be
  indexed by its region identity; a frame fact must never claim two cross-region places *may*
  alias (provenance proves they cannot) nor that two same-region handles *cannot* (the algebra must
  stay conservative there — §3 is the only place that refines it, and only with an explicit heap).
- **Do NOT bypass the termination gate for recursive heap predicates.** docs/115's verified-
  `decreases` gate is the F\*/Dafny soundness invariant; a heap predicate that recurses over the
  region graph enters SMT only behind it (`analyzer_termination.go`, `analyzer_lemma.go`). No
  `decreases *`.
- **Do NOT couple the path-algebra *representation* to SMT.** The bridge (§2.2) consumes the
  algebra's *verdict* (`framePathsOverlap`) as an oracle; it must not require rewriting
  `framePath`/`framePathCovered` into SMT terms. Keeping them separate is what lets 116-1/116-2
  ship without a breaking change and lets 116-4 be optional.
- **Do NOT model `changes`-as-formula before the fact layer is unified.** Lifting frames to
  propositions on top of three divergent fact mechanisms re-creates the invalidation-divergence
  risk docs/115 calls "the load-bearing retrofit risk." Unify first (116-1 prereq).

## 6. Relationship to other documents

- [87-frame-conditions.md](87-frame-conditions.md) — the `changes`/`preserves` path algebra this
  note bridges to the proposition layer (87-3's "interprocedural framing remaining" is 116-1/2).
- [68-region-memory-model.md](68-region-memory-model.md) — region identity = the ambient
  separation this design leans on instead of separation logic.
- [10-orthogonality-…-affine-concurrency.md](10-orthogonality-packed-enums-regions-and-affine-concurrency.md)
  — the affine ownership / `deps(v)` model that makes "ownership = framing" (§1) sound.
- [115-verification-foundation-and-extension-prereqs.md](115-verification-foundation-and-extension-prereqs.md)
  — the prereqs (fact unification, VC quantifier nodes, termination gate) this build order depends
  on; this note is the design-before-building deliverable it mandates.
- [85-contract-algebra-laws.md](85-contract-algebra-laws.md) — frame is the §4 *frame discharge
  class*; this note keeps it a discharge-by-analysis class, never a value premise.
