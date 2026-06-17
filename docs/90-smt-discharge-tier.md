# 90 — The optional SMT discharge tier

Status: **bricks 1–2 landed + profiled.** Adds an SMT solver as the highest tier of the docs/85
discharge ladder, reached only for obligations the bounded-linear prover (docs/86) declines —
non-linear products, division/mod (deferred), richer boolean law bodies, and (future) quantifiers.

## 1. Why a tier, not a replacement

The cheap tiers (flow interval, tier-2 affine, const-eval) stay first. SMT runs **last**, only on the
hard residue. Three consequences:

- **Cost is bounded and measurable** — the solver sees only what linear couldn't, so the marginal
  cost is the cost of the genuinely hard obligations, not the whole program.
- **Soundness is unchanged** — SMT can only *add* proofs; the existing tiers and the runtime fallback
  are untouched.
- **It's optional** — off unless `-smt`. No solver dependency for a normal build.

## 2. Architecture: SMT-LIB2 subprocess, not CGO

The harness (`src/smt`) drives a solver binary (default `z3`) in incremental mode over stdin/stdout,
speaking standard SMT-LIB2. Chosen over CGO `libz3` bindings because:

- **Zero native build dependency** — `go build` stays clean (the project already manages LLVM link
  friction); no solver needed to *build* the compiler, only to *use* `-smt`.
- **Solver-agnostic** — any SMT-LIB2 solver (z3, cvc5, yices) is a drop-in via the binary path.
- **Directly profileable** — subprocess wall-time per query is the number we want.

If profiling ever shows IPC dominates, an in-process backend can implement the same `smt.Solver`
interface with no change above it. **Profiling says it does not** (see §5).

The solver is **long-lived**: opened lazily on the first obligation that needs it (a compile with no
hard obligations never spawns a process) and closed at end of analysis, so the spawn cost is paid
once and amortized across every query.

## 3. The soundness contract

For an obligation `O`, we ask the solver whether `facts ∧ ¬O` is satisfiable:

| solver result | meaning | verdict |
|---|---|---|
| `unsat` | no input satisfies the facts yet violates `O` | **proven** (`ProofProvenSMT`) |
| `sat` | a model violates `O`, but `facts` is a *subset* of what's true | **decline** → runtime check |
| `unknown` / timeout / no solver | undecided | **decline** → runtime check |

Only `unsat` concludes. `sat` is *not* a refutation: we model only the integer flow facts, so a
counterexample may rest on a fact we didn't translate — refutation stays with const-eval, which works
from exact values. An incomplete translation or a flaky solver can therefore lose a proof (fall back
to a runtime check) but can **never fabricate one**. The harness maps any I/O error / missing solver
/ malformed answer to `Unknown` for the same reason.

A per-query timeout (2 s) keeps a pathological obligation from stalling the compile — it times out to
`Unknown` and uses the runtime check.

## 4. What it proves today (brick 2)

The translator (`analyzer_smt_discharge.go`) lowers the integer/bool fragment to SMT-LIB2:

- **terms**: integer literals, immutable integer identifiers (declared as SMT `Int` consts, with their
  flow facts asserted as hypotheses), unary minus, and `+ - *` — **including the var×var product the
  affine prover cannot handle** (the headline win).
- **bool**: comparisons (`= > >= < <= distinct`), `and`/`or`/`not`, bool literals.
- **hypotheses**: each free variable's range fact (`>=`/`<=` bounds) and written-constant equality.

Wired as the last step of `tryDischargeRefinementStaticallyOpt` (the shared `value is Law` path), so
it covers var-decl, call-arg, return, and ensures boundaries at once. Example now provable:

```elisa
type Small = i64 is Bounded[2, 10]
def mul(a: Small, b: Small) -> i64 is Bounded[4, 100]:
    return a * b                # a*b is var*var → linear declines → SMT proves [4,100]
```

A false bound (`Bounded[4, 50]`, since `a*b` reaches 100) is **not** proven — `sat` → runtime check.

