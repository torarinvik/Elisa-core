# 95 — Laws, refinement types, and contracts: one-page cheat-sheet

The recurring confusion is treating a **law** as if it were a **type**, or inventing a
`refinement` keyword. There isn't one. The model (docs/85 §1) is three roles and one operator:

| Concept | What it is | Spelling |
|---|---|---|
| **law** | a *predicate* — a pure, total `bool` function whose first parameter (`self`) is the subject | `law Name(self: T, ...) = <bool-expr>` |
| **`is`** | the *one* application operator: `x is P[a]` ≡ `P(x, a)` (same first-arg binding as UFCS) | type / flow / contract position |
| **refinement type** | a base type carrying a proven law | `T is Law[args]`, named via `type N = T is Law[args]` |
| **contract** | a function's pre/postconditions, stated with laws | `requires <bool>` / `ensure <bool>` on a `def` |

A **law is not a type.** A refinement type is `base is law`. `requires`/`ensure` belong to
functions, never inside a law.

## The one operator, three positions
```elisa
law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi   # the predicate

type RegIndex = u32 is InRange[0, 15]      # TYPE position    -> a refinement type
def f(x: u32) -> u32:
    if x is InRange[0, 15]:                # FLOW position    -> narrows x in the branch
        ...
def g(x: u32) -> u32 is InRange[0, 15]:    # CONTRACT position -> obligation on the return
    requires x is InRange[0, 15]           #                  -> obligation on the argument
    return x
```

## Bigger / comprehensive laws
A law is just a pure `bool` function, so "bigger" = a block body, quantifiers, and composition:
```elisa
law all_regs_valid(self: darray[u32], n: i64) =
    forall i: (0 <= i and i < n) implies self[i] is InRange[0, 15]   # quantified

law well_formed(self: DecodedR):                                     # block body + conjunction
    regs  = self.dst is InRange[0,15] and self.a is InRange[0,15]
    funct = self.funct <= 7
    return regs and funct
```
Compose **value-class** laws by conjunction or `self is A and self is B` — **not** `includes`
(`includes` is only for subjectless function-level laws: `NoAlloc`, `forbids Abort.Panic`, …).

## Common mistakes → fixes
| You wrote | Problem | Fix |
|---|---|---|
| `x: u32 RegIndex` | juxtaposition — no `is` | `x: u32 is RegIndex` |
| `law L(self: T): requires …; ensure …` | laws are predicates, not contracts | `law L(self: T) = <bool>`; put `requires`/`ensure` on a `def` |
| `refinement RegIndex(self: u8): …` | no such keyword | `law reg(self: u8) = self < 16` + `type RegIndex = u8 is reg` |
| `law Big includes Positive` | `includes` can't take a value law | `law Big(self: T) = self is Positive and …` |

## Anonymous binder refinements: `T where <bool-expr>`

`T where predicate` is **sugar for an inline anonymous law** applied at a single binder position.
It is representation-erased (exactly like `T is Law`), but the predicate is preserved for proof
obligations without requiring you to name a law first.

### Spelling in each binder position

```elisa
# Parameter: predicate may reference that param + any earlier params in scope.
def get(xs: darray[i64], i: i64 where 0 <= i and i < xs.count) -> i64:
    return xs[i]

# Return: predicate may reference `result` (the implicit return name) and all params.
def abs(n: i64) -> i64 where result >= 0:
    return if n >= 0: n else: -n

# Local variable: predicate may reference the variable name + all in-scope values.
def safe_index(xs: darray[i64]) -> i64:
    i: i64 where 0 <= i and i < xs.count = 0
    return xs[i]
```

### Rules and restrictions

| Rule | Details |
|---|---|
| **Predicate must be a pure bool expression** | Any impure call (I/O, allocation, mutation) is rejected. |
| **Scope of identifiers** | Param `where`: the param itself + params declared before it. Return `where`: params + implicit `result`. Local `where`: the declared name + any in-scope bindings. |
| **Representation erasure** | `T where p` resolves to `T` for `SameType`, `AssignableTo`, ABI, layout, and monomorphization. The predicate is proof metadata only. |
| **Discharge** | Same discharge ladder as `requires`/`ensure` (docs/85 §6): fact-lattice → tier-2 linear → SMT. Unprovable predicates emit a `proofLint` warning (runtime-checked in debug builds; a hard error under `-strict`). |

### Relationship to `requires`, `ensure`, and `is Law`

`T where p` is syntactic sugar — the compiler desugars it into the `requires`/`ensure`
machinery used by named-law refinements:

| Surface form | Desugars to |
|---|---|
| `def f(n: i64 where p(n))` | `def f(n: i64)` + `requires p(n)` |
| `def f() -> i64 where p(result)` | `def f() -> i64` + `ensure p(result)` |
| `x: i64 where p(x) = v` | local `x: i64 = v` + inline assertion that `p(x)` holds at declaration |

Prefer named laws (`T is Law`) when the predicate is reused across multiple sites.
Use `where` for one-off inline constraints that are too specific to name.

### What does NOT change with `where`

- `T where p` is assignable to and from `T` — there is no subtype relationship.
- Passing a plain `T` value where `T where p` is expected is legal at the call site;
  the proof obligation is discharged there (or runtime-checked).
- `SameType(T where p, T)` is `true`. The predicate is invisible to monomorphization and generics.

## Named refinement aliases: `refine NAME = BASE where PRED`

A **named refinement alias** is a reusable, declaration-level shorthand for an anonymous
`where` refinement.  It names a predicate once and lets you use the name as a type in binder
positions.

