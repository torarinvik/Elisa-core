# 109 — Unified refinement pipeline: `where`, `requires`, `ensure`, and `is Law`

All surface refinement forms — `T where <bool-expr>`, `requires`, `ensure`, and `<value> is Law`
— flow through a **single** verification pipeline.  This document describes that pipeline
end-to-end so contributors can reason about new discharge sites, new surface forms, and the
invariant that type identity is never influenced by prover results.

---

## 1. The four stages

```
Surface syntax
     │
     ▼
 SpecSignature   (refinement_scheme.go)
     │
     ▼
 Fact seeding    (seedWhereRefinementFact / seedParamWhereRefinementFacts)
     │
     ▼
 Discharge ladder (proveRequiresClause → trySMTProveRequires → runtime fallback)
```

### Stage 1 — Surface syntax

Five surface spellings, all representation-erased:

| Surface form | Binder position |
|---|---|
| `T where p` on a param | precondition on each call argument |
| `T where p` on a return type | postcondition on every return point |
| `T where p` on a local `var` / `let` | inline assertion at the declaration |
| `requires <bool>` on a `def` | explicit precondition (same ladder) |
| `ensure <bool>` / `<value> is Law` | explicit postcondition (same ladder) |

All five produce the same internal data by the end of stage 2.

### Stage 2 — SpecSignature (canonical representation)

`SpecSignature` (`refinement_scheme.go`) is the **representation-erased verification contract**
for a function.  It survives export/import, interface-summary generation, and specialization.

Key fields:

```go
type SpecSignature struct {
    Params              []SpecBinder
    Result              *SpecBinder
    ParamPredicates     []RefinementPredicate  // from T where p on params
    ResultPredicates    []RefinementPredicate  // from T where p on return
    Requires            []RefinementPredicate  // from explicit requires
    Ensures             []RefinementPredicate  // from explicit ensure / is Law
}
```

A `SpecBinder` gives each param / result a **stable position index** independent of source
spelling, so that cross-module references and specialization copies can always point to the
right slot.

A `RefinementPredicate` carries:
- `Kind` — which surface channel produced it (`RefinementPredicateType` for `where`, `…Requires`,
  `…Ensures`, `…Law`)
- `Subject` — `BinderRef` pointing at the owning binder by position
- `Dependencies` — `[]BinderRef` for other binders mentioned in the predicate expression
- `SourceExpr` — the raw AST so the discharge ladder can substitute into it

### Stage 3 — Fact seeding

Before the body of a function is analyzed the analyzer seeds the prover's fact lattice:

- **`seedParamWhereRefinementFacts`** — for each immutable param with a `where` predicate, adds
  an SMT assert-fact keyed on the predicate expression.  Mutable params are skipped: their value
  may change inside the body, so seeding would be unsound.
- **`seedWhereRefinementFact`** — called after a local `var`/`let` with a `where` type is
  initialized.  Records the predicate as an assert-fact whose **dependency set** (`smtFactDeps`)
  includes every identifier in the predicate.

**Dependence-freeze invalidation rule**: when any identifier in a fact's dependency set is
mutated, `invalidateSMTAssertFactsForTarget` drops that fact from the lattice.  The fact is
"frozen" at its seeding point; mutation restores an unknown state for any predicate that
mentioned the mutated variable.

### Stage 4 — Discharge ladder

All where/requires/ensure predicates are discharged through the same three-tier ladder
(`dischargeWherePredicate` delegates directly to the shared machinery):

1. **Fact-lattice + linear prover** (`proveRequiresClause`) — pattern-matches common arithmetic
   shapes (range comparisons, equalities, monotone chains) against the current fact set.
   O(1) per predicate; no external process.
2. **SMT tier** (`trySMTProveRequires`) — serializes the predicate + current facts into SMT-LIB2
   and calls `z3` as a subprocess.  Only reached when the linear prover returns `requiresUnknown`.
   Enabled by `AnalyzeOptions.EnableSMT`; gated off in fast builds.
3. **Runtime fallback** — if neither prover succeeds the obligation becomes a `proofLint` warning
   (runtime assertion in debug builds).  Under `-strict` / `EnforceStrictProofs` it is a hard
   error at compile time.

The same `proveRequiresClause` / `trySMTProveRequires` pair is called from:
- `dischargeWherePredicate` (where refinements)
- `dischargeRequiresClauses` (explicit `requires`)
- `dischargeEnsureClauses` (explicit `ensure` / `is Law`)

There is **no parallel discharge path** for `where`.

---

## 2. The erasure invariant

> **Runtime type identity never depends on prover results or SMT.**

Concretely:

- `SameType(T where p, T)` → `true` always (the predicate is stripped before the structural
  type-equality check).