**Division/mod are deliberately excluded:** SMT-LIB `div`/`mod` are Euclidean and would not match
Elisa's truncating integer division for negative operands, so translating them could be unsound. They
return to the linear-declined → runtime path until brick 3 models the semantics carefully.

## 5. Profile — is it cheap or demanding?

`TestSMTProfileBatch` (200 nonlinear obligations, all proven) on z3 4.16, this machine:

```
proven=200 declined=0
solver total = 16.3ms  (spawn 0.7ms, slowest 3.5ms)
per-obligation solver cost = 0.082ms
whole-analysis wall (lex+parse+analyze+SMT) = 22.4ms
```

**~80 microseconds per hard obligation, amortized.** Cheap — because the solver is long-lived (spawn
paid once), the fragment is easy for z3, and only the residue reaches it. The `--explain` report
prints this line so the cost is always visible:

```
SMT tier: 200 obligations, 200 proven, 0 declined; solver 16.3ms (spawn 0.7ms, slowest 3.5ms)
```

## 6. Staged bricks

1. **90-1 — harness. [LANDED]** `src/smt`: solver-agnostic SMT-LIB2 subprocess, incremental
   push/assert/check-sat/pop, per-query timing, fail-safe to Unknown. Tests run against z3 when present.
2. **90-2 — translator + discharge tier + profile. [LANDED]** integer/bool fragment incl. var×var;
   `facts ∧ ¬O` query; `ProofProvenSMT`; `-smt` flag; `--explain` profile line; `SMTStats` on Result.
   Profiled at ~0.08ms/obligation.
3. **90-3 — division/mod, `requires` SMT fallback, counterexamples. [LANDED]**
   - **division/mod**: `+ - * /` and `%` now translate. SMT-LIB `div`/`mod` are *Euclidean*, which
     equals Elisa's *truncating* `/`/`%` only for a non-negative dividend and a strictly-positive
     divisor — so translation is gated on `provablyNonNeg(dividend) ∧ provablyPositive(divisor)`
     (unsigned type or interval lower bound; the positivity gate also excludes div-by-zero, where
     SMT-LIB `div` is an unconstrained total function that could unsoundly "prove"). A signed dividend
     that could be negative declines. Now provable: `def half(n: usize is Bounded[0,100]) -> usize is
     Bounded[0,50]: return n / 2u`. Full signed-division modeling (introduce q,r with truncation
     axioms) is a follow-up.
   - **`requires` SMT fallback**: when the linear clause prover (brick 86-5) declines a precondition,
     `trySMTProveRequires` translates the clause with params substituted by caller-arg terms and proves
     it against the caller's facts. Now provable: `requires a * b <= 100` at a call with a,b ∈ [2,5].
   - **counterexamples**: the harness gained `CheckValues` (on `sat`, fetch `(get-value …)` for the
     declared vars; balanced-s-expr reader + tolerant parser, negatives normalized). A precondition the
     caller's facts don't guarantee now warns with a concrete witness — *"it can fail when a=5, b=5"* —
     instead of a generic message. Honest framing (a hint, not a refutation: our facts are a subset),
     but a real input the caller's facts permit.
4. **90-4 — quantifiers (`forall`/`exists`). [LANDED, arithmetic fragment]** The capability the
   linear tier can structurally never reach, and the line between "refinement checker" and "verifier."
   - **Surface** (logical-implication form, chosen by design): `forall i: <body>` / `exists i, j:
     <body>` in a law body, with `implies` as a low-precedence right-associative connective. Bounds
     live in the body as a guard: `law InRange(self: i64, n: i64) = forall k: (0 <= k and k < n)
     implies self != k * 2`. `forall`/`exists`/`implies` are activated only inside law bodies (parser
     `allowQuantifiers` flag), so ordinary code may still use them as identifiers; `implies` desugars
     to `(not a) or b` at parse (no new analyzer/SMT/backend node).
   - **AST/analyzer**: `ast.QuantifierExpr{Exists, Vars, Body}`; type-checked in a child scope (binders
     are `i64`, body must be bool).
   - **SMT**: `boolTerm` emits `(forall ((q_i Int) …) body)` / `(exists …)`, binders prefixed `q_` so
     they never collide with a free variable's `v_` symbol. Same soundness contract — only `unsat`
     concludes.
   - **Spec-only (the key consequence)**: an unbounded quantifier is **not executable**, so a
     quantified law has **no runtime fallback**. `lawBodyContainsQuantifier` routes it to SMT-only; if
     neither SMT nor const-eval proves it, the obligation is a warning (error under `-strict`) and
     emits **no** runtime check — never broken codegen. Proven by SMT → `proven (smt)`.
   - **Scope**: arithmetic-only first (binders + scalars; no array indexing).
