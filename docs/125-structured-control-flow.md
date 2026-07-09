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

**R5 (later increment) — declared transition contracts.** An arm may declare
its out-edges (`Num.Integer -> {Fraction, Exponent}` in a header block); the
body may then only take declared transitions. Diffs to control flow become
diffs to a declared table. Natural hook for typestate/protocols (docs/96).

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

## 7. Increment plan

1. **Probe (no new syntax).** Prototype the three `-Wflow` detectors as stage1
   diagnostics on existing syntax; sweep the 214-file corpus; hand-rewrite the
   three exemplar offenders to measure the ergonomic delta. Kill criteria: a
   detector that cannot reach zero FP, or rewrites that read worse.
2. **`when`** — smallest new surface; parser + disjointness/totality checker;
   reuses the range prover. Migrate `literal_fits_in_type`-shaped tables.
3. **Deep arm patterns + `with`** — extends docs/122 machinery; migrate the
   `check_*` extraction ladders.
4. **`machine from`** — generalizes docs/123 (states from transitions instead
   of sequence elements) + transition-graph checks (R2–R4) + cycle/`decreases`
   integration with docs/118. Migrate the lexer/parser flag scanners.
5. **`-Wflow` graduation** — off → warn → project default, with `can
   ComplexFlow` from day one. R5 (declared transitions) after real usage.

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
