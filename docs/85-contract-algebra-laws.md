# 85 — Laws, refinements, and one discharge model

Status: **design (revised after an adversarial review).** This supersedes the first
proposal. It unifies refinement types, design-by-contract, frame conditions, effects, and
performance shape into one surface — but only as far as is *honest*: where the axes differ
in reliability, the model says so explicitly. The §11 hazard table maps every criticism
from the review to its resolution here.

## 1. The model in one sentence

A **law** is a pure total `bool` function whose first value parameter is its subject
(conventionally named `self`); the **`is`** operator applies it by binding its left side to
that first parameter — the *same* first-argument desugaring as UFCS/method calls
(`x is P[a]` ≡ `P(x, a)`, docs/19) — in type, flow, or contract position; obligations are
**discharged** against one sound flow-fact lattice (prove), else a debug check/measure, else
a `-strict` error — and each law declares a **discharge class** so a uniform surface never
implies a uniform guarantee.

## 2. Primitives

- **`law`** — a pure, total, deterministic `bool` function whose first value parameter is
  the subject (by convention `self`, but it is an ordinary explicitly-typed parameter, not a
  magic binding). Not a new AST kind: it reuses functions, generics, modules, the type
  checker, and the effect system (purity is *enforced* by requiring an empty effect set;
  totality by the existing progress/recursion checks).
  `law Bounded[lo, hi](self: i64) = self >= lo and self <= hi`.