```elisa
refine Positive = i64 where self > 0

def needs_positive(n: Positive) -> i64:
    return n

def caller() -> i64:
    return needs_positive(5)   # proven: 5 > 0 ✓
```

### Parametric aliases

The alias may carry value parameters (square-bracket suffix), which are substituted at each
use site:

```elisa
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:
    return xs[i]
```

At the call site the compiler substitutes the concrete `xs` argument into `self >= 0 and self <
xs.count`, then discharges via the normal three-tier ladder.

### Restrictions

| Restriction | Details |
|---|---|
| **Binder positions only** | A `refine` alias may appear as a parameter type, return type, or local-variable type annotation.  Using it as an ordinary type (e.g., inside `darray[Positive]`) is a hard error: *"may only be used in a binder position"*. |
| **Representation erasure** | The alias erases to its base type.  `SameType(Positive param, i64 param)` is `true`; `AssignableTo` is bidirectional.  No runtime tag, no monomorphization key change. |
| **Same discharge ladder** | The desugared `where` predicate goes through the same fact-lattice → linear → SMT ladder as any anonymous `where`.  Violations are `proofLint` warnings (hard errors under `-strict`). |

The compiler desugars a `refine` alias in a binder position by rewriting the binder's type node
to a `WhereRefinementTypeExpr` in place, so the rest of the pipeline sees no difference from a
hand-written `where`.

### Relationship to `type N = T is Law`

| Form | When to use |
|---|---|
| `refine N = T where pred` | Predicate is complex / bespoke; you want a short name for one-off binder annotation. |
| `law L(self: T) = pred` + `type N = T is L` | Predicate is reusable in flow (`if x is L`), contracts (`requires`), and type aliases; promotes to first-class law. |

---

## `where` on struct fields

A struct field may carry a `where` predicate.  The compiler discharges the predicate at every
**struct construction** site, both named-argument and positional form.

```elisa
struct Pos:
    x: i64 where x > 0

def make_valid() -> Pos:
    return Pos(x: 5)     # proven: 5 > 0 ✓

def make_pos() -> Pos:
    return Pos(10)        # positional form also checked ✓

# Pos(x: -1) and Pos(0) are hard errors / proofLint violations.
```

### Rules and restrictions

| Rule | Details |
|---|---|
| **Discharge site** | Each struct literal (named or positional) — not field reads or assignments to fields after construction. |
| **Self-reference only (v1)** | The predicate may reference the field itself by its own name (`x > 0`).  Cross-field references (`hi > lo`) produce a clear diagnostic: *"cross-field refinement not supported"*. |
| **Representation erasure** | Reading `p.x` yields plain `i64`, not a where-refined type.  The predicate is construction-time proof metadata only. |
| **Same discharge ladder** | Fact-lattice → linear → SMT, with `proofLint` fallback (hard error under `-strict`). |

---

## Refinement subsumption (interval entailment)

When a value is known to satisfy a **stronger** law (a narrower interval), the compiler
statically concludes it also satisfies a **weaker** law (a looser bound), with no runtime check
emitted.

```elisa
law Positive(self: i64) = self > 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_positive(x: i64 is Positive) -> i64:
    return x

def caller(v: i64 is InRange[1, 100]) -> i64:
    return need_positive(v)   # InRange[1,100] entails Positive: lo=1 > 0 ✓
```

### What entails what

| Known fact | Goal | Result |
|---|---|---|
| `InRange[lo, hi]` with `lo > 0` | `Positive` (`> 0`) | proven statically |
| `InRange[lo, hi]` with `lo = 0` | `Positive` (`> 0`) | NOT entailed → runtime check |
| `InRange[lo, hi-1]` | `UpperBound[hi]` (`< hi`) | proven statically |
| `InRange[n, n]` (point) | any comparison that `n` satisfies | proven statically |
| Weaker law → stronger goal | any case | NOT entailed (soundness) |

Subsumption also applies to **return types**: a callee returning `i64 is InRange[1, 10]` placed
in a context that needs `i64 is Positive` is statically accepted (lo=1 > 0).

### Soundness note

The entailment check is **sound-only**: the prover concludes "entailed" only when it can prove
the implication from the interval bounds.  It never falsely accepts.  If the bounds are too
loose (e.g., `lo = 0` for a `> 0` goal), it falls through to the runtime-check tier rather
than guessing.

---

## What proves (discharge ladder, docs/85 §6)
Conjunctions of bounds → always-on. `implies` / multi-variable linear → tier-2 (budgeted).
`forall`/`exists` and bit masks/shifts → the SMT tier (on by default; `-nosmt` to disable).
Bit-level decode reasoning (masks, runtime shifts, sign-extension, refined-index elision) is
covered in docs/94.

## Guardrails for the refinement-scheme direction
The next consolidation should preserve these invariants:

- Refinements erase for runtime representation, ABI/layout, monomorphization, `SameType`, and ordinary `AssignableTo`.
- Proof metadata still survives in verification signatures: refined params/returns, `requires`, `ensure`, and `ensures p is Law` are obligations and reusable facts, not runtime type constructors.
- SMT is a discharge tier only. It may prove verification obligations, but it must not participate in type equality/assignability and must not feed proof-indexed storage or bounds-check elision.
- Dependent facts are frozen to the values they mention. Mutating `xs` invalidates facts such as `i is Bounded[0, xs.count]`.
- Proof-indexed APIs come before proof-indexed storage. Prefer refined parameters, return contracts, and explicit witnesses before making container element layout depend on proofs.
