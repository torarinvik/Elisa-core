# 86 — Stage 2: the tier-2 bounded linear arithmetic prover

Status: **design.** Implements docs/85 Stage 2 (§3 prover tier 2, §9.6 watchdog
subsumption). Tier-1 (single-variable interval + const-eval) shipped in Stage 1; this is the
multi-variable layer the spec flags as *required for the interesting cases* — the only thing
that can prove a derived index like `tx*MAPHEIGHT + ty is Bounded[0..<4096]`.

## 1. The gap, precisely

Today (`analyzer_refinement_flow.go`) the prover is **single-variable**:

- `numRange{lo, hi}` is a closed interval over **one** immutable integer identifier.
- `lawConstraints` extracts a conjunction of `self OP const` from a law body.
- `tryProveRefinementByFlow` discharges `x is Bounded[0,500]` only when `x` is a *bare*
  identifier whose own range fact entails the law's range.

What it cannot do — and what every motivating example needs:

```elisa
def tile_index(tx: TileX, ty: TileY) -> usize is Bounded[0..<4096]:
    return (tx * MAPHEIGHT + ty).usize()
```

Here the subject of the result obligation is the **expression** `tx*MAPHEIGHT + ty`, not a
variable. Proving it needs: (a) facts on *two* variables (`tx ∈ [0,63]`, `ty ∈ [0,63]`),
(b) the *linear form* of the expression, and (c) arithmetic to combine them
(`0 ≤ 64*tx + ty ≤ 64*63+63 = 4095 < 4096`). Tier-1 has none of these.

## 2. Scope — deliberately bounded

This is **bounded linear integer arithmetic**, not a general SMT integration:

- **In:** affine forms `c₀ + Σ cᵢ·xᵢ` over integer variables, with `cᵢ` compile-time
  constants. Operators `+`, `-`, unary `-`, and `*` **where at least one side is constant**.
  Obligations of the form `lo ≤ E ≤ hi` (the value-class `Bounded`/`Positive`/`NonEmpty`
  shapes — exactly what `lawConstraints` already yields).
- **Out (declines, falls to runtime check):** non-linear terms (`x*y`, `x*x`), division/mod
  in the *subject* (mod *in the law body* stays a tier-1 side-constraint, unchanged), bitwise
  ops, anything whose variables lack a range fact. Declining is always sound — it just means
  a debug check instead of elision.

Non-linear and residual cases are Stage 5's optional SMT plug-in, not this.

## 3. Abstract domain: affine forms over the existing lattice

No new stored state on `Scope`. The linear prover is a **query procedure** that reads the
facts already there and builds an ephemeral system per obligation:

```
affine form   L := { const: int64, terms: map[varName]int64 }   // c₀ + Σ cᵢ·xᵢ
```

Two operations, both total and allocation-light:

- `affineOf(expr) (L, ok)` — structural recursion over the subject AST:
  - int literal `k` → `{const:k}`
  - immutable int ident `x` → `{terms:{x:1}}`
  - `a + b`, `a - b` → componentwise add/sub of the sub-forms
  - `k * b` / `a * k` (one side a const) → scale the other form by `k`
  - unary `-a` → negate
  - paren / `.usize()` / width casts on an int → transparent (pass through)
  - anything else → `ok=false`
- `boundAffine(L, scope) (lo, hi, loKnown, hiKnown)` — interval-evaluate `L` by substituting
  each variable's `numRange` from `scope.rangeFacts` (the **seed**, §4). Per term
  `cᵢ·xᵢ`: if `cᵢ ≥ 0` use `[cᵢ·loᵢ, cᵢ·hiᵢ]`; if `cᵢ < 0` swap. Sum the per-term
  intervals plus `c₀`. A term whose variable has no `loKnown`/`hiKnown` on the relevant side
  makes that side of the result open → entailment on that side fails → decline.

This is interval arithmetic *over linear forms* — strictly more than tier-1 (which could only
bound a bare variable) and exactly enough for the index-derivation family. It is Fourier-
Motzkin's easy case (entailment of `lo ≤ L ≤ hi` from per-variable boxes); we do **not** need
a full simplex for the Stage-2 examples. The doc is honest about that ceiling: cross-variable
*coupling* constraints (`i < n` where both vary) are §7 future work.

