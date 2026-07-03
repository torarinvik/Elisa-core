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
   postcondition that they advance the cursor, e.g. on the stage1 side:

   ```
   def advance(parser: mutable Parser&) -> Token:
       ensure parser.position > old(parser.position)
       …
   def expect(parser: mutable Parser&, kind: TokenKind) -> Token:
       ensure parser.position > old(parser.position)   # expect always advances (consumes or errors+advances)
       …
   ```

   `accept`/`peek`/`cursor` get `preserves parser.position` (they must NOT be relied on for
   progress). These are intraprocedurally checkable today ([87] brick 87-1 landed `changes`;
   the `>`-on-a-scalar-field obligation is exactly a tier-2 linear fact from [86]).

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

- **118-0 — SCC detection for the real descent (Wall 2). [prereq]** Pin why
  `collectRecursiveSCCEdges` yields 0 edges on the parser's cycle while minimal cycles
  detect fine. Likely a traversal/scale limit in the reach-root walk over a large call
  graph, or a resolve gap on a specific call shape in the chain. Fix so the cycle is seen;
  a genuinely-recursive `decreases` must never silently become `unused`. Standalone value:
  every mutual-recursion `decreases` benefits.
- **118-1 — `ensure`/`preserves` on the token primitives. [stage1]** Add the progress
  contracts (§3.1) to `advance`/`expect` and `preserves position` to the non-consumers.
  Intraprocedurally checkable now; lands independently as executable documentation of the
  cursor discipline even before the prover consumes it.
- **118-2 — callee-summary composition in the decrease proof (Wall 1). [stage0, the core].**
  Teach `directNumericTerminationCertificate` / `recursiveCallCertificate` to consult callee
  `ensure`/`changes` summaries along the entry→call path and fold their guaranteed
  place-deltas into the measure comparison. Start with the single, sound, high-value pattern:
  *scalar field of a `mutable T&` param, strictly monotone via a callee whose `ensure` says
  so*. Reject (soundly) anything outside the pattern.
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
