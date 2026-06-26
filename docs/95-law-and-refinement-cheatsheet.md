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
