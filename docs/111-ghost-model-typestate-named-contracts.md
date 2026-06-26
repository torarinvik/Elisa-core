# 111 — Ghost models, typestate, and named contracts: a unified staging plan

> **(design / not yet implemented)** — everything in this document describes planned
> language features.  No compiler source has been modified.

Cross-references: docs/109 (unified refinement pipeline), docs/110 (progressive correctness
ladder), docs/96 (typestate protocols), docs/97 (named composable contracts).

---

## Overview

Three features extend Elisa's correctness story beyond the current value-refinement /
frame-condition / typestate pillars:

| Feature | One-line summary | Verification layer |
|---|---|---|
| **Ghost model fields** | Abstract spec state on a struct, erased from the runtime representation | Obligation/fact/prover (docs/109) |
| **Typestate** (extended) | Compile-time state index on a type; illegal orderings are unrepresentable | Named-state + borrow machinery (docs/96) |
| **Named contracts** (extended) | Reusable `contract C(args)` bundles applied with `uses C(args)` | Value + frame discharge (docs/97) |

docs/96 and docs/97 contain the foundational designs for typestate and named contracts.
This document adds:

1. The **ghost-model** feature (entirely new).
2. A concrete **staged implementation plan** (S0..Sn) for all three features.
3. Cross-cutting concerns: ghost-state framing across calls, typestate + aliasing,
   and named-contract discharge.

---

## Part 1 — Ghost model fields

### 1.1 Motivation

A `DynArray[T]` backed by a contiguous buffer has a simple logical model: it is a mathematical
sequence `seq[T]`.  Today a function like `push` can say:

```elisa
# docs/109 refinement pipeline — available today
def push(self: mutable DynArray[T]&, v: T):
    requires self.len < self.cap
    ensure   self.len == old(self.len) + 1
```

But callers cannot yet write:

```elisa
    ensure self.model == old(self.model) ++ [v]   # ghost model — not yet
```

The `model` field carries abstract logical content that is **not part of the C-layout struct**.
It exists only in spec annotations; the compiler erases it from `sizeof`, pointer arithmetic,
and code generation.  It lets a callee's postcondition describe *what happened logically*
without committing to the concrete layout.

### 1.2 Syntax

```elisa
struct RingBuf[T]:
    head: usize
    tail: usize
    buf:  darray[T]

    # --- ghost section ---
    ghost model: seq[T]           # abstract spec state; zero runtime bytes
    ghost invariant:              # must hold at every public entry/exit point
        model == buf[head..<tail] # (pseudocode — uses SMT seq theory)
```

Grammar addition (design):

```
StructBody      := ( FieldDecl | GhostFieldDecl | GhostInvariantDecl | … )*
GhostFieldDecl  := "ghost" Ident ":" Type NEWLINE
GhostInvariantDecl := "ghost" "invariant" ":" NEWLINE INDENT BoolExpr DEDENT
```

`ghost` is a new keyword.  It may only appear inside a `struct` body or as a qualifier on a
`let`/`var` binding inside a spec-only context (see §1.5).

### 1.3 Erasure rules

| Context | Rule |
|---|---|
| `sizeof(S)` / `alignof(S)` | Ghost fields are invisible — size unchanged. |
| Struct literal `S(a, b)` | Ghost fields are not positional args.  They are derived from the ghost invariant. |
| Field access `s.model` | Legal **only** inside `requires`, `ensure`, `where`, `ghost invariant`, and `is Law` predicates.  Compile error elsewhere. |
| Code generation | Ghost fields produce no LLVM IR.  The ghost-invariant predicate is lowered to an obligation at every public function boundary (see §1.4). |
| Debug builds | Optionally, ghost invariants can be materialised as runtime assertions behind `-fghost-check` (the invariant predicate must then be computable without external SMT). |

### 1.4 Obligation lowering

At every function that accepts or returns a `struct` with a `ghost invariant`:

- **Precondition** (caller side): the compiler seeds a fact `ghost_inv(s)` from the
  ghost-invariant predicate into the caller's fact environment, matching the discharge ladder
  in docs/109 §4.
- **Postcondition** (callee side): the compiler emits a proof obligation `ghost_inv(s')` for
  the output state `s'`.  This obligation travels through the same ladder: flow → linear →
  SMT → runtime fallback under `-fghost-check`.

```
Ghost invariant lowering (per public function boundary)
─────────────────────────────────────────────────────
 entry:  assume ghost_inv(param)      ← seeded as a refinement fact
 exit:   prove  ghost_inv(result)     ← discharged on the ladder
```

This reuses `seedWhereRefinementFact` / `proveRequiresClause` from docs/109 without new
infrastructure: `ghost_inv(x)` is just a boolean predicate node, the same as any `where`
predicate.

### 1.5 Ghost-local variables in specs

Inside `requires`/`ensure`/`is Law` blocks, a `ghost let` introduces a temporary proof
witness:

