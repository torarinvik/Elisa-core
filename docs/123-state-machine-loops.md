# docs/123 — `machine`: Checked State-Machine Loops

A restriction construct for loop-shaped state machines (scanners, parsers, protocol
drivers). A `machine` block is not a more powerful loop — it is a deliberately **less**
powerful one: each arm is exactly one (state, input) decision ending in exactly one
transition or exit, and the compiler refuses everything else. General `match` + `while`
remain the flexible tools; `machine` is the constrained rung between them and `for`.

Status: **SHIPPED** (Phases 0/1/4/5 — parser, scalarized desugar, refusals, both
stages; this header long lagged §10, which has the truthful per-phase record). The
stage1 lexer's 7 dispatchful scanners are production `machine`s, bit-exact by the
stage0/stage1 token-parity harness. Remaining: Phase 2 graph artifacts (dead states,
tag-coverage totality for closed-enum inputs — see docs/125 §9, duplicate-arm
rejection §5.5) and Phase 3 verification hooks.

---

## 1. Motivation — restriction as the feature

Elisa already trades freedom for guarantees on a ladder of loop forms:

```
comprehension  <  for x in coll  <  ???  <  while + match  <  can ComplexFlow
```

- `for` is a restricted `while`: guaranteed termination shape, no index bugs.
- Comprehensions are restricted loops: guaranteed shape, auto-vectorization.
- `lmut` is restricted `mutable&`; safe concurrency sugar replaced raw spawn.

The missing rung is the **typed automaton**: a loop whose body is a single dispatch over a
named state, every path advancing and transitioning exactly once. The flow lints
(docs/121 R1–R6) approximate this shape *from the outside* with calibrated heuristics; a
construct **defines** it, so inside a `machine` there is nothing left to lint. R1–R6 are
the evidence this shape matters; `machine` is the destination their diagnostics can point
to.

The construct is opt-in and maximally strict *because* general `match` exists next door
(principle 3: make the sound subset ergonomic; the general tool stays for everything
else).

Two additional payoffs the lint tier can never deliver:

- **A closed system for the prover.** State payloads are the only loop-carried data, and
  transitions are the only writes to them. Termination = a `decreases` measure over
  states/payloads; invariants = refinements on payloads, checked at every transition edge.
  A general `while` can mutate anything anywhere; a `machine` hands verification a finite
  transition graph.
- **State × input exhaustiveness.** Ordinary match exhaustiveness covers variants;
  a `machine` must cover every input in every state, and unreachable arms are errors.

---

## 2. Syntax (as implemented)

```
machine over INPUT_EXPR [while COND_EXPR] [-> YIELD_EXPR]:
    state Name1
    state Name2(payload: Type where REFINEMENT, ...)
    ...
    start NameK(args)

    Name1, PATTERN [if GUARD]:
        <straight-line statements>
        -> NameJ(args)        # transition (tail position, mandatory)

    Name2(payload_pat), PATTERN:
        return EXPR           # or: break (bare), return
```

Two clauses were added during implementation (both discovered against the real stage1
lexer, not invented):