## 4. Seeding the system from existing facts

`boundAffine` substitutes ranges drawn from, in order of precedence:

1. **`scope.rangeFacts[x]`** — branch-narrowed intervals (`if tx < MAPWIDTH:`) and
   `is`-narrowed law ranges (`gatherLawIsRangeRefinement` already lands these).
2. **Refinement-typed params** — a param `tx: TileX` where `TileX = i32 is Bounded[0..<64]`
   carries its range *on entry*. Tier-1 already resolves a param's declared refinement to a
   range; expose it as a seedable fact so `affineOf`'s leaves can be bound. **This is the
   key new seeding hook** — without it `tile_index`'s params have no range.
3. **`writtenConst[x]`** — a last-written constant pins `x` to `[k,k]` (reuses tier-3 data).

Dependence-freeze (§5.3) is respected for free: `affineOf` only admits *immutable* int idents
(same `immutableIntIdentName` gate tier-1 uses), so a mutable variable can never enter a form.

## 5. Integration point

One new clause in `tryDischargeRefinementStaticallyOpt` (`analyzer_law_is.go`), between the
flow proof and the const-eval fallback — so tier-2 only runs when tier-1's bare-variable path
already declined, and const-eval still backstops literals:

```go
if a.tryProveRefinementByLinear(value, lawDecl, pred.Args, scope) {
    a.recordProof(pos, valueName, pred.Name, ProofProvenLinear)
    return true
}
```

`tryProveRefinementByLinear`:
1. `L, ok := a.affineOf(value, scope)`; `!ok → false`.
2. `constraints, ok := a.lawConstraints(lawDecl, paramConsts)` — **reuse verbatim**; the law
   side is unchanged, still `self OP const` conjunctions.
3. For each constraint `self OP c`, check the corresponding side of `boundAffine(L)` entails
   it (e.g. `self <= hi` needs `L.hi ≤ hi`; `self >= lo` needs `L.lo ≥ lo`). All must hold.
4. Any open bound on a needed side → `false`.

New `ProofOutcome` `ProofProvenLinear = "proven (linear)"` in `analyzer_law_contract.go` so
`--explain` distinguishes tier-2 discharges (observability is a Stage-1 invariant, §12).

## 6. Watchdog subsumption (§9.6) — the payoff

Proving the obligation is only half the win; the point is **no double-instrumentation**. When
a container index `arr[E]` is proven in-bounds by tier-1 *or* tier-2, the backend must skip
`emitDebugIndexBoundsGuard` for that exact index site.

Plumbing (new, small):
- A `Result.ProvenIndexBounds map[ast.Node]bool` keyed by the `*ast.IndexExpr` (or its Pos).
- Where index expressions are checked semantically (`analyzer_bounds_indexing.go`), when the
  index expression discharges `is Bounded[0..<count]` against the container's length, mark the
  site. The length-bound obligation is synthesized from the container's `.count`/static
  extent — this is the dependent-fact path (`Bounded[0, xs.count]`) we already support, now
  also feeding the backend.
- `emitDebugIndexBoundsGuard` consults the set and emits nothing for a marked site. Exactly
  one check survives an unproven index; zero for a proven one (§9.6 invariant). Release is
  unaffected (no check either way); the win is **debug builds and `-fbounds-check`** drop
  proven checks — real codegen, not just assertions.

Fail-closed (§9.2): a missing/false entry always keeps the check. Subsumption can only
*remove* a check it proved; it can never assume one away.

## 7. Budget & degradation (§12)

- `affineOf` is structural and linear in expression size — no fixpoint, no search. Natural
  cap: a max term count (e.g. 32) per form; exceed → decline → runtime check.
- No solver loop to time out. (When Stage 5 SMT lands, *that* gets the call cap + degrade-to-
  runtime; tier-2 as specified cannot hang.)
- Per-function results already cache through the existing discharge path; nothing new needed.

## 8. What this explicitly does NOT do (honest ceiling)