```elisa
def merge(out: mutable darray[i64]&, a: darray[i64], b: darray[i64]):
    requires IsSorted(a) and IsSorted(b)
    ensure   IsSorted(out)
    ensure   ghost let perm = out.model: IsPermutation(perm, a.model ++ b.model)
```

`ghost let` bindings are erased entirely and exist only for the proof obligation.

### 1.6 Ghost-state framing across calls (open question)

The hardest problem: if `f` calls `g`, and `g` mutates `self`, the caller's ghost model for
`self` is invalidated.  There are two sound options:

**Option A — explicit frame:** `g` must declare `changes self.model` (docs/87 frame
conditions).  The caller's ghost-model fact is invalidated, and `f` must re-establish it
from `g`'s postcondition.  This is the safe-default approach and requires no new machinery.

**Option B — ghost-frame inference (research):** the compiler infers which ghost fields a
call may touch using a simple syntactic scan of the callee's `ensure` clauses and ghost
invariant.  Only ghost fields mentioned in those clauses are invalidated.  This is an
optimization over Option A — same soundness, less annotation burden — but requires an
interprocedural pass not yet designed.

**Staging recommendation:** implement Option A in S0; revisit Option B in a later stage.

### 1.7 Soundness invariant

Ghost fields are proof-only iff:
1. No ghost field appears in a non-spec expression.  The checker must reject `x = self.model`
   outside a spec context.
2. Ghost invariants are checked at every public entry/exit, not just at construction.
3. Ghost fields participate in no aliasing rules — they have no addresses.

---

## Part 2 — Typestate (extended staging)

docs/96 contains the complete design.  This section adds the staged implementation plan and
the aliasing interaction analysis.

### 2.1 Staging plan (S0..S3)

**S0 — struct[state] desugaring (no new surface syntax)**

Lower `typestate T: states: A, B, C` into:

```elisa
struct T[state S | A | B | C]:
    derive state: S
    # user fields unchanged
```

The `derive state` discriminant is a zero-cost enum field.  `is A` lowers to a discriminant
check using the existing `is` operator.  All soundness is inherited from enum-based dispatch.

Deliverables: parser extension for `typestate`, lowering pass in the semantic layer, no
prover changes.

**S1 — transition functions + `ensures p => NewState` postconditions**

Transition functions are ordinary functions over `mutable T[S]&`.  The compiler verifies that
on all exit paths the subject is in the declared target state.  This lowers to an `ensure
self is NewState` postcondition discharged through the existing poststate checker
(`appendImplicitPreservePoststates` in docs/96 §4).

Deliverables: `transition` keyword in the `typestate` body; poststate discharge via the
existing frame-condition infrastructure.

**S2 — affine consumption (linear typestate)**

A typestate instance that must be consumed exactly once is declared:

```elisa
typestate Builder[T]:
    linear                   # consumed-once flag
    states: Open, Sealed
    transition seal: Open -> Sealed
    transition finish: Sealed -> (consumed)
```

`(consumed)` is a pseudo-state meaning the value is moved out and may not be used again.
This lowers to Elisa's existing affine/ownership checks (the same mechanism that prevents
double-free of arena handles).

Deliverables: `linear` flag; `(consumed)` pseudo-state; move-out enforcement via ownership
checker.

**S3 — protocol composition (open question)**

Two typestate machines can be composed in a struct: `struct Conn[sockS, tlsS]`.  The compiler
must track both indices independently.  This requires extending the state-index representation
from a single enum to a tuple.  Design is deferred; S0..S2 are fully useful without it.

### 2.2 Typestate + aliasing (open question)

A `mutable T[S]&` borrowed reference creates an alias to a typestate value.  If a second
alias can observe the state mid-transition, soundness breaks.

Current answer: Elisa's existing mutable-alias checker already prevents two simultaneous
`mutable` borrows.  A transition function holds the only mutable borrow, so no second alias
can witness the intermediate state.  Immutable borrows (`T[S]&` without `mutable`) are
read-only and see only the pre-transition state.

The one residual gap: if a transition function stores `self` into a data structure that is
also readable by the caller, the caller could observe the state change out-of-order.  This is
the same aliasing gap that docs/`noalias-blocked-by-region-escape.md` addresses for
forwarded-ref parameters.  The fix is the same: the storage-class UNION check must also
cover typestate transitions.  This is flagged as an open item for S1 review.

---

## Part 3 — Named contracts (extended staging)

docs/97 contains the complete design.  This section adds the staged implementation plan and
the discharge-path analysis.

### 3.1 Staging plan (S0..S2)

**S0 — declaration + expansion (macro-like)**

A `contract` declaration is stored in the symbol table as a named list of
`requires`/`ensure`/`changes`/`preserves` clauses.  `uses C(args)` at a function site
expands those clauses inline before the standard discharge ladder runs.  No new prover logic;
the expansion is a pre-pass that runs before docs/109 Stage 2 (fact seeding).