- **`is`** — the one application operator, defined as **first-argument binding**: `x is P[a]`
  ≡ `P(x, a)`, exactly the UFCS/method-call desugaring (docs/19) — no new binding rule. The
  `is` left side fills the first value parameter; `[..]` are the static params; any remaining
  `(..)` are extra value args. Type position → refinement type; flow position → narrowing
  (today's `is`); contract position → obligation.
- **Composition = conjunction.** `includes` is clause-set union; a composite holds iff its
  parts do. Frame composition is *union of `changes` sets* (§7). Nothing more exotic.
- **One discharge routine** with the ladder of §6.

## 3. The soundness core is the fact lattice — not "independent provers"

There is **one** flow-sensitive abstract domain (the existing `FactTransform` / `is`
narrowing engine, extended) with sound transfer functions. Decision procedures *query* it;
they do not each carry their own truth. So:

- The soundness obligation lives on the **lattice + transfer functions** (the central thing
  to get right), plus each decision procedure being sound *relative to* the lattice.
- Decision procedures chain through the shared facts (interval prop establishes `x > 0`, the
  linear prover uses it) — which is why "independent monotone provers" was the wrong framing:
  cooperation is the point, so the coupling is real and the soundness must be central.

Prover tiers (all query the one lattice):

1. **known-facts + intervals** — always on, fast: single-variable bounds, sign, non-empty,
   tag-state. Discharges the easy refinements.
2. **bounded linear arithmetic** — multi-variable index bounds combining facts and struct
   invariants (e.g. the framebuffer index of §13.5). Budgeted; *required* for the
   interesting cases — interval propagation alone cannot prove them, and the design says so.
3. **external SMT** — a *pluggable, off-by-default* procedure for the residual. Never a
   dependency of the core.

## 4. Discharge classes (the honesty layer)

Every law has a class fixing *how it can be discharged* and *whether it may climb into type
position or gate a build by proof*. This is what stops `ensure Vectorizes` from looking like
`requires Positive`.

| class | example | discharged by | refinement type? | prove-gates build? |
|---|---|---|---|---|
| **value** | `Positive`, `Bounded[..]`, `NonEmpty` | fact lattice | yes | yes |
| **frame** | `changes`, `preserves` | mutation/alias analysis | no | yes |
| **effect** | `forbid Time.Now` | effect-grant system | no | yes |
| **shape** | `NoAlloc`, `NoBoundsChecks`, `BranchFree` | local codegen analyses | no | yes |
| **measure** | `Vectorizes`, `Inlined`, cycle/alloc budgets | debug measure / IR verify / benchmark | no | **no** |

The hard rule: **measure-class laws are never `prove`-discharged and never a composable
premise.** They are verified post-hoc (the autovec metadata verifier), surfaced via
`-Wperf`, or measured in debug — but you cannot `includes Vectorizes` into another law and
rely on it transitively, because vectorization is emergent and non-local (inlining into a
caller can create or destroy it). See §8.

## 5. Refinement types — the precise rules

1. **Erasure is of representation only.** `T is P` has the exact bytes of `T` — transparent
   to layout, ABI, FFI, regions, monomorphization. But *verification may still emit code*:
   an unproven obligation at a boundary inserts a (debug) check. "Erased" ≠ "free."
2. **Invariant under mutation.** `T is P <: T` covariantly in *immutable* position only.
   `mutable darray[T is P]` is **not** `mutable darray[T]` (you could store a non-`P` through
   the wider alias). Refinements are invariant under `mutable` refs/aggregates. Non-negotiable.
3. **Dependence requires a freeze.** A dependent refinement like `i is Bounded[0..<xs.count]`
   mentions a runtime value; it is valid only while `xs` is immutable/borrowed for the
   refinement's scope. A mutation of `xs` invalidates it — routed through the *same*
   storage-view invalidation chokepoint that already catches iterator invalidation.
4. **Runtime fallback only at function boundaries.** Params, returns, `requires`, `ensure`
   may debug-check the residual. Refinements on **struct fields and element types are
   prove-only** — no runtime fallback — so the model never silently inserts a per-element
   check, and erasure stays honest.

## 6. The discharge ladder

```
obligation ─► query the fact lattice
   ├─ Proven    → emit nothing; (subsume the bounds watchdog — no double check)
   ├─ Refuted   → WARNING, escalated to error only on a provably-reachable path with no
   │              unknowns (refutation-soundness is weaker than proof-soundness, so it must
   │              not reject valid programs on an incomplete analysis)
   └─ Unknown   → boundary check in debug / measure in debug (elided in release);
                  under -strict, a hard error for load-bearing (non-`measure`) laws
```

`-strict` is the dial from earlier: it turns `Unknown` into "prove it or it won't compile"
for everything except `measure` laws. Default keeps "debug verifies what release assumes."

## 7. Frame conditions — `changes` is primitive, `preserves` is derived

`preserves <all but X>` is the frame problem (whole-call-graph, alias-sensitive, and brittle
to new fields). So the primitive is the **upper bound on mutation**:

- `changes S` — the function (transitively, through aliases) writes at most the places in `S`.
- `preserves Y` ≡ `Y ∩ changes(f) = ∅` — derived, robust to new fields.

Discharged by the interprocedural mutation/alias analysis (the substrate from the noalias
work). Open-world by construction.

## 8. Performance — local shape (provable) vs emergent (measure only)

- **shape laws** (`NoAlloc`, `NoBoundsChecks`, `NoRealloc`, `BranchFree`) are *local,
  stable, compositional* — genuinely `prove`-class, and they may gate a build.
- **measure laws** (`Vectorizes`, `Inlined`, `CacheFriendly`, cycle/alloc budgets) are
  *emergent and non-local*. They are NOT proven and NOT composable premises. `Vectorizes`
  is *verified* against this function's final emitted IR (the existing autovec marker) and
  surfaced through `-Wperf` — a diagnostic, not a guarantee you can build on.

This kills the category error of treating "this loop is fast" as a local proof.

## 9. Soundness invariants (must always hold)

1. **Representation erasure** (§5.1) — refinements never change bytes.
2. **Fail-closed for load-bearing facts** — `Unknown` never *assumes* a fact the codegen
   depends on; the optimization is simply not taken (the generalized noalias rule).
3. **Invariance under mutation** (§5.2).
4. **Dependence freeze** (§5.3).
5. **Purity of laws** — enforced by the effect system; a law may not observe or mutate.
6. **Watchdog subsumption** — a proven index disables the debug bounds check; an unproven
   one keeps exactly one. Never double-instrument.
7. **Refutation is conservative** (§6) — incomplete analysis warns, does not reject.

Corollary for the refinement-scheme work: verification signatures may carry proof metadata, but
ordinary type identity remains representation identity. Do not route `SameType`/`AssignableTo`
through SMT or law entailment. Likewise, an SMT-proven value refinement is not automatically a
proof-indexed storage fact: bounds-check elision stays on the dedicated index-bounds channel.

## 10. Non-goals

- No separate "law runtime" — everything desugars to existing contracts/effects/analyses.
- No per-axis syntax — one `law`/`is`/`ensure`; only the discharge class differs.
- No trusted backdoor — the existing `trusted` block is the only escape hatch, reused as-is.
- No flow-fact duplication — refinement narrowing *is* the existing engine, extended.
- No build-gating on `measure` laws.

## 11. Hazards from the review → resolutions

| smell | resolution |
|---|---|
| over-unification implies uniform reliability | discharge classes (§4); reliability stated + shown |
| perf laws conflate local vs emergent | shape vs measure split (§8) |
| decidable core too weak for the demos | explicit prover tiers; tier-2 linear solver required (§3) |
| `Refuted`→error doubles soundness surface | refutation = warning unless provably reachable (§6, §9.7) |
| "independent monotone provers" illusory | soundness centralized in the fact lattice (§3) |
| subtyping + mutation covariance hole | invariance under mutation (§5.2) |
| dependent refinements go stale | dependence-freeze via the invalidation chokepoint (§5.3) |
| closed-world `preserves` brittle | `changes` primitive, `preserves` derived (§7) |
| runtime fallback explodes off-boundary | boundary-only fallback; field/element prove-only (§5.4) |
| black box | proof report `--explain` (§12) |
| compile-time blowup | summaries + cache + capped solver + degrade-not-hang (§12) |
| "erased" too strong / watchdog double-check | erasure is representation-only (§5.1); watchdog subsumption (§9.6) |

## 12. Observability and budget (first-class, not afterthoughts)

- **Proof report.** `elisac --explain` (and per-line annotation) shows each obligation as
  *proven (which tier) / debug-checked / measured / refuted*. Without this the system is a
  black box and debug performance is unpredictable; it is a Stage-1 requirement, not later.
- **Budget.** Per-function discharge summaries (reuse `FunctionAnalysis`), cached results,
  a cap on tier-2/SMT calls, and **degrade to a runtime check rather than hang** when the
  budget is exceeded.

## 13. Worked examples (Wolf3D, honest forms)

```elisa
type TileX = i32 is Bounded[0..<MAPWIDTH]          # value-class refinement
type TileY = i32 is Bounded[0..<MAPHEIGHT]

# value: result refinement proven by the linear prover (tier 2)
def tile_index(tx: TileX, ty: TileY) -> usize is Bounded[0..<4096]:
    return (tx * MAPHEIGHT + ty).usize()           # tilemap[...] needs no check (watchdog subsumed)

# frame: `changes` primitive; mutation analysis discharges it; preserves derived
law MovesPlayerOnly: changes self.px, self.py      # ⇒ preserves health/score/tilemap/...
def clip_move(r: mutable Render&, xmove: i32, ymove: i32):
    fulfills r is MovesPlayerOnly                  # a stray r.health <- … is a compile error

# shape (provable, build-gating) vs measure (verify-only) — kept distinct
law HotPixelLoop: includes NoAlloc, NoBoundsChecks # shape: local, provable
def scale_column(r: mutable Render&, pm: VSwap&,
                 x: i32 is Bounded[0..<r.vw],       # dependent on r.vw: valid while r frozen in this call
                 texcol: i32 is Bounded[0..<TEXTURESIZE]):
    fulfills HotPixelLoop                           # NOT `Vectorizes` — that loop is data-dependent;
    ...                                             # Vectorizes would be measure-only anyway
```

Note `x: i32 is Bounded[0..<r.vw]` is a *dependent* refinement (§5.3): sound because `r` is
not reassigned within the call; the analysis ties its validity to that.

## 14. Staged implementation

- **Stage 1** — `law` (pure predicate, implicit `self`, purity enforced) + `is` in type and
  contract position + the discharge dispatcher with the **tier-1** lattice prover and the
  boundary-only runtime fallback. Ship the **proof report** with it. Value-class only.
- **Stage 2** — the tier-2 linear prover (multi-var index bounds) + watchdog subsumption.
- **Stage 3** — `changes`/`preserves` frame laws on the mutation analysis; dependence-freeze.
- **Stage 4** — shape laws (`NoAlloc`/`NoBoundsChecks`) on the effect/bounds analyses;
  `-Wperf`/`-strict` wired as the dials (the `-Wperf` disjoint hint is the first instance).
- **Stage 5** — measure laws (verify/benchmark), optional SMT prover plug-in.

Refinement-type *subtyping under mutation* (§5.2) and the dependence-freeze (§5.3) are part
of Stage 1's type rules from the first commit — they are soundness, not polish.
