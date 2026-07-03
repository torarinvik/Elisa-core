# 118 — Interprocedural termination: proving progress-through-mutation

A machine-checked, **zero-runtime-overhead** termination proof for recursion whose
measure decreases via *side effects on a by-reference parameter* — the shape of every
recursive-descent parser (and stream consumer, tree-walker, work-list loop, …). Today
these can only be made safe with a runtime depth guard; this doc is the path to proving
them terminating at compile time.

Builds on: [86] (`decreases` + the bounded linear prover), [87] (`changes`/`preserves`
frame conditions), [111]/[112] (the ghost model — the erasure guarantee this design
leans on). Companion to those; nothing here changes existing behaviour (opt-in via
`decreases`, exactly as [86]).

## 1. The motivating case (and why it matters beyond the parser)

The self-hosted stage1 Elisa frontend (`Elisa Projects/Elisa-compiler`) is a hand-written
recursive-descent parser. A mutating fuzzer over its `lex → parse → check` pipeline found
`SIGSEGV`s on pathologically deep input (`((((…))))`, `a.b.b.b…`, nested `if`/`match`
thousands deep) — unbounded recursion exhausting a finite stack. That was fixed
pragmatically with runtime depth guards (bounded `Parser.depth` / `walk_depth` wrappers);
this doc is the *formal* companion: prove the recursion terminates so the guard is a
belt-and-suspenders backstop, not the only guarantee.

The parser recurses as `expr → unary_expr → postfix_expr → primary_expr → expr` (a 5-way
mutual cycle), and it makes progress by advancing `parser.position` — **inside** the
`advance()`/`expect()` primitives, on a `mutable Parser&`. The natural measure is
`parser.tokens.count - parser.position`, which strictly decreases at every recursive call
because at least one token was consumed on the path to it.

The current prover ([86]) cannot see this. Two walls, both verified empirically (§7):

- **Wall 1 (fundamental).** `decreases` is discharged by **substituting the recursive
  call's arguments** into the measure and showing `measure(params) - measure(args) > 0`.
  The parser passes the *same* `parser` ref every call, so `measure(args) == measure(params)`
  syntactically — the prover reports the measure *unchanged*. The decrease lives in a
  *state mutation* (`position` grew) performed by a *callee*, which argument substitution
  cannot express.
- **Wall 2 (mechanical).** For `decreases` on a function with no *direct* self-call the
  prover finds the mutually-recursive SCC (`collectRecursiveSCCEdges`) — but on the
  parser's cycle it returns no edges and emits `decreases … is unused`. Minimal cycles
  (direct, UFCS, through `match` arms, through `can` blocks) *are* detected; the parser's
  deep/wide descent is not. The exact trigger is unpinned (see §6, brick 118-0).

## 2. Why NOT the "fuel" workaround

The textbook trick — thread a `fuel: i64` decremented at each call and `decreases fuel` —
is **self-defeating under a zero-overhead requirement**:

- **Real fuel** (a live parameter) proves the real code but is an extra argument + a
  subtract per call → runtime overhead. Rejected.
- **Ghost fuel** (erased in release → zero overhead) proves a **fiction**: the erased
  version is not what runs. The caller picks the ghost fuel arbitrarily; nothing links it
  to the real recursion depth. Linking them needs a ghost invariant `fuel == tokens - pos`,
  and *maintaining* that invariant requires proving `position` advances — Wall 1 again.
  Circular. A zero-overhead ghost-fuel "proof" proves nothing about the executed program.

So the only path that is **both** zero-overhead **and** meaningful is to prove the *real*
measure. That is this design.

## 3. Design: compose callee `ensure` postconditions into the `decreases` proof

Two spec-only ingredients, both **fully erased in release** (they are contracts/ghost per
[87]/[111]; the `-O2`/release object contains no trace of them → the zero-overhead
requirement is met by construction):

1. **Progress contracts on the consuming primitives.** The token primitives gain a
   postcondition about the cursor. **The progress is conditional, not flat** — the single
   most important subtlety of this whole design, discovered while landing 118-0. `advance`
   does NOT strictly increase `position`; at end-of-input it stays put (you cannot advance
   past EOF), and that saturation is *exactly why the parser terminates* (the measure is
   bounded below at 0). So:

   ```
   def advance(parser: mutable Parser&) -> Token:
       ensure parser.position >= old(parser.position)                 # monotone (always)
       ensure parser.position <= parser.tokens.count                  # bounded (never past EOF)
       ensure old(parser.position) < parser.tokens.count => parser.position > old(parser.position)  # STRICT when a token remained
       …
   def expect(parser: mutable Parser&, kind: TokenKind) -> Token:
       ensure parser.position >= old(parser.position)                 # advances-or-stays (delegates to advance)
       …
   ```

   `accept`/`peek`/`cursor` get `preserves parser.position` (they must NOT be relied on for
   progress; `peek`/`cursor` genuinely preserve it, `accept` may advance so it gets the
   monotone form). The monotone/bounded halves are flat tier-2 facts checkable today ([86]);
   the **conditional strict** postcondition (`guard => strict`) is the piece that needs
   contract support for an implication postcondition — verify that spelling exists before
   118-1, else express it as a `where`-refined return or a lemma.