- **No coupling constraints.** `for i in 0..<n: arr[i]` with `arr.count == n` *both runtime* —
  proving `i < arr.count` needs the equality `n == arr.count` as a relational fact, which is a
  cross-variable coupling, not a per-variable box. That is the loop-index case; it wants a
  small relational layer (or the dependent-fact freeze extended to loop induction vars) and is
  the natural Stage-2.5 follow-up. Documented so the index example in §13.5 of docs/85 isn't
  silently assumed covered.
- **No multiplication of two variables, no division/mod in subjects.** Declines, sound.
- **`..<` in flow position** still parses only in type position (pre-existing follow-up).

## 9. Staged bricks

1. **86-1 — affine core. [LANDED]** `affineOf` + `boundAffine` + `tryProveRefinementByLinear`
   wired between flow and const-eval; `ProofProvenLinear`. Seeds from `rangeFacts` only. Test:
   `tile_index` with locally-narrowed `tx`/`ty` proves.
2. **86-2 — param refinement seeding. [LANDED]** `seedParamRefinementFacts` records each
   immutable-int param's declared refinement range (direct or via alias) onto the entry scope,
   so `tx: TileX`/`ty: TileY` carry bounds on entry; the docs/85 §13 form proves with no body
   narrowing. Alias refinements (`type TileX = i32 is Bounded[..]`) are captured in
   `aliasRefinements` (namedTypes erases them) and their predicate validation is **deferred**
   to `validateAliasRefinements` (after laws are collected — aliases resolve far earlier).
3. **86-3 — watchdog subsumption. [LANDED]** The analyzer already proved index bounds into
   `indexBoundsProven` (const-in-array, const-in-static-view, var-with-proven-upper-bound +
   non-negativity) but the set only drove a permission lint. Now exposed as
   `Result.IndexBoundsProven` and consulted by `emitDebugIndexBoundsGuard` (which takes the
   `*ast.IndexExpr`): a proven site emits NO debug guard — never double-instrumented. Test:
   a `0..<xs.count` loop index (non-const, non-`trusted`) emits no `wd.in_bounds` at -O0.
4. **86-4 (optional, scoped separately) — relational coupling** for the loop-index family
   (§8). Only if 86-1..3 don't already cover the real call sites.
5. **86-5 — static precondition discharge (interprocedural). [LANDED]** Connects a callee's
   `requires <bool-expr>` clauses to the CALLER's static facts. Previously `requires` was only a
   runtime debug-check inside the callee body (design-by-contract); now at each direct call site
   `f(args...)` the clause is re-interpreted with the callee's parameters substituted by the
   actual argument expressions and proven against the caller's facts with the same affine/interval
   machinery (`analyzer_requires_discharge.go`):
   - **proven** — caller facts entail the precondition → `ProofProvenLinear` in `--explain`;
     under `-strict` this is the line between "checked at runtime" and "guaranteed".
   - **refuted** — caller facts prove the precondition is ALWAYS violated → hard compile error
     (a definite contract break caught before the program runs).
   - **unknown** — declined soundly; the callee's runtime check stands, a `proofLint` warns, and
     `-strict` escalates to an error (Dafny-like prove-it-or-fail).

   Mechanics: `dischargeCallRequires` (hooked next to `dischargeCallArgRefinements` in
   `analyzeCallExpr`) builds a param→arg substitution; `proveRequiresClause` handles `and`
   conjunctions structurally and comparison leaves via `substitutedAffine` (mirrors `affineOf`
   plus the substitution leaf and a general product rule) + `boundAffine` over the difference
   `L - R`. Cross-variable preconditions (`requires lo <= hi`) fall out for free. **Soundness:**
   the fragment fails closed — any non-linear/unknown leaf or open bound makes a clause unknown;
   refutation needs the negation to hold on the WHOLE bounded range, so range over-approximation
   can only weaken a refutation to unknown, never fabricate one.

   Enabler (broadens tier-2 generally): `boundAffine` now falls back to the **written-constant**
   tracker (`writtenConstInt`) when a variable has no branch-derived range, so an immutable local
   `k: i32 = 5` bounds to the point range `[5,5]`; and an immutable const-initialized var decl now
   records a written-const fact (previously only assignments did). Both sound (immutable + const
   init = permanent exact value).

