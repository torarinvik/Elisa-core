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
   - **Deferred**: range-valued (non-constant) bracket arguments via direction-aware bounding (use the
     arg's lower bound for a `>=` law constraint, upper bound for `<=`); `ensures <result> is Law`
     clauses beyond the return-type form; re-asserting facts after a mutating call.

Each brick: build → targeted test → full `./src/...` green → commit.
