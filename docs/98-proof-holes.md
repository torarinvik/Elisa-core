# 98 — Proof holes + missing-fact suggestions

Status: increment 1 landed (`feature/proof-holes`).

## Motivation

The SMT discharge tier (docs/90) and the linear/interval tiers can already *decide* most
obligations, and on failure the SMT tier produces a satisfying-assignment counterexample
(`a.lastSMTCounterexample`, surfaced via `counterexampleSuffix`). But a raw counterexample
model ("it can fail when i = 4, n = 4") tells the programmer *that* it fails, not *why the
prover lacked the fact* nor *what to add*. That turns verification into "feeding the solver"
— a frustrating guessing game.

Proof holes flip this around. When an obligation cannot be discharged, the compiler prints a
**constructive** diagnostic built from information the analyzer *already computes*:

1. **GOAL** — the proposition to prove, in source form.
2. **KNOWN FACTS** — the facts currently in scope (interval `rangeFacts` + boolean
   `smtAssertFacts`), rendered as a readable list. This is the prover's actual hypothesis set.
3. **SUGGESTED MISSING FACT** — a heuristic guess at the invariant/precondition/lemma whose
   absence blocks the proof (e.g. "no fact bounds the upper end of `i`; add a loop invariant
   `i <= n` or a precondition `requires i < n`").

This is "programming with an assistant": the diagnostic reads like a proof sketch with one
hole, and names the hole.

## The diagnostic format

```
proof hole: assertion could not be proven
  goal:        i < n
  known facts:
    - i >= 0
    - n >= 1
  suggested:   no fact bounds `i` above; add a loop invariant `i <= n`
               or a precondition `requires i < n`
```

Rendering rules:
- The goal is `unparse.FormatExpr(cond)`.
- Known facts are gathered from the in-scope `rangeFacts` (each known bound becomes one
  `name >= lo` / `name <= hi` line) and `smtAssertFacts` (each boolean fact formatted via
  `unparse.FormatExpr`), de-duplicated, closer scopes shadowing outer, emitted in a stable
  sorted order so the text is deterministic (same determinism contract as the SMT cache key).
- "known facts: (none in scope)" when the hypothesis set is empty.
- The `suggested:` line is omitted when no heuristic fires (never fabricate a guess).

## `assert ?` hole semantics

`assert(cond)` is normally a *debug runtime check* — it is not statically discharged, only
recorded as a downstream fact. Proof holes attach to it as follows:

- Under `-strict -smt` (`EnforceStrictProofs` + `EnableSMT`), a plain `assert(cond)` whose goal is
  an integer comparison and that the prover cannot discharge now emits the structured proof-hole
  diagnostic (a hard error, consistent with `assert … by:`). The SMT tier is required because it is
  the only in-tree prover that consumes the analyzer's full in-scope hypothesis set (flow facts +
  asserts + intervals); the linear `proveRequiresClause` tier is built for callee-requires
  substitution and would reject facts a strict author legitimately established. Outside `-strict`,
  or with SMT off, the behaviour is unchanged: the assert stays a runtime check, no new noise — so
  no existing strict-without-z3 build regresses.
- An *explicit* hole — `assert ?` — asks the compiler "what can you prove here, and what is
  missing?" It always prints the goal/known-facts report at its position, treating the goal as
  the literal placeholder. The bare-`?` spelling needs a parser token that does not collide with
  the heavily-overloaded `?` (optional / recovery / postfix-cast). Increment 1 therefore ships
  the *diagnostic engine* and wires it to the strict-assert path; the bare-`?` surface syntax is
  deferred to increment 2 (a dedicated `AssertHoleStmt` keyword/token), so no risky parser change
  lands first.

## Suggestion heuristics (increment 1)

The first heuristics inspect the **goal shape** against the **known-fact set**:

1. **unbounded-index / missing upper bound.** Goal is `a < b` (or `a <= b`) where the upper
   side `b` has no `rangeFacts` upper bound and no asserted fact constrains `a` against `b`:
   suggest a loop invariant or `requires a < b`. This is the canonical array-index obligation
   `i < n`.
2. **missing lower bound.** Goal is `a >= b` / `a > b` (commonly `i >= 0`) and `a` has no known
   lower bound: suggest `requires a >= b` or establishing it before the loop.
3. **missing loop invariant.** When the obligation sits inside a loop body (the goal mentions a
   loop induction variable that has a `rangeFacts` lower bound but no upper bound), suggest
   adding `invariant <goal>` to the enclosing loop.

Heuristics are *advisory only* — they never affect soundness (only `unsat` ever concludes a
proof). A wrong suggestion costs nothing but the reader's glance.

## Next increments

- Increment 2: `assert ?` surface syntax (dedicated token, `AssertHoleStmt` AST node), and an
  `--explain-hole` mode that prints the report for any obligation, not just failed ones.
- Increment 3: lemma-shaped suggestions (when the goal matches a known `law`'s body, suggest the
  lemma call); transitive-fact suggestions (chain `a <= b`, `b <= c` ⇒ propose `a <= c`).
- Increment 4: rank multiple candidate suggestions; integrate with the counterexample so the
  suggestion is consistent with the falsifying model.