2. **A position-delta summary in the termination prover.** New machinery (this is the real
   work): when discharging `decreases M` at a recursive call, instead of *only* comparing
   `M(params)` to `M(args)`, the prover walks the straight-line path from function entry to
   the recursive call and accumulates the guaranteed change to each place mentioned in `M`,
   using **callee `ensure`/`changes` summaries** for calls on that path. If the accumulated
   fact entails `M` strictly decreased (here: `parser.position` strictly increased, `tokens.count`
   unchanged ⇒ `tokens.count - position` strictly decreased), the obligation discharges.

The measure stays the honest `parser.tokens.count - parser.position`; the *proof* is what
gets stronger. No parser refactor, no live fuel, no runtime cost.

## 4. Brick plan

> **STATUS (landed).** The stack is built end to end. The original 118-1 blocker
> ("no `old()`-vs-mutation-flow reasoning") turned out NOT to need new foundational
> machinery — the weakest-precondition engine (`analyzer_vc_wp.go`) already models
> `old()` as an entry snapshot, single-level `if`/`else` merges, and backward
> substitution. It only lacked FIELD places. So the realized bricks are:
>
> - **A — field places in WP** (`analyzer_vc_wp.go`, `analyzer_vc_ir.go`). A scalar
>   `p.field` of a `mutable T&` param lowers to a substitutable `vcVar` (was opaque),
>   so `ensure p.pos >= old(p.pos)` and the conditional-increment form now discharge.
>   Aliasing gate: field-place WP is admitted only with ≤1 reference param.
> - **B — `=>` implication postcondition** (both stage0 and stage1 parsers). Accepted
>   as a spelling of the existing `implies` infix (`(not A) or B`). Makes the
>   conditional-strict contract writable; the EOF-saturating branch discharges via the
>   false guard. stage1 parses+resolves it (no discharge there — stage1 has no prover).
> - **C/D — callee-summary decrease certificate** (`analyzer_termination_callee_summary.go`).
>   When the affine proof reports the measure unchanged (Wall 1), compose the consumer's
>   `ensure`/`changes`/`requires` into an SMT decrease proof over the two-symbol
>   entry/current model. Field-precise `changes p.pos` is what lets the unchanged places
>   (`stop`) cancel; a coarse `changes p` soundly declines. Pattern-restricted per §5.
>
> Original 118-1..4 map onto A+B (the primitives' contracts) and C/D (the composition).
> The one honest gap vs. the *full* real parser: the certificate handles a single
> straight-line-prefix consumer with pure early-exit guards; the mutual-recursion cycle
> (§4 118-3) and deeply-branched descent are the remaining extension surface.


- **118-0 — SCC detection for the real descent (Wall 2). [LANDED]** Root cause: the static
  call walker `walkStaticExpr` did not descend into the optional/error-unwrap and
  try/catch/match EXPRESSIONS (`get`/`else`/`try`/`catch`/`match`), so any call inside them
  was invisible to the termination call collector — and the parser's core recursion is
  `left = get parser.unary_expr()`. This was a **soundness hole**, not just a missed warning:
  a function with a visible decreasing call and a hidden diverging one (`get f(n+1)`) was
  UNSOUNDLY proven terminating. Fixed by adding the five cases to `walkStaticExpr`; the
  parser cycle is now detected (falls through to Wall 1). Regression tests
  `TestTerminationHiddenGetCallRefuted` / `TestTerminationGetHiddenCycleDetected`. Strict
  improvement to every consumer of the walker (structural induction + several lints).
- **118-1 — `ensure`/`preserves` on the token primitives. [BLOCKED — needs a prover
  prerequisite].** Verified empirically (2026-07-03) that the contracts do NOT check today,
  for two independent reasons, both foundational rather than bounded:
  1. **No `old()`-vs-mutation-flow reasoning.** Even the flat `ensure p.pos >= old(p.pos)`
     on `advance` (a plain `if p.pos < p.len: p.pos <- p.pos + 1`) is rejected —
     *"could not be proven statically … it can fail when p.pos=0"* — with `pos: usize`. The
     postcondition prover does not relate `old(p.pos)` to the value after a *conditional
     field mutation*. This is the very same limitation as Wall 1, surfacing in the `ensure`
     checker. Until it is fixed, `advance` cannot carry *any* useful progress contract.
  2. **No implication postcondition.** `ensure GUARD => POST` is a syntax error
     (`unexpected token =>`), so the conditional-strict form (§3.1) cannot even be written.
  So 118-1 is gated on a **prerequisite feature**: postcondition reasoning over conditional
  mutations relative to `old()` (an extension of [87]/[the contract system], likely its own
  brick/doc), plus implication-postcondition surface syntax. This is the true bottom of the
  stack — do it before 118-1, and 118-2 inherits the same `old()`-vs-mutation machinery.
- **118-2 — callee-summary composition in the decrease proof (Wall 1). [stage0, the core].**
  Teach `directNumericTerminationCertificate` / `recursiveCallCertificate` to consult callee
  `ensure`/`changes` summaries along the entry→call path and fold their guaranteed
  place-deltas into the measure comparison. Start with the single, sound, high-value pattern:
  *scalar field of a `mutable T&` param, strictly monotone via a callee whose `ensure` says
  so*. Reject (soundly) anything outside the pattern. **Because progress is conditional
  (§3.1), 118-2 must also thread the guard**: the strict-increase fact only fires when the
  callee's precondition-side guard (`position < tokens.count`, i.e. the consumed token was
  not EOF) holds at the call site — which the parser establishes by construction (it only
  recurses after matching a real, non-EOF token). So the composition is: `guard at call site`
  ∧ `callee (guard => strict)` ⊢ `strict decrease`. The guard fact comes from the same flow
  analysis [86]'s guard-if prover already does for loop measures — reuse it, don't rebuild.
  This is the crux and the bulk of the effort.
