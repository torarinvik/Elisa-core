# 114 — Named contracts: a user-facing guide

Cross-references: docs/97 (original design), docs/111 (unified staging plan),
docs/109 (unified refinement pipeline), docs/95 (laws and refinement cheat-sheet).

---

## Implementation status (as of 2026-06)

Named contracts are **fully implemented and working** end-to-end.  This covers:

- `contract Name(params):` top-level declarations with `requires`, `ensure`, `changes`,
  `preserves`, and `includes` clauses (docs/97).
- `uses Name(args)` application inside `fn`/`def` and `extern` declarations.
- Formal substitution: contract formals are substituted by the application arguments before clauses
  are folded into the applying function's own `Requires`/`EnsureValues`/`Changes`/`Preserves`.
- Argument type-checking: each `uses` argument is type-checked against the contract formal before
  substitution (type mismatch is a hard error naming the formal and the expected type).
- Arity checking: wrong number of `uses` arguments is a hard error.
- Unknown-contract detection: `uses UnknownContract(...)` is a hard error.
- Empty-contract detection: a contract with no clauses is a hard error.
- No-parameter contracts: a contract with zero parameters is a hard error (the parameter is the
  subject every clause is written against).
- Generic contracts: `contract Name[T](...)` specialized at `uses Name[i64](...)`.
- Included value laws (`includes NonNeg(x)`): the law predicate is substituted and folded into
  `Requires`, so it is checked at every call site of the applying function.
- Included effect laws (`includes NoAlloc()`): folded as an effect obligation (the function must
  not exhibit the forbidden effect).
- Frame conditions (`changes`, `preserves`): frame-path roots are rebound from contract formals
  to application arguments; the result is union-merged into the applying function's frame set.
- Proof composition: `by`-block proofs on contract clauses are substituted together with the
  clause and carried into the applying function.
- Extern boundary: `extern` declarations may also carry `uses` clauses.
- Discharge: inherited requires/ensure are discharged through the standard three-tier ladder
  (fact-lattice, linear prover, SMT under `-strict`), exactly like hand-written clauses.
- Call-site checking: inherited `requires` obligations are enforced at every call site of the
  applying function, just like explicit `requires` on that function.

What is **not yet implemented** (still design/planned):

- `@satisfies(ContractName)` conformance annotation on a struct or protocol for S1.
- Quantified predicates over collections inside contracts (S2).

---

## Overview

**Named contracts** are reusable bundles of preconditions and postconditions.  They let you
write a set of obligations once and apply them to multiple functions by name, avoiding
repetition and improving readability.

### The problem they solve

Without named contracts, if you have two functions that must satisfy the same set of
obligations, you must repeat them:

```elisa
def copy_floor(s: i64) -> i64:
    requires s >= 0
    ensure result >= s
    return s

def echo_floor(s: i64) -> i64:
    requires s >= 0
    ensure result >= s
    return s
```

Named contracts eliminate this duplication.

---

## Syntax and declaration

### Basic contract

A **contract** declaration groups related `requires` and `ensure` clauses:

```elisa
contract NonNegOut(out: i64, src: i64):
    requires src >= 0
    ensure result >= src
```

A contract:
- Has a name (`NonNegOut`).
- Takes one or more parameters (`out: i64, src: i64`).  A parameter-less contract is a hard error.
- Contains at least one `requires`, `ensure`, `changes`, `preserves`, or `includes` clause.
  A body-less contract is a hard error.
- Is a **top-level declaration** — it lives at the same level as `def`.

### Contracts with frame conditions

```elisa
struct Render:
    px: mutable i32
    py: mutable i32
    health: mutable i32

contract MovesOnly(r: mutable Render&):
    changes r.px, r.py
```

When a function `uses MovesOnly(r)`, writing `r.health` is a frame violation.

### Generic contracts

```elisa
contract Monotonic[T](lo: T, hi: T):
    requires lo <= hi
    ensure result >= lo

def clamp_floor(x: i64) -> i64:
    uses Monotonic[i64](0, 100)
    requires x >= 0
    return 100
```

---

## Application: the `uses` keyword

To apply a contract to a function, use `uses`:

```elisa
def copy_floor(s: i64) -> i64:
    uses NonNegOut(0, s)
    return s
```

When the compiler sees `uses NonNegOut(0, s)`, it substitutes `{out → 0, src → s}` into the
contract's clauses and appends them to `copy_floor`'s own `Requires`/`EnsureValues`:

```
# Effective obligations of copy_floor after expansion:
requires s >= 0      # from NonNegOut.requires (src → s)
ensure result >= s   # from NonNegOut.ensure    (src → s)
```

From the prover's perspective, a `uses`-applied clause is indistinguishable from a hand-written
one.  Discharge uses the same three-tier ladder (fact-lattice → linear prover → SMT).

You can apply multiple contracts on a single function:

```elisa
contract InRange(lo: i64, hi: i64, x: i64):
    requires lo <= x
    requires x <= hi
    ensure result >= lo
    ensure result <= hi

def clamp(x: i64) -> i64:
    uses InRange(0, 100, x)
    requires x >= 0
    requires x <= 100
    return x
```

The composition is **conjunction of value premises ∪ union of frames** (docs/97 §5).

---

## Call-site checking

The inherited `requires` clauses become real preconditions of the applying function.  Any caller
that cannot prove them statically gets an error:

```elisa
contract Positive(x: i64):
    requires x > 0
    ensure result > 0

def use_it(x: i64) -> i64:
    uses Positive(x)
    return x

def bad_caller() -> i64:
    return use_it(0 - 3)   # ERROR: cannot prove x > 0 (x = -3)
```

---

## Including laws

A contract can include a value law as a precondition:

```elisa
law NonNeg(x: i64) = x >= 0

contract NonNegSrc(src: i64):
    includes NonNeg(src)     # becomes: requires src >= 0
    ensure result >= src
```

It can also include an effect law as a prohibition:

```elisa
law NoAlloc forbids Memory.Allocate

contract PureUse(x: i64):
    includes NoAlloc()
```

A function `uses PureUse(x)` must not allocate.

---

## Contracts vs. laws: what is the difference?

| Concept | Purpose | Spelling | Reusability |
|---|---|---|---|
| **law** | A *predicate* — a pure, total boolean function | `law Name(self: T, ...) = <bool-expr>` | Used in type positions (`T is Law`), flow narrows (`if x is Law`), contract clauses |
| **contract** | A *bundle of obligations* — pre/postconditions for a function | `contract Name(...): requires / ensure ...` | Applied to functions via `uses` |

---

## Staged rollout (implementation plan)

| Stage | Feature | Status |
|---|---|---|
| **S0** | `contract` declaration + `uses` inline expansion + discharge | **Implemented** (docs/97) |
| **S1** | `@satisfies(ContractName)` conformance checking on structs/protocols | Not yet implemented |
| **S2** | Quantified predicates over collections inside contracts | Not yet implemented |

---

## See also

- **docs/97** — Original named contracts design.
- **docs/95** — Laws and refinement types cheat-sheet.
- **docs/109** — Unified refinement pipeline; how `requires`/`ensure` discharge.
- **docs/111** — Unified staging plan for ghost models, typestate, and named contracts.
