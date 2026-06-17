# 85 — Contract Algebra: composable `law`s

Status: **design proposal.** Grounds the "contract algebra" discussion in Elisa's
*existing* contract + effect + property machinery, separates what already works from
what is genuinely new, and stakes a bottom-up implementation ladder. No code yet.

## 1. The idea

A `law` is a **named, parameterized, reusable correctness property** built by composing
smaller laws. Tiny laws are atoms (`FinitePosition`, `ValidDeltaTime`); they compose into
mechanics (`BasicMovement`, `DamageApplication`); those compose into systems
(`PlayerController`, `ProjectileUpdate`); those into the architecture (`GameFrame`,
`RenderFrame`). A function declares `fulfills SomeLaw(args)` and the compiler must
**discharge** it — prove it, test it, or record an explicit review obligation. The
composition is simultaneously a proof structure, a design document, and an AI-repair map.

This is domain-specific correctness as a *composable, checked* contract — the natural
extension of Elisa's existing design-by-contract.

## 2. What Elisa already has (the atoms are real)

The discussion's primitive laws are, almost entirely, **expressible in today's syntax**:

| discussion law | today in Elisa |
|---|---|
| `ensure entity.position.x is finite` | `ensure` postcondition clause (leading body stmt; debug-checked) |
| `require dt >= 0` | `requires` precondition clause |
| `ensure result == old(x) + delta` | `ensure` with `result` and `old(...)` — both implemented |
| struct field law (`health >= 0`) | struct `invariant <bool>` (checked on construct/mutate, debug) |
| randomized property | `@property` fuzz harness (source-generated test) |
| `forbid Time.Now / Random.Unseeded` | the effect-grant system — a function's `can [...]` set |

So `requires` / `ensure(result, old)` / `invariant` / `@property` / effect grants are the
**atoms already shipped** (see memory: design-by-contract-impl, effect-alias-redesign).
The contract *runtime* (snapshot `old`, assert at entry/exit, debug-gated, elided in
release) exists and is the substrate to reuse.

## 3. What is genuinely new

1. **`law Name(params): …`** — a named, parameterized bundle of contract clauses,
   declared once and reused. Today a `requires`/`ensure` clause is anonymous and inlined
   per-function; a `law` lifts it into a first-class, composable entity.
2. **`includes OtherLaw(args)`** — composition. The included law's clauses are flattened
   (with params substituted) into the including law. Composition is **conjunction**: an
   `includes` can only *add* obligations, never relax a parent.
3. **`fulfills Name(args)`** on a function — attaches the law's clauses to that function
   and obliges the compiler to discharge them.
4. **Frame conditions `changes {place…}` / `preserves {place…}`** — *new and the
   powerful part*: a bound on **what state the function may mutate**. `BasicMovement`
   changing only `position` and preserving `health/velocity/sprite/team` is what stops an
   AI from "accidentally" mutating unrelated state. (§5 — this rides on analysis we are
   already building.)
5. **Verification strengths `prove` / `test` / `inspect`** — a law clause declares *how*
   it is discharged: statically proven, property-tested, or surfaced as a human/AI review
   obligation. Not every visual game property is statically provable; the strength makes
   that explicit instead of silently unchecked.

## 4. Semantics (desugaring-first, matching Elisa's pattern)

Elisa's recurring lever is **pure desugaring** (comprehensions, fold, the grammar DSL).
The law algebra follows suit wherever possible:

- A `law` is **not** a runtime object. It is a named template of clauses.
- `fulfills L(args)` **desugars** the substituted `require`/`ensure` clauses of `L` into
  the function's existing precondition/postcondition lists — reusing the working contract
  runtime verbatim. `includes` flattens transitively first.
- `changes`/`preserves` lower to frame assertions: snapshot `old(p)` for each preserved
  place `p`, assert `p == old(p)` at every exit (debug); statically discharge where the
  mutation/alias analysis can prove the function never writes `p` (§5).
- `forbid`/`effects` clauses constrain the function's effect-grant set — they *are* the
  existing effect system, named and composed.
- Strengths map to existing machinery: `prove` → static contract discharge / type-level
  facts; `test` → generated `@property` cases; `inspect` → a recorded review obligation
  emitted into tooling (never a silent pass).

A `fulfills` is **verified, not asserted**: every clause must be discharged by *some*
strength, or the compiler errors — composition can never launder an unchecked claim.