- **118-3 — compose 118-2 around the mutual cycle.** Extend `mutualRecursionVerified` so a
  cycle discharges when the *summed* per-edge place-delta strictly decreases the shared
  measure (not only when each edge decreases in isolation) — the distributed-decrease case
  the parser needs (progress happens at *some* edge, not every edge).
- **118-4 — apply to the frontend + lock it.** Put `decreases parser.tokens.count -
  parser.position` on the parser's recursive functions, prove it, and add a regression
  guard (a fixture that must keep proving) so a future edit that breaks progress fails the
  build, not the fuzzer.

## 5. The soundness obligation

For 118-2/118-3, the fact fed into the measure comparison must be a **guaranteed** change,
not a possible one: use only `ensure` clauses that hold on *every* path through the callee
(unconditional postconditions), and only compose along the *must-execute* straight-line
prefix from entry to the recursive call (no conditional/short-circuited calls). A
`mutable T&` aliasing the measured place through *another* parameter breaks the summary —
gate on the non-aliasing already required by [87]'s `changes` discharge. Unsound
composition is worse than no proof; when in doubt, fail closed (report non-decreasing).

## 6. Honest limitations

- 118 proves *termination*, not *bounded stack depth*. The parser already provably
  terminates once 118 lands, yet still needs the runtime depth guard as a finite-stack
  backstop — a 10⁵-deep-but-finite recursion terminates and still overflows. Keep both.
- The composition is deliberately pattern-restricted (monotone scalar field via
  unconditional `ensure`). General interprocedural measures (containers shrinking, heap
  reachability) are out of scope and route through [116]'s framing model instead.
- 118-0's fix may reveal that some parser functions are in the SCC but lack `decreases`;
  the all-members-annotated rule ([86]) then applies — 118-4 must annotate the whole cycle.

## 7. Evidence (probes run 2026-07-03, stage0 = Go elisac)

- `decreases n` with `return f(n+1)` → rejected: *"measure `n` non-decreasing across the
  recursive call: n -> (n + 1)"*. Prover is real, runs by default, precise.
- Direct self-recursion, recursion inside `match` arms, inside `can` blocks, and via UFCS
  (`(n-1).pong()`) all discharge correctly.
- `decreases p.len - p.pos` with `p.pos <- p.pos + 1` *directly* before `rec(p)` → rejected
  as `(p.len - p.pos) -> (p.len - p.pos)` **unchanged** — Wall 1, even with the mutation in
  the same body. (The parser's mutation is one level worse: inside `advance()`.)
- `decreases parser.tokens.count - parser.position` on the real `expr_body` → warning
  *"makes no direct recursive call; the termination clause is unused"* — Wall 2.
- `grep` of `analyzer_termination.go` + `analyzer_termination_mutual.go` for
  `ensures|postcond|callee.*contract|interprocedur` → **no matches**: the decrease proof is
  argument-substitution only; interprocedural-contract composition (118-2) is unbuilt.

## See also

- [86] `decreases` + tier-2 linear prover — the substitution model 118-2 extends.
- [87] `changes`/`preserves` — the frame vocabulary the callee summaries reuse; §4/§5 note
  `preserves`/interprocedural as remaining, which 118-2 is one concrete consumer of.
- [111]/[112] ghost model — the erasure guarantee that makes the contracts zero-overhead.
- [116] heap & framing decision record — where non-scalar interprocedural measures route.