- **`while COND`** — the loop condition. Every real scanner needs one (`while not
  lexer.is_end_of_source()`): without it, `over lexer.current_char()` reads past EOF.
  When the condition fails, control falls out of the machine to the following statement
  (the pilot's post-loop unterminated-token return). Omitted → `while true`.
- **`-> YIELD` replaces the spec's `break value`** — `break` carries no value anywhere in
  Elisa; loops yield via the docs/119 header-arrow instead, evaluated against the final
  payload state on exit. A yield-form machine is consumed as a decl RHS exactly like a
  value loop:

  ```
  total: usize =
      machine over source[cursor] while cursor < source.count -> count:
          state Skipping(count: usize)
          start Skipping(0)
          Skipping(count), ' ':
              cursor <- cursor + 1
              -> Skipping(count + 1)
          Skipping(count), _:
              break
  ```

- **`machine over EXPR:`** — `EXPR` is the *input expression*, re-evaluated once per
  step (e.g. `lexer.get_char()`). The input is machine input, **not** state payload — no
  arm re-fetches or re-packs it (that re-packing was the duplicated-advance shape R3
  kills).
- **`state` declarations** — a private enum scoped to the machine. Payloads may carry
  refinements (`depth: usize where depth > 0`); illegal states are unrepresentable.
- **`start State(args)`** — the initial state; args checked against payload refinements.
- **Arms match the pair** `State(payload_pat), input_pat:` — reusing tuple-pattern
  syntax. Or-patterns on the input slot come free (`' ' | '\t'`). An optional arm guard
  `if COND` follows the pattern (§8 prerequisite).
- **`-> State(args)`** — the transition. Only declared states are valid targets; the
  arrow reads declaratively and deliberately is not `goto` (it cannot jump into code,
  only select the next state).
- **Exits** — `return EXPR` leaves the enclosing function; `break EXPR` yields the
  machine's value in expression position (docs/119); bare `break` in statement position.

### 2.1 Explicit self-transitions

Self-transitions are **mandatory and explicit** (`-> Text`, not implicit fall-through).
Rationale: the classic automaton bug is a wrong-*target* transition (observed three times
in the motivating sketch — nested-string arms escaping to `Text` and dropping `depth`).
If "stay" were implicit, a forgotten arrow silently means self-transition and the intent
is invisible. Forcing every arm to name its successor makes wrong targets reviewable and
lets the checker construct the complete transition graph. Restriction construct →
explicit beats convenient.

---

## 3. Worked example — f-string scanner

```
def fstring_scan(lexer: lmut Lexer) -> Token:
    machine over lexer.get_char():
        state Text
        state Expr(depth: usize where depth > 0)
        state StringInExpr(depth: usize where depth > 0)

        start Text

        Text, '"':
            return fstring_token()

        Text, '\\' if lexer.next_is_end_or_newline():
            return fstring_token()

        Text, '\\':
            lexer <- lexer.advance(2)
            -> Text

        Text, '{' if lexer.peek(1) == '{':
            lexer <- lexer.advance(2)
            -> Text

        Text, '{':
            lexer <- lexer.advance(1)
            -> Expr(1)

        Text, '}' if lexer.peek(1) == '}':
            lexer <- lexer.advance(2)
            -> Text

        Text, _:
            lexer <- lexer.advance(1)
            -> Text

        Expr(depth), '{':
            lexer <- lexer.advance(1)
            -> Expr(depth + 1)

        Expr(1), '}':
            lexer <- lexer.advance(1)
            -> Text

        Expr(depth > 1), '}':
            lexer <- lexer.advance(1)
            -> Expr(depth - 1)

        Expr(depth), '"':
            lexer <- lexer.advance(1)
            -> StringInExpr(depth)

        Expr(depth), _:
            lexer <- lexer.advance(1)
            -> Expr(depth)

        StringInExpr(depth), '\\':
            lexer <- lexer.advance(2)
            -> StringInExpr(depth)

        StringInExpr(depth), '"':
            lexer <- lexer.advance(1)
            -> Expr(depth)

        StringInExpr(depth), _:
            lexer <- lexer.advance(1)
            -> StringInExpr(depth)
```

Note the refinement discharge: `Expr(1), '}'` is the *only* exit to `Text`, and
`Expr(depth > 1), '}'` → `Expr(depth - 1)` satisfies `depth > 0` automatically. The
`depth == 0` state cannot be constructed, so brace balancing is enforced by type, not by
convention.

### 3.1 Expression form

```
def skip_trivia(lexer: lmut Lexer) -> usize:
    count = machine over lexer.get_char():
        state Skipping(count: usize)
        state InComment(count: usize)

        start Skipping(0)

        Skipping(count), ' ' | '\t' | '\n':
            lexer <- lexer.advance(1)
            -> Skipping(count + 1)

        Skipping(count), '#':
            lexer <- lexer.advance(1)
            -> InComment(count)

        Skipping(count), _:
            break count                    # the machine's value

        InComment(count), '\n':
            lexer <- lexer.advance(1)
            -> Skipping(count + 1)

        InComment(count), _:
            lexer <- lexer.advance(1)
            -> InComment(count)
```

---

## 4. Semantics via desugar (zero overhead) — as implemented

The desugar is **scalarized**: the synthesized mode enum is payload-LESS, and every
payload field becomes one loop-header mutable local, shared across states that declare
the same field name (same type required). This was forced by two stage0 facts and turned
out strictly better than the spec sketch:

- stage0 enum payload decls carry no `where` refinements — but a **local's type** can be
  a refinement type (`depth: usize where depth > 0`), so scalarized payloads get their
  refinements edge-checked by the existing typed-local machinery for free (§5.7 without
  machine-specific code);
- the payload-less enum + scalar accumulators is the read_fstring pilot's exact
  bit-pattern, so zero overhead is inherited, not re-proven.

The actual lowering of §2's brace counter (real `-emit lowered` output shape):

```
enum __MachineMode_<uniq>:            # hoisted to file scope, payload-less
    Text
    Expr

# wrapLoopHeader scoped wrapper (docs/119) holds the machine-private decls:
__machine_mode = __MachineMode.Text   # `start Text(0)`
found: mutable i64 = 0                #   start arg seeds the payload local
depth: mutable usize = zeroed         #   non-start payloads init zeroed
while cursor < source.count:          # the `while` clause (default: true)
    __machine_input = source[cursor]  # the `over` expression, once per step
    match __machine_mode:
        __MachineMode.Text:
            if __machine_input == '{' and cursor + 1 < source.count and ...:
                cursor <- cursor + 2                     # self-transition: mode rebind ELIDED
            elif __machine_input == '{':
                cursor <- cursor + 1
                depth <- 1
                __machine_mode <- __MachineMode.Expr     # `-> Expr(found, 1)`
            else:
                cursor <- cursor + 1
        __MachineMode.Expr:
            ...
found                                  # `-> found` yield = ExprBlock value
```

Per-state arm chains lower to one `if`/`elif`/`else` ladder whose conditions are
`payload-pattern AND input == lit [or ...] AND guard`; payload literal patterns
(`Expr(found, 1)`) become equality tests, payload refinement patterns (`Expr(found,
depth > 1)`) are used verbatim as conditions over the scalar local. Self-transitions
elide the mode rebind and `field <- field` no-op assigns; multi-arg transitions evaluate
args into temps before any field assign (so `-> Swap(b, a)` reads pre-transition values).
The whole thing rides `wrapLoopHeader`, so licensing/analysis/codegen are the tested
docs/119 paths. Verified: `-Wflow-strict` is silent on the lowering, and the native
`-emit test` smoke (multi-state, guards, literal + refinement payload patterns, yield
form, unterminated input) passes.

---

## 5. What the construct refuses

These are the construct's identity — each is a hard compile error inside `machine`:

1. **Branching in arm bodies.** No `if`/`match`/loops in an arm — *zero*, not "depth ≤ 3".
   All discrimination lives in the pattern pair + arm guard.
   ```
   Text, c:
       if c == '{':          # ✗ machine arms cannot branch;
           -> Expr(1)        #   split into `Text, '{':` and `Text, _:` arms
       -> Text
   ```
2. **Arm without a decision.** Every arm ends in exactly one of `->` / `return` /
   `break`, in tail position.
   ```
   Expr(depth), '}':
       lexer <- lexer.advance(1)   # ✗ arm does not end in ->/return/break
   ```
3. **Foreign mutation.** Arm bodies may mutate only (a) the driven `lmut` resource(s)
   appearing in the `over` expression and (b) state payloads via `->`. No captures, no
   globals, no out-params.
   ```
   Skipping(count), _:
       total <- total + count      # ✗ machine arms may only mutate the driven
       break count                 #   lmut resource and state payloads
   ```
4. **Non-exhaustive state × input.** Every declared state must handle every input value
   (a final `State, _:` arm per state discharges it). A state with no arms is an error.
5. **Unreachable arms.** An arm shadowed by earlier patterns/guards is an error, not dead
   code. **KNOWN GAP (verified 2026-07-11): only the irrefutable-arm-before-others case
   is caught today — two literally identical arms (`Scanning, 'm':` twice) pass
   silently. General pairwise reachability is Phase 2 work, alongside tag-coverage
   (docs/125 §9.1).**
6. **Undeclared transition targets.** `->` may only name a declared `state`.
7. **Refinement violations at edges.** Every `-> State(args)` must discharge the target
   payload refinements (via the arm's pattern facts, e.g. `Expr(depth > 1)` proving
   `depth - 1 > 0`).

Escape in the other direction: if the logic genuinely doesn't fit, don't fight the
construct — use plain `while` + `match` (and `can ComplexFlow:` if the flow lints
object). `machine` is opt-in; there is no pressure to force code into it.

---

## 6. Verification hooks

- **Termination**: `machine` composes with `decreases` (docs/78/118) — the measure ranges
  over states/payloads, and the transition graph gives the prover exactly the edges to
  check. A machine whose `over` expression drives an `lmut` resource forward (e.g.
  `lexer.advance`) can alternatively discharge via the resource's progress measure.
- **Invariants**: payload refinements are edge-checked (§5.7); machine-level invariants
  (facts over all states) are expressible as refinements repeated on each payload, or —
  future — a `machine ... where INV:` clause.
- **The transition graph is a first-class artifact**: reachability (dead states),
  totality, and refinement edges are all decidable checks over a finite graph.

---

## 7. Relationship to docs/121 and docs/122

- **docs/121 (flow lints)**: the lints are the *pressure*, `machine` is the *destination*.
  R2's message should eventually suggest `machine` instead of prescribing a hand-rolled
  shape. Inside a `machine`, R1–R6 are unviolatable by construction and do not run.
- **docs/122 (pattern matching)**: `machine` consumes the pattern grammar for its arm
  pairs. Two wanted features (§8) are prerequisites and initially scoped to `machine`;
  they may generalize to `match` later if they earn it (arm guards are docs/122 §5.1).

---

## 8. Prerequisites (status: 1 and 2 LANDED in Phase 0; 3 partially)

1. **Arm-header guards** — `State(pat), input_pat if COND:` (docs/122 §5.1). ✅ DONE.
   Guarded arms are non-covering for exhaustiveness.
2. **Payload refinement patterns** — `Expr(depth > 1)` + payload literals `Expr(1)`.
   ✅ DONE (via the scalarized desugar, §4).
3. **State × input exhaustiveness** — per-state coverage of the input domain. PARTIAL:
   the per-state irrefutable-final-arm rule ships; true domain totality is Phase 2.
   For closed-enum inputs the cheap complete form is tag-coverage (docs/125 §9.1) —
   strictly easier than the general char-range problem and it removes the wildcard
   mandate that erases the add-a-variant safety net.

**Verified arm-pattern matrix (2026-07-11):** literals ✓, alternation `'a' | 'b'` ✓,
input bind + guard (`c if c.is_digit()`) ✓, enum tags ✓ (an enum-returning scrutinee
dispatches today — docs/125 §9), payload literal/refinement patterns ✓, `_` ✓
(mandatory per state); **ranges `'0'..='9'` / `'0'..<'9'` ✓ (LANDED, this pass)** —
the docs/122 §5.2 range grammar now parses in machine arm headers and lowers to a
bounds test (`lo <= input and input <(=) hi`) OR'd with the arm's literal alternatives,
so `Num, '0'..='9' | 'a'..<'g' | '_':` is one arm. Bound spelling is explicit (`..=`
inclusive / `..<` exclusive; bare `..` is a hard error), matching match/when. This
unifies the pattern grammar across all three dispatch constructs (roadmap Phase-1 step 1).

---

## 9. Open questions

1. **Keyword** — `machine` (current pick) vs `automaton` vs overloading `state` as the
   block keyword. `machine` is short and unambiguous; `state` is already the per-state
   declarator.
2. **Multiple driven resources** — `over (a.next(), b.peek())`? Start with one input
   expression; tuples of inputs are a natural extension since arms already match pairs.
3. **Epsilon steps** — arms that transition *without* consuming input (re-dispatch on the
   same input). Classic automata need them; the desugar would skip the `__in` refresh.
   Proposal: `->> State(args)` or defer entirely (the pilot didn't need it).
4. **`break` from Text-position machines in statement form** — statement-position
   machines with no value: is bare `break` the only non-`return` exit, and does the
   machine require at least one exit arm (else provable non-termination unless
   `spawn_daemon`-like)?
5. **Nested machines** — a machine arm cannot contain a loop (§5.1), which includes
   another machine. Keep it that way; factor sub-machines into functions.

---

## 10. Phasing (status)

- **Phase 0 — DONE.** Arm guards + payload literal/refinement patterns + input BIND
  patterns (`Scanning, character if character.is_ident():`), scoped to `machine`.
- **Phase 1 — DONE.** Parser + scalarized desugar (§4), refusals §5.1–5.3/5.6, plus
  §5.4/5.5 in their per-state form (irrefutable-final-arm exhaustiveness, unreachable
  arms) and §5.7 via refinement types on the scalarized locals.
- **Phase 2 (remaining)** — transition-graph artifacts: dead-state reachability
  (DONE for `machine from`, R4), input-domain totality beyond the wildcard rule
  (for closed-enum inputs: tag-coverage, docs/125 §9.1 — makes the wildcard
  optional and discouraged; still TODO). **Duplicate-arm rejection — LANDED**
  (2026-07-11): two unconditional arms in one state that handle the same literal
  or enum tag (`Go, 'm': … Go, 'm': …`) are rejected as unreachable. Exact-match
  only (0-FP): a guard or a distinguishing payload condition exempts the arm, and
  overlapping ranges are not compared. Tests `TestMachineRefusalDuplicateLiteralArm`,
  `TestMachineGuardedArmSharesLiteralAccepted`.
- **Phase 3** — verification hooks (§6): `decreases` over states, graph artifacts.
  For `machine from` cycles, presence of `decreases` is already required (R3);
  discharge should wire into the EXISTING provers (4-increment + docs/118
  summaries) rather than new machinery.
- **Phase 4 — DONE (dogfood).** The stage1 lexer's 7 dispatchful scanners are machines:
  read_fstring (3-state, the FStringMode enum retired), read_string, read_char,
  skip_block_comment, skip_spaces, measure_indent (yield form), read_identifier.
  Bit-exact by the stage0/stage1 token-parity harness (13 fixtures). Condition-only
  scans (digit runs, blank-line/newline collapse, line-directive probes) deliberately
  stay plain loops — no body dispatch means a machine adds only boilerplate.
- **Phase 5 — DONE.** Stage1 self-hosted parser port (parser_stmt_machine.elisa):
  same desugar shape with an `if mode == Enum.State:` dispatch chain; breadth
  self-parse 37 files / 0 errors, all 30 parity smokes green.
- **Remaining** — docs/121 R2 diagnostic rewording to suggest `machine`.

---

## 11. Cross-references

- [docs/121](121-flow-checked-loops.md) — flow lints; the read_fstring pilot proving the
  desugar shape zero-overhead and bit-exact.
- [docs/122](122-pattern-matching.md) — pattern grammar; §5.1 arm guards, §5.6 deep
  exhaustiveness.
- [docs/119](119-expression-unification-and-explicit-mutation.md) — `break value` /
  loops as expressions; `<-` rebind.
- [docs/120](120-declared-lmut-threading-and-multi-place-assign.md) — `lmut` driven
  resources.
- [docs/78](78-termination-measures-and-debug-erasure.md),
  [docs/118](118-interprocedural-termination-via-ensures.md) — termination measures.