- `AssignableTo(T where p, T)` and `AssignableTo(T, T where p)` → both `true`.
- ABI layout, monomorphization keys, and dispatch tables are computed from the base type `T`
  only.
- Passing a plain `T` where `T where p` is expected is **always type-legal**.  The discharge
  ladder may emit a warning, but the program is not ill-typed.

This invariant is enforced by `WhereRefinementTypeExpr` being stripped at the point where types
enter `SameType` / `AssignableTo` (`typeEraseWhere` in the type resolver).

---

## 3. How `where` desugars to the same machinery as `requires`/`ensure`

| Surface | Internal representation | Discharge point |
|---|---|---|
| `def f(n: i64 where p(n))` | `SpecSignature.ParamPredicates[0]` | each call to `f` via `checkCalleeParamWhereRefinements` |
| `def f() -> i64 where p(result)` | `SpecSignature.ResultPredicates[0]` | each `return` stmt via `dischargeReturnWhereRefinement` |
| `x: i64 where p(x) = v` | local assert-fact + seeded via `seedWhereRefinementFact` | at declaration via `dischargeLocalWhereRefinement` |
| `requires p` | `SpecSignature.Requires[0]` | each call site |
| `ensure p` / `result is Law` | `SpecSignature.Ensures[0]` | each `return` stmt |

In every case the substitution map `subst` maps binder names to concrete expressions and is
passed unchanged to `proveRequiresClause`.

---

## 4. Worked example

```elisa
def safe_get(xs: darray[i64], i: i64 where 0 <= i and i < xs.count) -> i64:
    return xs[i]

def caller(xs: darray[i64]) -> i64:
    j: i64 where 0 <= j and j < xs.count = 0
    return safe_get(xs, j)
```

Pipeline trace:

1. **Surface**: `i64 where 0 <= i and i < xs.count` on param `i`.
2. **SpecSignature**: `ParamPredicates[1]` (position 1, after `xs`) with `Dependencies = [xs, i]`.
3. **Fact seeding in `caller`**: `seedWhereRefinementFact` records `0 <= j and j < xs.count` with
   deps `{j, xs}`.  If `j` or `xs` is reassigned, the fact is dropped.
4. **Discharge at call site**: `checkCalleeParamWhereRefinements` builds `subst = {i: j}` and
   calls `proveRequiresClause(0 <= j and j < xs.count, {i: j})`.  The linear prover finds the
   matching assert-fact and returns `requiresProven`.  No SMT call needed.

If `j` were reassigned to an unknown value before the call, step 4 would reach the SMT tier or
emit a `proofLint`.

---

## 5. Table: surface form → canonical slot

| Surface form | SpecSignature field | RefinementPredicateKind |
|---|---|---|
| param `T where p` | `ParamPredicates` | `RefinementPredicateType` |
| return `T where p` | `ResultPredicates` | `RefinementPredicateType` |
| `requires <bool>` | `Requires` | `RefinementPredicateRequires` |
| `ensure <bool>` | `Ensures` | `RefinementPredicateEnsures` |
| `<value> is Law[args]` in ensure position | `Ensures` | `RefinementPredicateLaw` |
| local `var x: T where p` | assert-fact only (not in SpecSignature) | — |

Local `where` facts do not appear in `SpecSignature` because they are not part of the function's
observable contract; they are purely intra-procedural discharge obligations.

---

## 6. Extension points

- **New surface form**: add a `RefinementPredicateKind`, populate the appropriate
  `SpecSignature` field in `buildSpecSignatureFromFuncDecl`, and call `dischargeWherePredicate`
  (or the shared `dischargeRequiresClauses` helper) at the relevant AST node.
- **New discharge tier**: insert a new case between tiers 1 and 2 in `dischargeWherePredicate`
  and in the parallel `dischargeRequiresClauses` path; the runtime-fallback case should always
  remain last.
- **New seeding site**: call `seedWhereRefinementFact` (or a new variant) after any assignment
  whose RHS satisfies a statically known predicate; `smtFactDeps` handles invalidation
  automatically.

---

## 7. References

- `compiler/src/semantic/refinement_scheme.go` — `SpecSignature`, `SpecBinder`, `RefinementPredicate`
- `compiler/src/semantic/analyzer_where_refinements.go` — all four discharge/seed entry points
- `compiler/src/semantic/analyzer_refinement_scheme.go` — `buildSpecSignatureFromFuncDecl`
- `docs/85-contract-algebra.md` — discharge ladder detail, proof-hole semantics
- `docs/95-law-and-refinement-cheatsheet.md` — surface spelling reference
- `docs/90-smt-discharge-tier.md` — SMT tier internals
