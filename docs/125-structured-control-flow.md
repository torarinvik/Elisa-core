# docs/125 — Structured Control Flow: Refusals as the Product

Status: DESIGN (nothing implemented)
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
level. Deferred: stage1 port (stage1 keeps `|` as a single `Pattern.Or` node, so
the port needs a decl accumulator threaded through the pattern parser); the R1
diagnostic (or-alternatives that bind different names currently fail late as
`undefined identifier` only when the body uses the missing binding — an early,
clear error needs a zero-FP corpus sweep since strict identical-binding would
also flag existing `A(x) | B(_)` arms whose body uses neither); R2 (the
shape-retest lint) belongs with the §6 detectors.

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
   (lexer parity bit-identical). Remaining probe detectors: shape-retest
   ladders, shadow-prone elif tables.
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
   Remaining: the syntactic block-`if` detector itself (fold into 6).
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
   (stage0 ddce717d). Remaining: the other ~135 census sites.
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
6. **`-Wflow=strict` graduation** — off → warn → stage1 becomes the first
   strict-flow project; the syntactic detectors keep it clean.

## 8. Open questions

- Anonymous state enums for `machine from` (declare states inline at first
  `next`?) vs requiring a named `const enum` — inline is lighter; named gives
  payload types a place to live.
- `when` over non-tuple scrutinees with refinement types: does totality consult
  the refined range (e.g. scrutinee `Small` means arms need only cover 0..9)?
  (Yes in spirit — reuses the vacuity machinery.)
- Interaction of `machine` arms with region inference when payloads carry
  region-backed containers (should be identical to `match` arm bindings).
- Whether `_` per-column (`"u8", _`) participates in overlap checking as
  "everything" (proposed: yes, it is just a total range for that column).