5. **90-5 — array-element quantifiers (SMT array theory). [LANDED]** Quantify over container contents:
   `forall i: (0 <= i and i < n) implies self[i] > 0`. The container is modeled as an SMT `(Array Int
   Int)` and `self[i]` becomes `(select <arr> i)`; `self.count`/`.len` become a per-array non-negative
   length symbol (derived from the array's SMT symbol so it resolves through `self`). Only
   integer-element arrays/darrays are modeled (`isArrayLike`); other element types decline.
   `trySMTProveRefinement` binds an array-typed subject via `arrayTermEnv` (instead of `term`), so the
   law's `self` is the SMT array. The showcase is a real theorem over an **abstract** array — *sorted ⟹
   the first element is the minimum* — proved by z3 with NO concrete contents:
   ```elisa
   law SortedFirstMin(self: darray[i64], n: i64) =
       (forall i: (0 <= i and i < n - 1) implies self[i] <= self[i + 1])
       implies (forall j: (0 <= j and j < n) implies self[0] <= self[j])
   ```
   A false array claim (`forall i: self[i] == self[0]`) is soundly declined. (Note: law bodies are
   single-line today — the multi-line form above is illustrative.)
6. **90-6 — interprocedural quantified contracts (assume `requires`). [LANDED]** The practical
   payoff: a function may **assume its `requires` clauses** as SMT hypotheses when discharging
   obligations in its body. Quantifiers are now parsed in `requires`/`ensure` clauses (the
   `allowQuantifiers` flag is set around those `parseExpr` calls too), and `trySMTProveRefinement`
   asserts the enclosing `currentFuncDecl.Requires` (translated with the SAME translator, so param/array
   symbols unify with the obligation) before the negated goal. So:
   ```elisa
   def first(xs: darray[i64], n: i64) -> i64 is NonNeg:
       requires n > 0
       requires forall k: (0 <= k and k < n) implies xs[k] >= 0
       return xs[0]                       # proven: instantiate the forall at k = 0
   ```
   **Contract soundness**: a callee may assume its preconditions (callers must establish them — and
   are warned, error under `-strict`, when they can't). Assuming them is safe even without a runtime
   check because an SMT-proven *value* fact never drives bounds-check elision (that is the separate
   syntactic/linear `indexBoundsProven` system, not fed by SMT) — so a violated precondition is
   garbage-in-garbage-out, never memory unsafety. A clause outside the fragment is skipped (fewer
   assumptions = conservative). Without the precondition, `xs[0] is NonNeg` correctly does NOT prove.
   - **Deferred**: caller-side proof of quantified *array* preconditions (the caller rarely has the
     quantified facts; declines to a warning today); multi-line law/contract bodies; trigger/pattern
     tuning.
7. **90-7 — return refinements as caller-side facts (the other direction). [LANDED]** The dual of
   90-6: where 90-6 lets a callee *assume* its preconditions, 90-7 lets a caller *use* the callee's
   postcondition. A refined return type IS a postcondition — `def clamp() -> i64 is Bounded[0, 100]`
   promises its result is in `[0, 100]`. When an immutable integer binding takes the result of such a
   direct call, `seedReturnRefinementFacts` records that interval as a flow fact on the binding:
   ```elisa
   def clamp() -> i64 is Bounded[0, 100]: ...
   def use() -> i64:
       x = clamp()          # x assumes Bounded[0,100] — the callee already PROVED it
       y: i64 is Nat = x    # discharges statically, no runtime check, no re-derivation
   ```
   This closes the modular loop: the callee proves its return refinement once
   (`dischargeReturnRefinements`), and every caller reuses it as a fact instead of re-deriving it from
   the body. Shared kernel `rangeFromRefinementTypeExpr` (factored out of `seedParamRefinementFacts`)
   computes the interval from a constant-argument refinement. **Sound and conservative**: immutable
   bindings only; direct resolvable calls only; constant-argument return refinements only; the seed
   only *narrows* (intersect), never widens; and like every refinement VALUE fact it never drives
   bounds-check elision, so even a wrong callee contract is garbage-in-garbage-out, never memory
   unsafety. With no return refinement, nothing is seeded and the obligation falls back to a runtime
   check. Tests: `refinement_type_test.go` `TestReturnRefinementSeedsCallerFact` /
   `TestReturnRefinementNoSeedWhenUnrefined`.
   - **Deferred**: parametric return refinements (`-> i64 is Bounded[0, n]`, needs arg substitution
     — done in 90-8); seeding from `ensures <result> is Law` clauses beyond the return-type form;
     re-asserting facts after a mutating call.
8. **90-8 — parametric return refinements (argument substitution). [LANDED]** A return refinement may
   name the callee's own parameters in its bound — `def cap_to(n: i64) -> i64 is Bounded[0, n]`. At a
   call site, `seedReturnRefinementFacts` now builds a callee-param → caller-argument substitution map
   and resolves the bracket arguments in the caller's terms, so `x = cap_to(100)` seeds `x ∈ [0, 100]`
   and a downstream `y: i64 is Nat = x` discharges with no runtime check:
   ```elisa
   def cap_to(n: i64) -> i64 is Bounded[0, n]: ...
   def use() -> i64:
       x = cap_to(100)      # n ↦ 100  ⟹  x ∈ [0, 100]
       y: i64 is Nat = x    # proven from the substituted fact
   ```
   `substConstInt` const-evaluates a bracket argument after substituting callee params, over the small
   arithmetic fragment (`n`, `n - 1`, `n * 2`, …). **Sound and conservative**: a substituted argument
   that does not itself const-fold in the caller (e.g. `cap_to(m)` for a runtime `m`) drops the
   predicate — no fact seeded, runtime fallback — never a fabricated bound. With `subst == nil`
   (the param-entry seed) the kernel is exactly the old constant path. Tests:
   `TestParametricReturnRefinementSeedsCallerFact` / `TestParametricReturnRefinementNonConstArgNoSeed`.
   - **Deferred**: range-valued bracket arguments (done in 90-9); `ensures <result> is Law` clauses
     beyond the return-type form; re-asserting facts after a mutating call.
9. **90-9 — range-valued bracket arguments (direction-aware bounding). [LANDED]** Generalizes 90-8:
   when a parametric return refinement's bracket argument is a non-constant caller value with a *known
   interval* rather than an exact constant, it is resolved direction-aware. For `self OP param` with
   `param ∈ [lo, hi]`: a `>=`/`>` constraint contributes `self >= lo` (since `self >= param >= lo`)
   and a `<=`/`<` constraint contributes `self <= hi` (since `self <= param <= hi`); `==`/`!=` against
   a non-constant interval cannot become a single sound constraint and is declined.
   ```elisa
   def cap_to(n: i64) -> i64 is Bounded[0, n]: ...
   def use(k: i64 is AtMost10) -> i64:
       x = cap_to(k)          # n ↦ k, k ∈ (−∞, 10]  ⟹  x <= 10  (and x >= 0)
       y: i64 is AtMost10 = x # proven from the ranged fact — k is never a constant
   ```
   A new `paramRanges` channel threads through `lawConstraintsRanged` → `collectLawConstraints` (which
   now normalizes each leaf to `self OP operand` once, then tries `operandConst` then
   `operandRangeBound`). `substArgRange` bounds the substituted bracket argument by reusing the
   bounded-linear machinery (`substitutedAffine` + `boundAffine` over the caller's range facts).
   **Sound and conservative**: a bracket argument with no known interval on the needed side resolves
   nothing → predicate dropped → runtime fallback, never a fabricated bound. The exact-constant
   callers pass `paramRanges == nil`, so 90-7/90-8 and the param-entry seed are byte-for-byte
   unchanged. Tests: `TestRangedReturnRefinementSeedsCallerFact` /
   `TestRangedReturnRefinementNoBoundNoSeed`.
   - **Deferred**: value-contract postconditions over `result` (done in 90-10); re-asserting facts
     after a mutating call.
10. **90-10 — value-contract postconditions as caller facts. [LANDED]** Beyond the refined return
    *type*, a function's design-by-contract postconditions over `result` (`ensure result >= 0`,
    `ensure result <= n`) are equally a promise about the returned value. `seedReturnRefinementFacts`
    now also reads `decl.EnsureValues`: each clause's `result OP operand` comparisons (in conjunction
    position) contribute an interval on the result binding, with the same caller-substitution +
    direction-aware bounding as refinement bracket args.
    ```elisa
    def clamp(n: i64) -> i64:
        ensure result >= 0
        ensure result <= n
        return 0
    def use() -> i64:
        x = clamp(50)              # result ∈ [0, 50]
        y: i64 is Bounded[0, 50] = x   # proven, no runtime check
    ```
    `rangeFromEnsureResult` / `collectResultConstraints` collect from the clauses; unlike a law body
    they never fail-whole — a comparison outside the fragment is skipped, since each `ensure` conjunct
    is independently true (partial information is sound). **Why this and not `ensures <param> is
    Law`**: that channel already gains a *pred* fact at the call site (docs/85 brick 2A), and its only
    syntactic form is a *mutable* ref param — a numeric range seeded on a mutable variable would never
    be read (the flow prover admits only immutable idents), so a range seed there would be inert.
    `ensure result` constrains the *immutable* result binding, which the flow prover does read. Same
    soundness model as the rest: postconditions are a contract (debug-checked / release-elided), never
    drive bounds-check elision, so a wrong one is garbage-in-garbage-out, never memory unsafety. Tests:
    `TestEnsureResultPostconditionSeedsCallerFact` /
    `TestEnsureResultParametricPostconditionSeedsCallerFact` / `TestEnsureResultNoPostconditionNoSeed`.
    - **Deferred**: ~~`old(...)` in postconditions~~ — now done (see below). (The range-seed-across-a-call item is also done — brick 90-11.)

11. **90-11 — `ensures <param> is Law` seeds the caller's INTERVAL store. [LANDED]** The complement
    to 90-10. A mutating callee's `ensures p is Bounded[0, 9]` already gains a *pred* fact at the call
    site (docs/85 brick 2A); now it also seeds an integer interval on the caller's variable, so the
    flow/interval prover can use the mutated argument's numeric bound directly — discharging an
    obligation the exact-key factset path cannot:
    ```elisa
    law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi
    def clamp(p: mutable i64&) -> void ensures p is Bounded[0, 9]:
        p <- 0
    def use(seed: i64) -> i64:
        n: mutable i64 = seed
        clamp(&n)                       # n ∈ [0, 9]
        m: i64 is Bounded[0, 20] = n    # proven via interval ([0,9] ⊆ [0,20]); factset has only Bounded[0,9]
        return m
    ```
    **The prior "would be inert" reasoning (90-10) was wrong about the mechanism.** The *read* path
    (`tryProveRefinementByFlow` → `lookupRangeFact`) never gated on immutability; the immutable-ident
    gate only governs *recording branch-condition* facts. The real blocker was that `rangeFacts` had
    **no write-invalidation** — its whole soundness rested on only ever holding facts about immutable
    variables. 90-11 adds that invalidation: `invalidateRangeFacts` / `invalidateRangeFactsForTarget`
    fire at exactly the four sites `invalidatePredFacts` does (the three assignment forms + the
    call-site mutable-ref drop), so a seeded interval on a mutable variable can never outlive a write.
    The seed (`seedEnsuresParamRangeFacts`) is applied AFTER the call-site invalidation (so it
    survives), gated to **mutable-ref params** (only there does the postcondition constrain the
    caller's variable — a by-value/immutable-ref param's `ensures` describes the callee's copy), and
    reuses `rangeFromLawApplication`. Range facts need NO dependent-fact cascade (unlike predFacts):
    they are concrete interval snapshots with no live symbolic dependence on other variables, so only a
    write to the *subject* stales them. Riding the predFact invalidation envelope exactly is the
    soundness argument. Same model as the rest — interval facts only NARROW, never drive bounds-check
    elision, so a wrong contract is GIGO not memory unsafety. Tests:
    `TestEnsuresParamSeedsCallerInterval` / `TestEnsuresParamIntervalDroppedOnMutation`.

12. **90-12 — `old(...)` in postconditions + void-return contract gap. [LANDED]** Completes the
    design-by-contract postcondition surface: `ensure result == n - old(n)`, `ensure p == old(p) + 1`.
    `old(expr)` is the value of `expr` at function ENTRY, letting a postcondition relate the final
    state to the initial one. The front-end already recognized `old(...)` (parsed as a pseudo-call,
    typed via `inEnsureContext`) and the backend already captured/substituted it — but it was untested
    and **two real bugs hid behind that**:
    - *Capture stored the reference, not the value.* `emitOldCaptures` emitted `old(p)` for a `T&`
      param as the pointer, so the return-time check re-read the same address (always the *current*
      value, never the entry snapshot) — and the raw pointer type-mismatched the auto-deref'd operand.
      Fixed by coercing the capture to the pointee type (`expected = RefType.Elem`) so the entry rvalue
      is snapshotted.
    - *Void returns skipped value-contract checks entirely.* A bare `return` (and the fall-through
      exit) lowered straight to `RetVoid`, calling only `emitRefinementPostconditionChecks` (the `is
      Law` channel), never `emitPostconditionChecks` (the `ensure <bool>` channel). So ANY `ensure` on
      a `void` function was silently unenforced. Added the call at both void exits.
    - A third, smaller fix: the optimization-fact provenance walk (`regionRefStateForExpr`) descended
      into the `old` callee identifier and reported "undefined identifier old"; an `old`-call now
      resolves to its inner argument's provenance.
    Debug-only like all contracts (`-O0` / `ELISACORE_FORCE_CONTRACTS`), zero cost in release. Tests:
    `TestOldEnsureSatisfiedRuns` / `TestOldEnsureViolatedTrapsInDebug` / `TestOldResultSatisfiedRuns` /
    `TestOldResultViolatedTrapsInDebug` (end-to-end compile+run, satisfied passes / violated traps).

13. **90-13 — caller-side quantified array preconditions. [LANDED]** The deferred half of 90-6: where
    90-6 lets a callee *assume* its `requires` in-body, 90-13 lets a caller *discharge* a callee's
    quantified array precondition at the call site from the caller's OWN matching precondition.
    `trySMTProveRequires` now (a) maps array-typed arguments through the array env (so a clause
    `forall k: 0<=k<n implies xs[k] >= 0` resolves `xs` to the caller's array symbol, instead of
    immediately declining as a non-integer term) and (b) asserts the enclosing function's own
    `requires` as SMT hypotheses (`smtRequiresHypotheses`, the same translator the in-body path uses).
    Both clauses then translate against the same array symbol, so:
    ```elisa
    def consume(xs: darray[i64], n: i64) -> i64:
        requires forall k: (0 <= k and k < n) implies xs[k] >= 0
        return 0
    def forward(data: darray[i64], m: i64) -> i64:
        requires forall k: (0 <= k and k < m) implies data[k] >= 0
        return consume(data, m)        # PROVEN from forward's own precondition
    ```
    **Sound and conservative**: only `unsat` concludes; an SMT-proven precondition never drives
    bounds-check elision (GIGO, never memory unsafety); the caller's callers must establish the
    caller's `requires`; a caller clause outside the fragment is skipped (fewer assumptions). Without a
    matching caller precondition the call declines to the runtime check (a warning). Tests:
    `TestSMTCallerQuantifiedArrayPrecondition{Proven,DeclinesWithoutFact}`.

14. **90-14 — standing in-body invariants re-checked on mutation. [LANDED]** (design-by-contract
    follow-up.) An in-body `invariant <bool-expr>` was checked only where written; now it is a STANDING
    contract, re-asserted after every later mutation (within its block scope) of any variable it reads
    — so `invariant x >= 0` before a loop re-checks on each `x <- ...` inside, the loop-invariant
    idiom. Backend: `functionState.activeInvariants` records each invariant's condition + the set of
    identifier names it reads (`collectInvariantIdentNames`); `emitStmt` (the single statement
    chokepoint) calls `recheckInvariantsAfter`, which on an assignment form (`AssignStmt`/`AugAssignStmt`/
    `AsRefAssignStmt` — the same envelope as brick 90-11) whose target root ident is in an invariant's
    var set re-emits `emitContractCheck`. The list is truncated at block exit (`emitBlock`), so a
    re-check never re-evaluates a condition whose variables have left scope. Debug-gated like all
    contracts (registered only when the in-place check is emitted), and never drives codegen — a
    re-check just re-evaluates the real condition, so it can only trap on a genuine violation, never
    falsely on valid code; a missed node kind in the free-ident walk under-checks but never over-traps.
    Tests (end-to-end compile+run): `TestInvariantRecheck{HoldsRuns,ViolatedTrapsInDebug,ElidedInRelease}`.

15. **90-15 — block-form law bodies. [LANDED]** A law may name its sub-predicates in an indented block
    ending in `return`, instead of packing everything onto one `= <expr>` line:
    ```elisa
    law SortedFirstMin(self: darray[i64], n: i64):
        sorted   = forall i: (0 <= i and i < n - 1) implies self[i] <= self[i + 1]
        firstMin = forall j: (0 <= j and j < n)     implies self[0] <= self[j]
        return sorted implies firstMin
    ```
    Pure parser desugaring (`desugarLawBlock`): the `name = <expr>` bindings (immutable, resolved in
    order — a later binding may reference an earlier one) are inlined into the final `return`
    expression by capture-safe AST substitution (`substituteLawIdents` respects quantifier binders and
    wraps each replacement in parens). The law is then represented EXACTLY like the `= <expr>` form (a
    single predicate `ReturnStmt`), so every downstream tier (SMT, linear, flow, codegen) is byte-for-
    byte unchanged. A block that is not `bindings* return` is a parse error. (Multi-line *parenthesized*
    expressions already worked — the lexer suppresses newlines inside parens; 90-15 adds *named* parts.)
    Required completing `ast.CloneExpr` for `QuantifierExpr` (it silently returned nil before, which
    surfaced as a bogus "not requires bool operand" on a quantifier binding). Tests:
    `TestBlockFormLaw{ProvesArrayQuantifierTheorem,DeclinesFalseClaim,ChainedBindings}`.

16. **90-16 — SMT quantifier trigger tuning. [LANDED]** Array-element quantifiers now carry an explicit
    E-matching trigger: a `forall`/`exists` over array contents is emitted as `(! <body> :pattern
    (<select-terms>))`, where the pattern is the set of `(select <arr> <idx>)` subterms whose index
    mentions a bound variable (`collectSelectTriggers`, the canonical array-quantifier trigger). This
    gives z3 a deterministic, cheap instantiation strategy instead of relying on auto-pattern
    inference, which matters as quantifier count scales. **Soundness/completeness preserved**: triggers
    only guide E-matching, and z3's MBQI (on by default) still completes any goal the trigger alone
    would miss — so no existing proof regresses (the whole quantifier suite passes with patterns on). A
    purely arithmetic quantifier (no select term on a binder) gets no pattern and is left to MBQI
    exactly as before. Test: `TestSMTTriggerPreservesArrayAndArithmeticProofs` (the sorted theorem and
    `NotDouble` both still prove).

Each brick: build → targeted test → full `./src/...` green → commit.
