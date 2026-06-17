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
3. **90-3 (next) — division/mod with sound semantics; the `requires` precondition path (brick 86-5)
   as an SMT fallback; counterexample extraction (z3 `(get-model)`) to turn `sat` into a concrete
   "fails when a=10, b=10" diagnostic where the facts are complete.**
4. **90-4 (future) — quantifiers** (`forall`/`exists` in specs): the capability the linear tier can
   never reach, and the line between "refinement checker" and "verifier." Gated behind `-smt`, same
   soundness contract (only `unsat` concludes), with trigger management as the main risk.

Each brick: build → targeted test → full `./src/...` green → commit.