## 5. Convergence with the noalias / disjointness work

The frame conditions are not a new soundness island — they ride the **same substrate**
we are already building for sound vectorization:

- `preserves entity.velocity` is the statement "this function does not mutate
  `entity.velocity`." Proving it statically is a **mutation + aliasing** question — the
  exact analysis behind `proven_distinct` / the borrowed-owner mutation tracking. A
  `preserves` that the analysis can discharge needs no runtime check; one it cannot falls
  back to the debug snapshot.
- `proven_distinct(a, b)` — the derived buffer-disjointness predicate (docs/84) — *is
  itself a law*: a named, checked, composable property with `prove` strength. The optional
  `disjoint(a, b)` assertion (docs/84 §3.4) is the `law`-algebra's checked-assertion form.
- `forbid Time.Now / Random.Unseeded` (`DeterministicUpdate`) is the effect system.

So the algebra is mostly a **naming + composition layer over machinery Elisa already has**
(contracts, effects, mutation/alias analysis), plus the frame-condition lowering. One
source of truth; the laws compose the facts the compiler already computes.

## 6. Soundness & the four principles

- **Checked, never trusted.** A law is debug-checked by default (like today's contracts),
  statically proven where the analysis allows, `@property`-tested where requested, and
  `inspect` is an *explicit* obligation surfaced to a human/AI — never a silent pass. This
  mirrors the noalias rule: emit the guarantee only when discharged, never on a guess.
- **Composition only conjoins.** `includes`/`fulfills` add obligations; nothing relaxes a
  parent law. A bigger law is strictly stronger than its parts.
- **Pit of success.** Naming a law and writing `fulfills` is less work than re-deriving the
  invariants per function, and the composition tree doubles as the design doc — the easy
  path is the correct one (principle 3).

## 7. Implementation ladder (bottom-up, each independently shippable)

- **Stage 0 — today.** Atoms work: `requires`/`ensure`/`old`/`result`/`invariant`/
  `@property`/effect grants.
- **Stage 1 — `law` + `includes` + `fulfills`, pure desugaring.** Named clause bundles;
  `includes` flattens; `fulfills` lowers the substituted `require`/`ensure` into the
  function's existing contract lists. **No new runtime.** Demonstrable end-to-end on the
  `FinitePosition` → `ValidDeltaTime` → `BasicMovement` → `update_position` example. This
  is the minimal slice that proves the algebra.
- **Stage 2 — frame conditions.** `changes`/`preserves`, debug-checked via `old`-snapshot
  first; then statically discharged through the mutation/alias analysis (§5).
- **Stage 3 — effect laws.** `forbid`/`effects` clauses compose onto the effect-grant
  system (`DeterministicUpdate`, `RenderDoesNotMutateGameState`).
- **Stage 4 — verification strengths.** `prove` / `test` / `inspect` selectors mapping to
  static discharge / `@property` generation / review obligation.
- **Stage 5 — quantified laws + tooling.** `ensure each x in xs`, `includes each layer`;
  the composition-tree diagnostic and the law-path failure format ("Failed law:
  `ParallaxScrolling > DeeperLayersMoveSlower`") as the AI-repair map.

## 8. Open questions / risks

- **`law` as a keyword** — contextual (like `requires`), to avoid breaking identifiers.
- **`fulfills` discharge policy** — default to debug-runtime-checked (matches existing
  contracts), escalate to static where provable, require an explicit strength for
  visual/untestable properties. Never silently skip.
- **Frame conditions over collections / aliasing** — `preserves layers.world_position`
  across a `darray` needs the aliasing analysis; debug-check first, static later. This is
  the hardest piece and the one that most depends on the noalias substrate.
- **Quantified composition** (`includes each`) — desugar to a loop of clause instantiations;
  watch for O(n²) pairwise laws (`DeeperLayersMoveSlower` over all layer pairs).
- **Scope discipline** — keep laws a *desugaring + checking* layer; resist turning them
  into a runtime reflection system. The value is compile-time guarantees, not a framework.

## 9. Recommendation

Build **Stage 1** as the first concrete slice: `law` / `includes` / `fulfills` desugaring
to the existing contract runtime, validated on the movement-law composition. It proves the
algebra with near-zero new runtime risk and immediately demonstrates the bottom-up
composition the discussion is after. Frame conditions (Stage 2) are the high-value next
step and the point where this work and the noalias/mutation analysis become one substrate.