```
contract Permutation(out: darray[i64]&, src: darray[i64]):
    ensure IsPermutation(out, src)
    ensure out.len == src.len

def sort(xs: mutable darray[i64]&):
    uses Permutation(xs, old(xs))
    uses Sorted(xs)
    # compiler expands to:
    #   ensure IsPermutation(xs, old(xs))
    #   ensure xs.len == old(xs).len
    #   ensure IsSorted(xs)
```

Deliverables: `contract` keyword; `uses` keyword; symbol-table entry; inline-expansion pass;
no new AST node for the obligation (reuses existing `requires`/`ensure` nodes).

**S1 — contract conformance checking**

A function can *claim* it satisfies a contract without `uses`:

```elisa
@satisfies(Permutation)
def sort(xs: mutable darray[i64]&): …
```

The compiler checks that the function's obligations imply the contract's obligations.  This is
an entailment check in the existing linear/SMT prover.

Deliverables: `@satisfies` annotation; implication check at function boundary.

**S2 — parameterised contracts + higher-order uses (research)**

A contract can quantify over a predicate parameter:

```elisa
contract Monotone[P](f: fn(i64) -> i64):
    ensure forall x, y: x <= y => P(f(x), f(y))
```

This requires first-order quantifier support in the SMT backend (docs/100 quantifier sugar).
Deferred until quantifier sugar is stable.

### 3.2 Interaction with ghost models

A named contract may reference ghost fields:

```elisa
contract QueueInvariant(q: RingBuf[T]&):
    ensure q.model == old(q.model) ++ [last_pushed]
```

This is legal because `q.model` is a ghost field and `uses QueueInvariant(q)` is a spec
context.  Ghost-field erasure rules (§1.3) still apply; the expansion is proof-only.

---

## Part 4 — Cross-cutting concerns

### 4.1 Interaction table

| Combination | Status | Note |
|---|---|---|
| Ghost model + typestate | Compatible | Ghost invariant may mention the state index: `ghost invariant: state == Open => model.len < cap` |
| Ghost model + named contract | Compatible | Contracts may reference ghost fields (§3.2) |
| Typestate + named contract | Compatible | A contract can require a particular state: `requires self is Open` |
| All three | Compatible | No fundamental interaction conflicts; compose freely |

### 4.2 Discharge class summary

All three features discharge through the existing ladder (docs/109 §4):

```
flow  →  linear prover  →  SMT  →  runtime (debug/-fghost-check)
```

No new discharge class is introduced.  Ghost-invariant obligations are boolean predicates in
the same prover language as `where` and `requires`.

### 4.3 Erasure guarantees

| Feature | Runtime footprint |
|---|---|
| Ghost model fields | Zero bytes (erased from struct layout and all codegen) |
| Typestate index | Zero bytes (the `derive state` discriminant IS a runtime field — one enum word — because transitions are checked at runtime in debug builds; in release the field may be optimised away if no runtime check uses it) |
| Named contract expansion | Zero bytes (macro expansion; all verification is compile-time) |

---

## Part 5 — Staged rollout summary

| Stage | Ghost model | Typestate | Named contracts |
|---|---|---|---|
| S0 | `ghost` field parse + erasure; ghost-inv obligations at public boundaries; Option A framing | `typestate` desugars to `struct[state]` + `derive state` | `contract` decl + `uses` inline expansion |
| S1 | Ghost-local `ghost let` witness in specs | Transition functions + `ensures p => NewState` discharge | `@satisfies` conformance check |
| S2 | `-fghost-check` runtime materialisation | Affine/linear typestate (`linear` flag, `(consumed)`) | (deferred: parameterised contracts) |
| S3 | Ghost-frame inference (Option B, research) | Protocol composition (`struct[s1, s2]`) | (deferred: higher-order `uses`) |

---

## Open questions

1. **Ghost-state framing (§1.6, Option B):** interprocedural ghost-frame inference is
   unsolved.  Option A (explicit `changes self.model`) is sound and ships in S0.

2. **Typestate + aliasing gap (§2.2):** the storage-class UNION check must be extended to
   cover typestate transitions before S1 ships.  Until then, the existing mutable-alias
   checker provides a conservative guard.

3. **Ghost-invariant computability:** the `-fghost-check` runtime mode (S2) requires the
   ghost-invariant predicate to be expressible in Elisa without external SMT.  Invariants
   that mention mathematical sequences (`seq[T]`) may not be directly executable.  A
   restricted `-fghost-check`-compatible predicate language needs to be defined.

4. **Typestate index in generics:** `fn[T, S] f(x: T[S])` requires the compiler to track
   the state index as a generic parameter.  The type-parameter machinery must be extended.
   Deferred to S3.

5. **Named-contract versioning:** if a contract's obligations change, all `uses` sites
   silently gain new obligations.  Whether this should require an explicit version bump or
   re-verification pass is an open policy question.
