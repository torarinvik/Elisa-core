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
   - **Scope**: arithmetic-only first (binders + scalars; no array indexing). Array-element quantifiers
     (`forall i in 0..<n: xs[i] > 0`, modeling `xs[i]` via SMT array theory) are the next brick.
   - **Deferred**: array-theory element quantifiers; quantifiers in `requires`/`ensure` clauses (only
     law bodies activate the syntax today); trigger/pattern tuning (z3 auto-triggers; the 2 s timeout +
     decline keeps a matching loop safe).

Each brick: build → targeted test → full `./src/...` green → commit.
