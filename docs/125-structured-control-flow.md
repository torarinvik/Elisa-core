# docs/125 — Structured Control Flow: Refusals as the Product

Status: **LANDED + ACTIVE** (originally DESIGN; this header lagged the tree — see §7
for per-increment status). Implemented on BOTH stages: postfix guards, the
ternary-requires-else invariant (code 138), `when` (R1/R2/R3 stage0-owned),
`with` (+R1), `machine from` (+state payloads, R5 declared out-edges), and the §6b
strict block-`if` ban under `-Wflow-strict` (calibrated 3/3-TP, 0-FP). Strict census
2026-07-11: 884 sites in stage1 `src/` (lexer 8→0 DONE; parser 201, semantic 675
remain). Active design increment: §9 classified dispatch.
Depends on: `machine over` (docs/123), pattern features (docs/122: guards, ranges,
rest/as-bindings), expression unification (docs/119: statement-position exprs,
block/loop exprs, `rebind`), interprocedural termination summaries (docs/118),
`decreases` + the 4-increment prover, the graduated-lint precedent (`-Wperf` +
`can Scalar`), the raw-concurrency-removal precedent (unsafe primitive → grant).

## 0. One-sentence pitch

Every degree of freedom in control flow is a place where intent and code can
silently diverge; this document carves out a **blessed control-flow subset**
whose constructs are valuable precisely for what they *refuse*, and demotes the
unrestricted forms to a visible grant (`can ComplexFlow`) — the same move that
regions made against dangling, affine handles against double-use, and safe
concurrency sugar against raw spawn.

## 1. Design principle

The claim: a `mutable bool` flag can be flipped anywhere, so the type system
cannot see that "set twice" is a bug. An `elif` ladder can have overlapping or
missing arms, so the compiler cannot distinguish "deliberately first-match-wins"
from "didn't notice the shadow." A nested-if ladder can act at depth 5, and
nothing distinguishes "we checked all six preconditions" from "we forgot the
seventh." In each case the language accepts a **superset of what the programmer
means**, and the gap is where mistakes live.

Therefore the design is **obligations-first**: each construct below is specified
by what it makes *unwritable*, with the diagnostic it emits. Syntax is packaging.

### 1b. Higher-order branching

The unifying principle. **First-order branching** (block `if`) selects the next
*statement*; after the join the program is back in the undifferentiated
"anything next" state — the decision's outcome evaporates, which is exactly why
flow flags exist (the programmer manually re-materializes the outcome into a
`mutable bool` because the language threw it away). **Higher-order branching**
selects the next *decision space*: taking a branch restricts which branches are
legal afterward, so the outcome persists as a constraint on the future.

Existing Elisa machinery already does this in fragments: refinement facts
(`if x is Small:` makes later `x > 100` branches vacuous — a restricted decision
space at the value level), typestate (docs/96 — a value's state restricts which
method-branches exist), affine handles (the successor set after `consume` is
empty). `machine` makes it explicit at the control level: a state IS a bundle
of allowed decisions, and its transitions ARE the declared successor relation.

Block `if` is banned under strict flow (§6b) not for aesthetics but because it
is the one branching form with a trivial successor relation (every branch →
everything). Equivalently: **every branch must state what survives it** — a
value selection survives as the value, a guard survives as a refinement fact, a
match arm survives as bindings + facts, a transition survives as the state.
Nothing survives a block `if`.

### 1c. Branch totality (the semantic core)

In strict flow mode every branch arm is a **total function**

```
arm : (state payload, threaded resources) -> next State'(payload') | done VALUE
```

with no third option — specifically, no "fall through having mutated ambient
state." Anything that crosses a flow point crosses *explicitly*: in the
successor's payload, or as a linearly-threaded resource (`lmut` receiver,
`mutable T&`, affine handle). A branch produces a legal state — or one of its
declared *set* of legal states (§3 R5) — or it produces a value and leaves the
flow. Dataflow is thereby unambiguous.

This is SSA discipline surfaced as language semantics: machine payloads are phi
nodes, arms are basic blocks with arguments (the form MLIR/Cranelift use
internally because optimizers want it). Consequences:

- **Zero overhead in the strongest sense**: strict-flow source is isomorphic to
  the optimizer's IR; there is no lowering gap, and nothing for `-O2` to
  reconstruct out of mutable-flag soup (and fail at, under aliasing).