6. **86-6 — cross-variable bound equality (relational coupling). [LANDED]** Closes the §8
   loop-index gap: `for i in 0..<n: xs[i]` where `n` is known equal to `xs.count`. The index-bound
   prover is *syntactic* — a loop sets `i`'s upper bound to the string `"n"`, while the access wants
   `"xs.count"`, so they never matched and the access fell to a runtime check. Brick 86-6 adds a
   flow-sensitive **equivalence relation over upper-bound expression strings** (`currentBoundEqual`,
   a symmetric adjacency set with reflexive+transitive `boundExprEqual`). The bounds-proof check now
   accepts `fact.Upper == want OR boundExprEqual(fact.Upper, want)`.

   Equalities are recorded from three sources: an `==` guard (`if n == xs.count:`), an immutable
   integer binding (`n: usize = xs.count`), and a `requires n == xs.count` precondition seeded at
   function entry (the canonical Dafny pattern, no body guard needed). **Soundness** rests on the
   relation riding *exactly* the same lifecycle as `currentIndexBounds`: cloned/saved/restored at the
   if-block and loop boundaries (so an equality cannot leak out of a branch), invalidated whenever a
   referenced container base is mutated (`invalidateBoundEqualReferencingBase`, folded into the same
   `invalidateIndexBounds*` functions so it inherits their mutation-site coverage — a `requires`
   equality dies the moment `xs.push(..)` changes the count), and reset per function in
   `analyzeRequiresClauses` (the one entry hook, since a top-level binding records without a
   save/restore). Negative control test confirms an unrelated `n` is NOT subsumed.

7. **86-7 — recursive-function termination (`decreases`). [LANDED]** The Dafny termination story:
   a function with a `decreases <measure>` clause proves it terminates by showing the measure
   strictly decreases — and stays bounded below — at every recursive call (a strictly-decreasing
   sequence of naturals is finite). Implemented for **direct self-recursion**, reusing the
   bounded-linear prover: the measure at a recursive call is the measure expression with the callee's
   parameters substituted by the call's arguments (`substitutedAffine`), and the obligation is that
   `measure(params) - measure(args)` is a provably-positive constant.

   - Surface: `decreases <int-expr>` as a leading body clause (parsed like `requires`/`ensure` into a
     `ContractStmt`, lifted to `FuncDecl.Decreases`). Multiple `decreases` lines form a lexicographic
     tuple, satisfied at the first component that strictly decreases with all earlier components
     provably unchanged.
   - Discharge (`analyzer_termination.go`, `checkTermination` called once per function after the body,
     where params are still in scope): collect direct self-calls (`walkStaticStmts`), substitute
     args, prove `measureStrictlyDecreases` (difference's lower bound > 0) and `measureBoundedBelow`
     (unsigned type, or a provable ≥ 0 floor). Proven → `ProofProvenLinear` "termination of f";
     unprovable → hard error (`the function may not terminate`).
   - **Opt-in and additive:** termination is checked ONLY when a `decreases` clause is present, so
     existing recursive code is unaffected. With the clause it is an explicit claim, so an unprovable
     measure is a hard error (like `ensures`). A `decreases` on a non-recursive function warns
     (unused clause / likely a typo'd recursive call).
   - **Soundness:** the prover fails closed (any non-affine arg, e.g. `f(n/2)`, or an unbounded
     measure → not proven → error, never a false "terminates"). Strict-decrease + bounded-below is a
     complete termination argument for the cases it proves.

   **Deferred (docs/86 §9 follow-ups):** while-loop variants (a loop `decreases` requires tracking
   the measure's change THROUGH the body — a dataflow problem, unlike the substitution available at a
   call); mutual recursion (a→b→a); and division/multiplicative measures (`f(n/2)`), which need
   monotonicity reasoning beyond linear affine arithmetic.

Each brick: build → targeted test → full `./src/...` green → commit, per the established
per-brick pattern.