- **Fact-flow precision where it is currently weakest**: joins are where facts
  die today, because the checker must havoc over ambient mutable state it
  cannot track (the recurring dogfooding friction: "fact/range propagation
  through loops/joins"). Under branch totality, joins receive only declared
  payloads and threaded resources, so facts ride the edges — a payload
  `Num.Exponent(digits: usize)` carries its refinements into the arm.
- **Linear conservation across transitions**: a must-consume handle that enters
  a machine must leave every arm — via payload, threaded ref, or the `done`
  value. An arm that drops it is a compile error at the transition boundary
  (a far better place to catch a leak than function end). Same conservation
  aesthetic as effect rows (docs/124).
- Reference-typed payloads pass the same region-escape checks as match-arm
  bindings (existing storage-class-union / ReturnIsolation machinery, applied
  at transition edges).

Elisa precedents being closed over, not invented: loop accumulator pipes
(`|table, position: usize = 0| -> table`), `rebind value, lexer = ...`
threading, `lmut` receivers. Today these are opt-in idioms coexisting with
ambient mutation; strict flow makes them the only way state crosses an edge.

### 1d. The join rule (the one unratified article)

Branch totality (§1c) states the law for machines. Its generalization to ALL
control flow, in one sentence:

> **Any value whose meaning differs between incoming control-flow paths must
> cross the join explicitly.**

The legal crossings already exist — this names them as the closed set: the
*value* of an `if`/`match` expression, a `rebind`, a machine state payload, a
loop-header accumulator (`|sum = 0| -> sum`), or a declared threaded resource
(`lmut` receiver, `mutable T&`, a `changes`-listed set). What the rule forbids
is the one remaining implicit channel: **statement-join ambient mutation** — an
ordinary `match` whose arms mutate three outer variables differently and then
fall through the join, leaving the checker to reconstruct phi nodes from
ambient writes. Most articles of this constitution are ratified and enforced
(`rebind`; capture manifests; the docs/120 §10 `lmut` reassignment discipline —
a bare mutating call in return position is a hard error; frame conditions,
docs/87). Statement joins are the gap.

Enforcement is deliberately deferred behind measurement (the §7 pattern:
census → warn-tier detector calibrated to 0-FP → strict). The loop-header
mandate (`while cond |outer_a, outer_b|:` as a complete local frame condition)
is the natural strict-tier form for loops, and the highest-ergonomic-risk item
on the list — it graduates only if the census supports it.

> **CENSUS DONE, ENFORCEMENT DEFERRED (2026-07-11).** The census tool ships as
> `findJoinRuleSites` / `TestJoinRuleCensus` (analyzer_flow_join_rule.go): it
> counts branch statements whose fall-through arms phi-reconstruct ≥N outer
> variables, excluding synthesized `__`-names and counting a variable only when
> ≥2 sibling arms write it (a lone guarded write is not a value-picking join).
> Measured over the stage1 compiler + stdlib (145+18 files): **the anti-pattern is
> effectively absent.** Every ≥2-variable hit is a blessed form the syntactic pass
> can't yet tell apart — a post-desugar machine if-ladder (`{depth, lexer}`),
> self-referential accumulation (`n <- n + 1` across arms), same-value writes
> (`flag <- true` in two arms), or a captured threaded accumulator (`table`,
> already in a `|table, …|` manifest). The only hand-written residue is the
> two-variable default-then-overwrite extract (`x, y` pulled from a `match` with a
> `_` default), which §1d classes as **style, not divergence** — flagging it would
> train reflexive `ComplexFlow` grants, the failure §1d exists to prevent. So the
> warn-tier detector (step 11) and the loop-header mandate (step 12) do **not**
> graduate now: the corpus already crosses joins explicitly. Re-run the census as
> the codebase grows; wire the lint only if genuine multi-variable divergent-phi
> sites appear.

Two hard constraints carry over unchanged from the rest of the language:

- **Zero overhead.** Every blessed form must lower to exactly the code the
  unrestricted form would have hand-written. Restrictions are compile-time only.
- **Zero false positives.** Every refusal must fire on a *divergence between two
  things the programmer wrote* (an unreachable state, an arm that cannot run,
  or-alternatives that bind differently, a flag whose writes outrun its meaning)
  — never on style. A restriction that fires on correct code trains people to
  grant `ComplexFlow` reflexively, which destroys the signal.

## 2. Motivating corpus (stage1 compiler, measured)

Ranked by max indentation depth over `src/**/*.elisa`:

| Shape | Exemplar | Depth | Recurrence |
|---|---|---|---|
| Pattern-extraction ladder | `check_literal_assign_out_of_range.elisa:67` | 14 | nearly every `check_*.elisa` |
| elif decision table | `literal_fits_in_type` (same file, :21) | — | dozens (`type_name == "u8" or ...`) |
| Flag-driven scanner | `lexer_numbers.elisa:68` (`is_float`) | — | ~20 `mutable bool` flow flags in lexer/parser |
| Exhaustive recursive walk | `check_firm_arg_type_mismatch.elisa` | — | ~20 walkers |

The fourth shape is **deliberately out of scope**: the walkers are flat,
exhaustive, and the exhaustiveness is load-bearing (a new `Expr` variant forces
every walker to confront it). A derived-visitor construct would trade that
safety for brevity — wrong trade for a compiler. Long ≠ nested.

## 3. `machine from` — state is a place, not a variable

Generalizes docs/123: decouple `machine` from sequence consumption. States are
an enum (possibly anonymous); each arm ends in `next State` or `done VALUE`;
the whole form is an expression (docs/119).

```elisa
kind: TokenKind = machine from Num.Integer:
    Num.Integer:
        next Num.Fraction if lexer.should_read_float_fraction()
        next Num.Exponent if lexer.at_exponent_head()
        done TokenKind.IntLit
    Num.Fraction:
        lexer <- lexer.advance_char()
        lexer <- lexer.scan_decimal_digits()
        next Num.Exponent if lexer.at_exponent_head()
        done TokenKind.FloatLit
    Num.Exponent:
        lexer <- lexer.advance_exponent()
        done TokenKind.FloatLit
```

State payloads (`Num.Exponent(was_fraction)`) carry data legal *only* in that
state — the flattened replacement for nested ifs that exist purely to keep "we
already checked X" in scope. Refinement facts seed per-arm.

### Refusals

**R1 — no ambient state mutation.** There is no state variable; the only
transition is `next` at arm tail position.

```
error: machine state is not a value; transitions occur only via `next`
```

> **R1 arm law — LANDED (stage0, 2026-07-11).** The shared arm law now holds for
> BOTH machine forms: an arm body is straight-line computation only, then guarded
> `next`/`done` terminators. `machine from` arm bodies are validated by
> `validateMachineFromArmStmt` (parser/machine_from.go), the sibling of `machine
> over`'s `validateMachineArmStmt` — a nested `if`/`match`/`while`/`for`, or a
> control-flow escape (`return`/`break`/`continue`, since resolution is `next`/
> `done` not function-return) is now a parse error. Mutation stays legal in the
> body (the desugared loop's captures license it), so the two forms differ only in
> how they resolve. Regression tests: `TestMachineFromRefusalBranchInArm`,
> `TestMachineFromRefusalReturnInArm`, `TestMachineFromStraightLineArmAccepted`.
> Historical note: before this fix the `machine from` path performed zero
> arm-statement validation, so a nested `if a: if b: outer <- …` before the
> terminator passed semantic analysis silently — undermining `machine from` as the
> non-loop sibling of the strict machine.

**R2 — every arm must resolve.** Falling off an arm's end is neither "stay" nor
"exit"; it is a compile error. "I forgot to decide" is unrepresentable.

```
error: arm 'Num.Fraction' can complete without `next` or `done` —
every path must transition
```

**R3 — cycles demand a measure.** The transition graph is a compile-time
artifact. Acyclic ⇒ terminating for free. Any cycle requires a machine-level
`decreases`, discharged by the 4-increment prover / callee `ensure` summaries
(docs/118), or the escape hatch `can Unsafe.AssumeProgress`.

> **DISCHARGE WIRED (stage0, 2026-07-11).** The measure no longer stops at a
> presence check. The desugar prepends the machine-level `decreases M` as a
> leading loop-body clause on the lowered `while` (flagged
> `RuntimeProgressBackstop`), so the *existing* `checkLoopTermination`
> (analyzer_flow.go) owns it: `M` is type-checked as an integer (a non-integer
> measure is now a hard error — this catches real bugs), and a straight-line body
> is proved by strict-decrease + bounded-below. But a machine-from body is a
> `match` over the mode whose arms call into the driven resource, so it is never
> straight-line; the measure is therefore a runtime-backstopped **claim**, not a
> proof obligation (docs/125 §5) — an unprovable measure records `ProofRuntime`
> **silently** (no advisory lint, no `-strict` escalation) and relies on the
> runtime progress check, with `can Unsafe.AssumeProgress` the explicit opt-out.
> This is deliberate: unlike a hand-written `while decreases` (where the author
> expects a proof), R3 already *requires* the measure's presence, so the machine
> only needs the measure to be well-typed and the loop to be runtime-guarded.

```elisa
machine from Scan.Ws decreases lexer.remaining():
    Scan.Ws:
        next Scan.Comment if lexer.at('#')
        done lexer
    Scan.Comment:
        lexer <- lexer.skip_line()      # prover: skip_line shrinks remaining()
        next Scan.Ws
```

Without the measure: `error: transition cycle Ws → Comment → Ws with no
'decreases' measure`. The classic forgot-to-advance lexer hang becomes a
compile-time error.

**R4 — dead states are errors.** A state unreachable from the entry state means
the graph in the programmer's head and the graph in the code diverged.

**R5 — declared transition contracts (core, not bolt-on).** The transition
relation is the primary semantic artifact of `machine` (§1b: a branch decision
with declared restrictions on the branches it may execute afterwards). v1
*infers* the successor sets from `next` sites; v2 lets an arm *declare* its
out-edges (`Num.Integer -> {Fraction, Exponent}`), and the body may then only
take declared transitions — diffs to control flow become diffs to a declared
table. All graph checks (R2–R4, cycle/`decreases`) are queries against this
relation. Mirrors how regions went: inferred by default, annotated where the
contract should be visible. Natural hook for typestate/protocols (docs/96).

### Lowering

Acyclic machine ⇒ pure jump threading; no state variable materializes.
Cyclic machine ⇒ the same loop-over-branch a hand-written state variable
produces. Payloads live in registers/stack slots exactly as locals would.

### Status

✅ **MVP LANDED (stage0**, this increment): `machine from START [decreases M]:`
as a real `MachineFromExpr` node (states are the variants of an existing enum, so
`mode` reuses that enum — no synthesis). The analyzer owns the lowering: it runs
the graph refusals, determines the result type from the binding/return context
(the doc-canonical inferred form, `kind: T = machine from …`) or the join of the
`done` values, then builds the loop/mode/match desugar into `Lowered` and the
backend emits it — **zero new codegen** (the `LoweredCall` pattern). Refusals
enforced: R2 (every arm ends in `next`/`done`, last unguarded), R4 (dead states),
R3 (a `next`-graph cycle demands a `decreases`; the measure's presence is checked
here, discharge deferred to the docs/118 prover). Threaded mutation is licensed by
the loop's inferred captures. ✅ **stage1 port LANDED** (parser-only desugar to a
value `Expr.Block`: frontend, so no codegen and the graph refusals aren't
re-emitted — stage0 owns them).

✅ **STATE PAYLOADS LANDED both stages** (§8 resolved: named enum variant fields
now, anonymous inline enums later). A state may carry data legal only in that state:
`next Num.Exponent(was_fraction)` constructs the successor variant, the arm header
`Num.Exponent(was_fraction):` binds it — the flattened replacement for a nested `if`
kept in scope purely to remember "we already checked X". Reuses existing machinery
entirely: stage0 lowers `next State(args)` to an ordinary enum construction and binds
the arm via a variant-pattern destructure on `mode` (so refinement facts seed per-arm
and the graph checks, which key on the target name, are unchanged); stage1 dispatches
via `if mode is Enum.State(binds):` — the refinement `is`-pattern selects the arm and
binds the payload while keeping the exhaustiveness-free if-chain. The entry state may
carry a payload too (`machine from Once.Only(seed)`).

✅ **R5 declared out-edges LANDED** (`State -> {A, B}:`, stage0-enforced): an arm may
declare its successor set and then only `next` a listed target; every declared target
must be a real variant. The transition relation becomes a declared table, so a
control-flow change that adds an edge must change the declaration too — inferred by
default, annotated where the contract should be visible (mirrors regions). Compile-only,
zero runtime cost; R3/R4 still query the actual `next` graph. stage1 consumes the clause
structurally.

✅ **HEADER ACCUMULATOR PIPE LANDED (stage0)**: `machine from START |r: i32 = 0,
blocks: i32 = 0| decreases M:` declares machine-private mutables threaded across every
transition — the machine analogue of a loop-header accumulator pipe (docs/119 §3). They
are in scope for the `decreases` measure (which routinely references one, e.g.
`decreases 3 * (dh - r) + 2`) and every arm body; the pipe precedes `decreases` so the
measure can name an accumulator. This replaces the hand-declared outer mutables that a
cyclic machine previously needed (`r: mutable i32 = 0` before the `return machine`),
closing the last case where machine state leaked into an ambient outer variable. Reuses
the loop-header parser (a `-> yield` is rejected — a machine yields through `done`); the
lowering prepends the pipe decls to the machine's ExprBlock as loop-private locals, so
they are licensed by their own declaration rather than an outer capture. Zero new codegen.

✅ **LOCAL-STATE SUGAR `state`/`start` LANDED (stage0)**: a function may declare its states
inline instead of adding a top-level enum that exists only to name them:

```elisa
def emit_changed_blocks(dw: i32, dh: i32) -> i32:
    state Scan
    state Grow(first: i32, last: i32)
    state Emit(first: i32, last: i32)
    return start Scan |r: i32 = 0, blocks: i32 = 0| decreases 3 * (dh - r) + 2:
        Scan:
            next Grow(r - 1, r - 1) if row_changed
            next Scan
        Grow(first, last): …
        Emit(first, last): next Scan
```

`state Foo` / `state Bar(baz: T)` are function-body statements that accumulate on the parser;
`start Foo …:` <arms> synthesizes an enum from them (hoisted to file scope with a program-unique
name, like `machine over`'s mode enum), then delegates to the identical `machine from` tail parser
with the synthesized enum as the state type. So it inherits EVERYTHING from `machine from` —
payload states (`state Bar(baz: usize)` → the arm binds `baz`), the header accumulator pipe,
`decreases`, and every R2/R3/R4/R5 refusal — with zero new analysis or codegen. `state`/`start`
stay contextual keywords (recognized only when directly followed by an identifier, so variables
named `state`/`start` are unaffected); states are function-scoped (cleared when a `start` consumes
them and reset per function). This is the anonymous-inline-state need met WITHOUT the earlier
objection: payload field types are written at the `state` line, so nothing is reinvented — the
`state` block IS the declaration site. Diagnostics suggest the `start` spelling, never the
synthesized enum name. Golden TestMachineFromStateStartSugarRuntime.

Anonymous inline state enums (a fully nameless `machine from` with no `state`/enum at all):
declined (§8 — payloads settled the trade in favor of named states); `state`/`start` gives the
locality benefit while keeping every state a written, named declaration. The construct is complete.

## 4. `when` — order-independence as a declaration

A decision-table construct. Choosing `when` over `match` *declares* the arms
order-independent, and the checker enforces that declaration. Wildcard `_` is
the total-default row (there is **no `else` keyword** in this construct).

```elisa
def literal_fits_in_type(value: i64, negated: bool, type_name: sview) -> bool:
    when type_name, negated:
        "u8",  false -> value <= 255
        "u16", false -> value <= 65535
        "u32", false -> value <= 4294967295
        "u8" | "u16" | "u32", true -> false
        "i8",  false -> value <= 127
        "i8",  true  -> value <= 128
        "i16", false -> value <= 32767
        "i16", true  -> value <= 32768
        "i32", false -> value <= 2147483647
        "i32", true  -> value <= 2147483648
        _ -> true                      # unknown type: never flag (stated policy)
```

Shuffling rows is provably behavior-preserving; the table reads as a spec
because it is checked as one. The silent fall-off-the-bottom `return true` of
the current code becomes a named policy line with a blame-able location.

### Refusals

**R1 — overlap is an error, not a style issue.** In ordered `match`, a
shadowed arm is legal (first wins — that is the contract; docs/122 guards are
deliberately non-covering). In `when` it means the table you wrote is not the
table that runs — always a bug, always rejected.

```
error: arm ("u8", _) overlaps arm ("u8", false) at line 3;
`when` arms must be disjoint
```

The `_` row is exempt from overlap *against* it (it is defined as "everything
not covered above"), but a `_` row that is itself unreachable — the non-`_`
arms are already total — is an error (vacuous default).

**R2 — incompleteness is an error.** No `_` row ⇒ the checker demands totality
over the scrutinee tuple's type, using the existing range/refinement prover and
(under `-strict`/`-smt`) the SMT tier for numeric scrutinees.

```
error: `when` does not cover ("i64", false); add an arm or a `_` row
```

**R3 — arm forms are restricted so disjointness stays decidable.** Grammar of a
`when` arm: literals, ranges, enum tags, `_` per-column, or-groups (`|`),
tuples thereof. No computed guards, no bindings-with-guards. Need a guard?
That is `match`. Everything `when` accepts, it fully verifies — the restriction
is the point.

```
error: `when` arms are literal/range/tag patterns; computed guards need `match`
```

### Lowering

Identical to the equivalent `match`/if-chain; disjointness additionally licenses
jump-table / bit-test lowering without first-match ordering constraints.

## 5. Deep patterns in arm position — the test IS the binding

Extends docs/122: full patterns (not just binders) in enum-payload positions of
a `match` arm, plus or-alternative distinguishing bindings via `with`.

The depth-14 offender collapses to one arm whose pattern states every
precondition atomically:

```elisa
Stmt.Assign(Expr.Ident(target_name, _), TokenKind.LArrow,
            Expr.IntLit(magnitude) with negated = false
          | Expr.Unary(TokenKind.Minus, Expr.IntLit(magnitude), _) with negated = true,
            line):
    target_type: sview = find_local_type(target_name, local_names, local_types)
    if not literal_fits_in_type(magnitude, negated, target_type):
        table.diagnostics <- table.diagnostics.push(Diagnostic{...})
```

Depth 14 → 4, and the duplicated positive/negative ladders unify — the
duplication is exactly where a future edit fixes one copy and not the other.

### Refusals

**R1 — or-alternatives must bind identical names at identical types.**

```
error: or-pattern alternatives bind different names: {magnitude} vs {op, operand}
```

`with name = LITERAL` supplies the distinguishing constant per alternative so
the arm body is written once.

**R2 — no shape re-testing of a bound value** (enforced by the tier in §6):
binding a payload and then probing it with chained `is` tests is the shape that
produced depth 14; once deep arm patterns exist, that shape is flagged.

### Lowering

Same tag-test chain the nested `is` ladder compiles to today. Zero overhead.

### Status

Deep nested patterns, payload-literal patterns, and or-patterns binding a shared
name were already delivered by docs/122 — the only new syntax §5 needs is the
`with` discriminator. ✅ **`with NAME = LITERAL` LANDED (stage0**, commit
24d14243): a parser-level desugar — the top-level `|` already fans an or-arm
into one `MatchArm` per alternative sharing the body, so a `with` clause
prepends its `NAME = LITERAL` bindings (immutable, type-inferred locals) to that
alternative's body copy. No AST/semantic/backend change; `with` is a
reserved-but-unused keyword. Works in statement and expression match.

Scope landed: `with` binds at the arm-ALTERNATIVE (top) level. The §5 depth-14
example nests the `with` inside a payload arg's or-group; that nested form is
deferred (it needs the backend or-pattern matcher to bind the constant deep in
the pattern), and the same table is expressible by lifting the or to the arm
level. ✅ **stage1 port LANDED** (commit 50fed61, `parser_stmt_with.elisa`): a
lookahead gate (`arm_header_has_with`) takes a fan-out path ONLY when the arm
header carries a top-level `with`, so every `with`-less arm stays byte-for-byte on
the existing `parse_pattern`/`Pattern.Or` path. When present, the arm fans out into
one `MatchArm` per `|` alternative, each with its constants prepended to a fresh
body copy. Two stage1-specific wrinkles the port handles: the constant value is
parsed at the postfix level (not `expression()`) so a following `|` isn't swallowed
as bit-or, and the continuation-line `|` separator is found across the un-suppressed
layout newline (`next_nonnewline_is_pipe`). **Dogfood outcome — principled
decline:** a sweep of the self-hosted parser found exactly one candidate
(`parser_expr.elisa` `True | False` → `BoolLit(kind == TokenKind.True)`), and
converting it is a wash — the re-test is already the clearer one-line form, and
every other or-arm in the tree deliberately erases the alternative distinction via
uniform dispatch/derivation. `with` is a user-code feature (large shared bodies that
genuinely re-branch), not a compiler-source one. The one *untested shape* the sweep
surfaced — bare-member (payload-free) or-arm `with` in value position — was verified
end-to-end and locked in as additive stage0 runtime coverage (commit e12138f9).
✅ **R1 LANDED both stages** (stage0 5010ed92 `checkWithBindingParity`; stage1
f66c3bd `check_with_binding_parity`): every alternative of a `with`-arm must bind
the IDENTICAL set of constant names — a name only some alternatives supply resolves
for those siblings and fails late as a confusing `undefined identifier` on the
others, so the mismatch is reported once, clearly, at the arm (naming the
missing/extra constants in stage0). Zero-false-positive by construction: the check
is *inert unless some alternative carries a `with`*, so plain or-arms
(`A(x) | B(_):`, no `with` anywhere) are never touched — the feared collision with
existing pattern-capture arms cannot occur, because those have no `with` clause.
This is the key insight that made the "zero-FP corpus sweep" unnecessary: R1 keys on
the new `with` construct, not on pattern bindings. stage1 wires a new
`SyntaxMismatchedWithBindings` DiagnosticKind through the full 6-point path. Still
deferred: R2 (the shape-retest lint) belongs with the §6 detectors.

## 6. `can ComplexFlow` — unrestricted forms become the visible exception

Same social technology as `trusted`, `can Scalar`, and the raw-concurrency
removal: the free forms still exist, but using them where a checked form fits
requires a grant, so reaching for freedom is a decision in the diff.

Blessed subset (no grant needed): straight-line + postfix guards, `match`,
`when`, single loop per function level, `machine`, the existing comprehensions.

Flagged under `-Wflow` (graduated like `-Wperf`; error under `-Wflow=error`):

- **Flow flags**: a `mutable bool` written in ≥2 branches and read after the
  branching join — "this is a state machine; use `machine from`."
- **Shape re-tests**: an `is`-ladder ≥ N deep re-probing one bound value —
  "this is one deep pattern; use a `match` arm."
- **Shadow-prone elif tables**: an elif ladder whose conditions are all
  equality/range tests over the same scrutinee expression — "this is a decision
  table; use `when`."

Escape hatch:

```elisa
def resolve_overload(...) -> Symbol:
    can ComplexFlow:        # stated: hand-woven flow; reviewers look closely
        ...
```

Greppable, auditable, and its *absence* is a machine-checked guarantee. Each
detector must meet the zero-FP bar on the stage1 corpus before it ships
(precedent: every `check_*` diagnostic's corpus sweep).

### 6b. Strict flow mode (`-Wflow=strict`)

The full discipline, as a graduated project-level mode (same ladder as
`-Wperf`; `can ComplexFlow` remains the per-function escape). Under strict
flow, every decision must be **a value, an exit, an arm, or a transition**:

| Form | The branch IS | Strict flow |
|---|---|---|
| `x = A if cond else B` (expression-if) | value selection | allowed |
| `return false if not valid` (postfix guard) | early exit | allowed |
| `STMT if COND` (generalized postfix guard) | do-or-skip effect | allowed |
| `if EXPR is NAME:` (refinement binding, docs/80) | checked destructure | allowed |
| `match` / `when` | shape/table decision | allowed |
| `machine` | state transition | allowed |
| `for` / `while` | iteration | allowed |
| block `if cond:` / `elif` / `else` statements | anything | **banned** |

Notes:

- **Prerequisite: postfix `if` generalizes** from `break`/`continue`/`return`
  to all simple statements (assignment, expression-statement, `rebind`). No
  postfix `else` — a postfix guard is do-or-skip, never two-way (two-way is a
  value selection or a match). Without this, conditional mutation has no home
  and people write `x <- a if c else x` — a fake ternary with a no-op arm,
  worse than the `if` it replaces.
- **`if EXPR is NAME:` is exempt** because it is not a decision but a checked
  destructure — a guard with a binding — and it is the canonical refinement
  spelling (docs/80).
- The ban is **syntactic**, so the detector is zero-FP by definition.
- The ban creates the demand that `machine from` and `when` supply: a
  5-statement conditional block can no longer hide — it must become a guard
  ladder, an extracted function, a match/when arm, or a machine state. The
  restriction and the ergonomic constructs must therefore land together.
- Branch totality (§1c) is enforced in strict flow at machine/loop edges: no
  ambient mutable local may cross a transition; whatever crosses is payload or
  linearly threaded. The FlowFlagStateMachine lint (shipped, code 137) is the
  first, weakest enforcement of this rule.

## 7. Increment plan

1. **Probe (no new syntax).** ✅ DONE (first detector): FlowFlagStateMachine
   (code 137) shipped as a stage1 diagnostic; 131-file sweep found 2 hits, both
   true positives, zero FP; both offenders flattened to value-form state
   (lexer parity bit-identical). ✅ REMAINING DETECTORS LANDED (stage0
   1ccee352, under `-Wflow`): **shape-retest** (≥3 nested `if … is PATTERN:`
   probes each re-probing a value the previous pattern bound → one docs/122
   deep pattern; pure-is conditions + direct nesting only) and
   **shadow-prone elif tables** (≥3-arm chain, every condition an equality
   test of one scrutinee against int/char/string literals → `when`; member
   references excluded in v1 — syntactically ambiguous with variable fields,
   where the advice would be wrong). Both keyed on the parser-set
   `IfStmt.FromSource` mark → zero-FP by construction. Calibration: 132-file
   sweep, 3 hits, 3 TP, 0 FP; all three fixed (Elisa-compiler b1a5c64 — the
   Call→Field→Ident ladder became one deep pattern; the two
   `diagnostic.expected` message tables became `when` expressions).
2. **Postfix-guard generalization** (stage0, small — extends a landed feature)
   + the syntactic block-`if` detector + a census sweep of stage1 pricing the
   strict-flow migration: how many block-ifs are guards-in-disguise or value
   selections (fixable today) vs genuine state machines (need `machine from`).
   ✅ PARTIAL: generalized postfix guards landed both stages (stage0 0a349414,
   stage1 9ef477a); census done (1687 block-ifs: 419 multi-stmt, 332 effect-
   guards, 320 exit-guards, 284 is-bindings, 194 value-selects, 138 elif
   ladders). The load-bearing **ternary-requires-else invariant** is now
   enforced on BOTH stages (stage0 always had it; stage1 gained
   `IfValueMissingElse`, error code 138) — an else-less `if` at a statement
   site is therefore unambiguously a postfix guard on both compilers.
   ✅ COMPLETE: the syntactic block-`if` detector LANDED (stage0 1ccee352,
   fires ONLY under `-Wflow-strict`): a written block `if` errors unless its
   condition contains an `is`-test (checked destructure, exempt per §6b) or a
   `can ComplexFlow:` covers it. Zero-FP by construction via the
   `IfStmt.FromSource` parser mark — postfix-guard desugars, loop-header
   wrappers, and machine lowerings never carry it. Fresh strict census:
   **1295 block-if sites in stage1 src/** (excl. stdlib) — the priced
   increment-6 migration debt.
3. **`when`** — parser + disjointness/totality checker; reuses the range
   prover. Migrate `literal_fits_in_type`-shaped tables.
   ✅ STAGE0 LANDED: contextual keyword (machine precedent — parser-only,
   desugars to `match`, zero AST/semantic/backend footprint). `|` binds within
   a column, `,` separates columns; the `_` default row is emitted last so a
   mid-table default cannot swallow arms. R1 = syntactic pairwise disjointness
   over literals/ranges/tags (opaque atoms never claim overlap — zero-FP); R2
   inherited from match exhaustiveness (range-union totality proving and the
   vacuous-`_` check remain future work); R3 rejects bindings, destructuring,
   guards, and pinned values at parse time. Fixed en route: string patterns in
   tuple-match columns emitted an invalid aggregate icmp (now lower through the
   runtime string-equality helper).
   ✅ STAGE1 LANDED: same contextual-keyword + parser-only-desugar-to-Match
   shape; per the machine-port philosophy the R1/R2/R3 refusals are NOT
   re-emitted (stage0 owns them) — the stage1 parse is purely structural.
   Gates green (self-hostable 0/130, breadth 127/0, 31 parity smokes +
   when_smoke).
   ✅ DOGFOOD BATCH 1: 3 stage1 elif ladders migrated — `precedence`
   (9-way operator table, `in {…}` groups → `|` or-columns incl. a `.Pipe`
   tag beside the `|` separator), `literal_outside_range` (7-way type→bounds
   table), `family_of_kind` (6-way TypeKind→string table). The last exposed a
   pre-existing match/when-expression gap: an ALL-string-literal table joins to
   `static u8&` (no arm supplies a view) and couldn't be returned as `sview`;
   fixed by a contextual match-expr path that adopts the expected string type
   (stage0 ddce717d).
   ✅ DOGFOOD BATCH 2 (stage1 6e3d8ad): 9 more value-selection ladders migrated —
   `token_text`, `literal_never_fits`, `firm_never_fits` (resolve_types),
   the three `machine_*_name` int-slot tables (parser_stmt_machine),
   `int_fits_storage` (const-enum), the inline result-RHS operator table
   (check_constant_comparison), and `literal_fits_in_type` (signed arms folded to
   `A if is_negative else B` ternaries). FINDING: not every elif ladder wants
   `when` — two all-`-> true` predicates (`is_mutation_operator`,
   `is_primitive_type`) are set membership, so they became `x in {…}` instead
   (confirmed string-set membership compiles+runs). Census showed the true pure-
   table pool is ~a dozen, not 135: most remaining elif chains are token-scanner
   state machines (effects/multi-statement) — `machine from` territory, not `when`.
   Remaining table sites are now few; the bulk of §7.6 strict-flow debt is scanner
   ladders awaiting `machine from` state-payload support.
4. **Deep arm patterns + `with`** — extends docs/122 machinery; migrate the
   `check_*` extraction ladders.
   ✅ PARTIAL: deep nested / payload-literal / shared-name or-patterns were
   already delivered by docs/122; `with NAME = LITERAL` arm-alternative
   discriminators LANDED stage0 (24d14243). Remaining: stage1 `with` port,
   nested-`with`, the R1 early diagnostic, and the check_* ladder migrations.
5. **`machine from`** — generalizes docs/123 (states from transitions instead
   of sequence elements) + transition-graph checks (R2–R5) + cycle/`decreases`
   integration with docs/118 + branch-totality enforcement at edges (§1c).
   Migrate the lexer/parser flag scanners.
   ✅ MVP LANDED stage0: `MachineFromExpr` node, analyzer-owned lowering to a
   loop/mode/match desugar (inferred result type, zero new codegen), refusals
   R2/R3/R4. ✅ stage1 port LANDED (c339d93). ✅ STATE PAYLOADS LANDED both stages
   (stage0 d4e78c74; stage1 eb65c39): `next State(args)` constructs, arm header
   `State(binds):` binds, entry payload supported. ✅ R5 declared out-edges LANDED
   (stage0 22c8eb32, `State -> {A, B}:`). Anonymous inline state enums DECLINED
   (§8); flag-scanner migrations N/A (already flat value-form). Increment complete.
6. **`-Wflow=strict` graduation** — off → warn → stage1 becomes the first
   strict-flow project; the syntactic detectors keep it clean.

## 8. Open questions

- ~~Anonymous state enums for `machine from`~~ — **RESOLVED: declined.** The
  trade was "inline is lighter; named gives payload types a place to live", and
  landing state payloads settled it: an anonymous state with a payload must
  write the field type *somewhere* — either inferred at first `next` (use-site
  inference, worse diagnostics, order sensitivity) or via an inline field
  syntax that reinvents the enum declaration with fewer letters. The named form
  costs ~4 lines and buys a declaration site for payload types (and future
  per-state invariants — the docs/96 typestate hook wants named states), reuse
  across machines, and diagnostics that name something the programmer wrote
  rather than a synthesized `__MachineState_3`. Dogfood evidence agrees: the
  scanner audit found zero internal `machine from` targets, so a second
  parallel state-declaration path would be speculative surface with no
  demonstrated demand. Purely additive — revisit only if a real corpus site
  shows the named enum being genuine friction.

- **Active patterns (F#-style) — considered, mechanism declined, need
  acknowledged.** The need (abstracting over match *shapes* so patterns
  compose) is real, but arbitrary user functions executing in pattern position
  collide with both prime directives. (1) Zero-FP refusals: `when` R1/R2,
  match exhaustiveness, and guarded-arms-never-cover all depend on the
  compiler *knowing* disjointness and totality; two active patterns are opaque
  predicates it cannot relate — F# concedes exactly this by giving up
  exhaustiveness for partial active patterns, a concession that would poison
  the analyses everything else here stands on. (2) Zero overhead: hidden user
  code running during matching breaks "the blessed form lowers to what you'd
  hand-write" — the match stops being a discrimination tree and becomes an
  invisible call sequence. The sound core already exists in Elisa today: a
  *total* active pattern is a classifier fn returning an enum (`match
  classify(x):` — exhaustiveness comes back for free, from the enum); a
  *partial* one is an option-returning fn (`if parse_int(s) is v:` — the
  refinement-`is` binding). If pattern abstraction ever earns dedicated
  syntax, the Elisa-shaped candidate is `match x via classify:` — sugar that
  REQUIRES the classifier to be a total, enum-returning (spec-tier pure)
  function, so totality/disjointness come from the enum, not from trusting a
  predicate. Wait for a dogfood case where the `match classify(x)` spelling is
  demonstrably clunky before adding even that. → **PROGRESSED (2026-07-11): the
  dogfood case arrived (the number lexer's strict-flow migration) and the probes
  showed even `via` is unnecessary — `machine over classify(c)` with enum-tag
  arms parses, checks, and runs on the current compiler. The classifier hatch
  graduates to a full design increment: §9.**
- `when` over non-tuple scrutinees with refinement types: does totality consult
  the refined range (e.g. scrutinee `Small` means arms need only cover 0..9)?
  (Yes in spirit — reuses the vacuity machinery.)
- Interaction of `machine` arms with region inference when payloads carry
  region-backed containers (should be identical to `match` arm bindings).
- Whether `_` per-column (`"u8", _`) participates in overlap checking as
  "everything" (proposed: yes, it is just a total range for that column).

## 9. Classified dispatch — total classifiers as scrutinees

Added 2026-07-11, graduated from the §8 active-patterns entry. The dogfood case was
the number lexer's strict-flow migration: numeric literals are a genuine state
machine (`Int → Frac → Exp → Suffix`), but its transitions fire on **character
classes** (`is_digit`, `is_hex_digit`) — and no dispatch construct could take a
class as scrutinee, so the machine collapsed into `while is_digit()` loops and the
modelling evaporated. The gap is not syntax; it is that dispatch could not see
through a *derived classification* of the input.

### 9.1 The discovery: this already runs

Probed on the shipping compiler (2026-07-11): `machine over classify(c)` where
`classify: char -> CharClass` returns a plain enum, with enum-tag arms
(`Scanning, CharClass.Digit:`), **parses, type-checks, and executes correctly
today** — end-to-end native test green. The feature is therefore NOT new grammar.
What is missing is exactly three guarantees:

1. **Tag-coverage totality.** The machine totality checker demands a final
   unguarded `State, _:` wildcard even when the arms cover the closed enum
   exactly. For enum scrutinees this is backwards: `_` **erases the
   add-a-variant safety net** (extend `CharClass` and every machine silently
   routes the new class to the wildcard instead of failing to compile). Rule:
   when the input type is a closed enum, per-state coverage = `match`'s tag
   totality; the wildcard becomes optional and *discouraged*.
2. **Classifier qualification — three existing facts, no annotation.** A
   scrutinee former qualifies as a classifier when (a) its effect row is empty
   (pure), (b) it has a termination summary (docs/118 / the 4-increment
   prover), (c) its return type is a closed enum. All three are facts the
   compiler already tracks; the conjunction needs no keyword, no `@classifier`,
   no new checker. **The carrot rule:** qualification is what *upgrades*
   coverage checking from wildcard-mode to tag-mode. An impure or unproven
   former keeps today's wildcard rules — zero breakage; the stricter checking
   is what classifier discipline earns.
3. **Table folding — the performance proof.** A pure total `char -> Class`
   function constant-folds into a 256-byte class table — exactly what
   production lexers hand-write (`switch(char_class[c])`). The declarative
   spec should lower to something *faster* than the hand-rolled branch chain,
   not merely equal. `-Wperf` flags a classifier too complex to fold.

### 9.2 Evaluation rule

The scrutinee expression (`classify(cur())`) is evaluated **exactly once per
step** — the machine's existing `over` contract. Purity makes the classification
coherent within the step; guards run after classification and may *read* driven
resources (lookahead) but never mutate. Division of labor, stated once:

> **`when` classifies values into classes; `machine` sequences classes into
> states; guards consult context.** Patterns never hide user code (the §8
> active-patterns refusal stands); classifiers never hide partiality (totality
> is machine-checked, not promised).

This is the one sound corner of the views / active-patterns design space:
Wadler-style views broke equational reasoning, Haskell pattern-synonym
`COMPLETE` pragmas are unchecked promises, Scala `unapply` is opaque, F#
partial active patterns concede exhaustiveness. Elisa's version is checkable
because the provers it leans on (purity via effect rows, termination via
docs/118, totality via closed enums) already shipped.

### 9.3 Payload-carrying classes

`Digit(d: u8)` — the classifier *parses as it partitions*. Arms receive the
decoded payload with its refinement facts (`d <= 9`) feeding the arithmetic
prover. Payloads subsume the remaining wants:

- **Value capture** without re-deriving from the raw input in the arm.
- **The radix fork enters the machine.** The hex/decimal lead that otherwise
  needs a pre-machine block-`if` (today: `can ComplexFlow:` in `read_number`)
  becomes an ordinary `Lead` state: `Lead, Digit if lexer.current_char() == '0'
  and lexer.peek(1) in {'x','X'}: … -> Hex` — guards already work in machine
  arms. No `start from` construct needed.
- **Refinement flow**: `parse_int_literal_value`'s digit-decode ternary chain
  is the second client.

Ergonomic follow-on: leading-dot tag shorthand in arm position
(`Lead, .Digit:`), consistent with the existing shorthand-member forms.

### 9.4 Verified pattern-support matrix (machine arms, 2026-07-11)

Probed individually — earlier cascade errors had blurred this:

| In arm input position     | Status |
|----------------------------|--------|
| literals (`'"'`, `'m'`)    | ✓ |
| alternation (`' ' \| '\t'`)| ✓ |
| bind + guard (`c if c.is_digit()`) | ✓ |
| enum tags (`CharClass.Digit`) | ✓ (runs today) |
| payload literal/refinement patterns | ✓ |
| `_` wildcard               | ✓ (currently *mandatory* per state) |
| **range (`'0'..='9'`)**    | **✗ hard parse error** |
| duplicate/shadowed arms rejected | **✗ two identical `'m'` arms pass** |

The range gap is why classifiers matter more than pattern-grammar expansion:
ranges belong in the classifier's `when` (which has them), not in the machine's
arms. Unifying the grammar (roadmap Phase 1) is still right for delimiter
machines, but the classifier makes it non-blocking. The duplicate-arm gap is a
§5.5 (docs/123) enforcement hole to close alongside tag-coverage.

### 9.5 Increments

- **C0 — pilot (no compiler change).** ✅ DONE (Elisa-compiler d76556e).
  `read_number` is a 6-state classified machine (Lead/Hex/Whole/Fraction/
  ExpLead/ExpDigits over a total `NumberClass` classifier); the radix fork is
  a guarded Lead arm and the `can ComplexFlow:` grant is gone; the kind
  payload rides the states and survives EOF fall-out. All acceptance criteria
  green: token parity byte-identical (13 fixtures), strict census 0,
  lexer_bench at-or-above baseline (289.8 vs 284.6 MB/s best-of-3).
  **Perf finding: state-DECLARATION order shapes the lowered dispatch ladder**
  — hot digit-run states declared first turned a 4% regression into a small
  win. C3's table folding should also consider mode-dispatch ordering by
  execution frequency, or profile-free heuristics (self-transitioning states
  first).
- **C1 — tag-coverage** for closed-enum scrutinees ✅ DONE. Two-tier totality,
  strictly *stronger* than before (not a relaxation):
  - **Tier 1 (open input — char/int/range):** the final unguarded `_` stays
    mandatory. The parser proves this without types and errors early.
  - **Tier 2 (closed const-enum input — classified dispatch):** every state must
    spell **all** the enum's variants as explicit tags. A missing variant is a
    hard error naming it (the add-a-variant safety net); an unguarded `_` is
    **rejected** ("spell the tags" — a wildcard is exactly what erases the net).
    The parser can't see types, so the desugar emits a `MachineCoverageStmt`
    (compile-time-only, no codegen) carrying per-state {tags, hasWildcard} and the
    input expr; the analyzer resolves the input type and enforces the tier. A
    wildcard-less state lowers with a defensive `else: break` — dead code under a
    proven-total dispatch, but the machine can never spin even if a hole slipped
    past. Also: duplicate same-literal/tag arm rejection (`063f385e`) and range
    patterns in machine arms (`3eb5d3c5`). The pilot's `read_number` was flipped
    from `_` arms to explicit `NumberClass` tags (Elisa-compiler); parity
    byte-identical. Tests: `TestMachineCoverageClosedEnum{Complete,MissingTag,
    WildcardRejected}`.
- **C2 — qualification gating** (the carrot rule); leading-dot arm shorthand
  ✅ ALREADY WORKS (`Go, .Digit:` resolves via the scrutinee enum type; a bogus
  `.Member` errors). Qualification gating (empty-effect + termination +
  closed-enum → tag-mode) remains.
- **C3 — table folding** ✅ DONE (stage0 `analyzer_classifier_fold.go`). A pure
  total `char -> const enum` classifier whose body is a single `when`/`match`
  (literal / range / alternation arms + wildcard) is recognized before analysis,
  evaluated at compile time for all 256 bytes (char is byte-sized), and rewritten
  to a static-table lookup: a file-scope `const __classtable_<fn>: Enum[256]`
  (lowers to `internal constant` rodata) plus `return __classtable_<fn>[c]`. No
  backend change — reuses const-array codegen; behavior-preserving by
  construction (the table is the classifier's own truth table). Works for
  top-level and `extend`/`module`-nested classifiers (the pilot's real shape).
  **Benchmark (500M classify calls, best-of-3): folded table 4011 Mchar/s vs the
  same classifier's branch chain 1402 Mchar/s — 2.86× faster**, identical
  checksum. The "declarative *and* faster" claim is demonstrated, not asserted.
  (Note: `when c:` lowers to a 13-`icmp` comparison ladder, not an LLVM
  `switch` — LLVM does not auto-table it, so the fold is a real win.)
  The `-Wperf` foldability lint (`checkUnfoldableClassifier`) is also LANDED: a
  classifier-shaped function (char param, const-enum return) that dispatches by a
  branch chain instead of folding draws a warning (hard error under `-Wperf`),
  naming the blocker (a guarded arm, etc.); a folded classifier draws nothing.
