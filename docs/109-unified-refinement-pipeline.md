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

Seven surface spellings, all representation-erased:

| Surface form | Binder position |
|---|---|
| `T where p` on a param | precondition on each call argument |
| `T where p` on a return type | postcondition on every return point |
| `T where p` on a local `var` / `let` | inline assertion at the declaration |
| `refine NAME = T where p` (named alias used in a binder) | desugars to `T where p` in place; see below |
| struct field `f: T where p` | precondition at each struct literal |
| `requires <bool>` on a `def` | explicit precondition (same ladder) |
| `ensure <bool>` / `<value> is Law` | explicit postcondition (same ladder) |

All seven produce the same internal data by the end of stage 2.

#### Named refinement aliases

`refine NAME = BASE where PRED` declares a named alias.  When `NAME` appears as a binder type
the compiler rewrites the binder's AST node to `WhereRefinementTypeExpr` in place before
building the `SpecSignature`.  Parametric aliases carry value parameters substituted at each
use site:

```elisa
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:   # desugars to: i: i64 where self >= 0 and self < xs.count
    return xs[i]
```

A `refine` alias is **binder-position-only**: using it outside a param, return, or local
variable annotation is a compile error.  Internally the rewrite is transparent — the downstream
pipeline sees only a `WhereRefinementTypeExpr`, never the alias name.  Erasure is preserved:
`SameType(IndexOf[xs] param, i64 param)` is `true`.

#### Struct field `where` predicates

A struct field declaration may carry a `where` predicate:

```elisa
struct Pos:
    x: i64 where x > 0
```

The predicate is discharged at every struct literal (named-arg and positional forms) via
`analyzeStructFieldWhereRefinement`.  It is **not** a `SpecSignature` slot — it is an
intra-construction obligation.  Cross-field references (e.g., `hi > lo` where `lo` is another
field) are not supported in v1 and produce a diagnostic.  Erasure is preserved: reading `p.x`
yields plain `i64`.

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
| `refine N = T where p` used as param type | `ParamPredicates` (desugared) | `RefinementPredicateType` |
| return `T where p` | `ResultPredicates` | `RefinementPredicateType` |
| `requires <bool>` | `Requires` | `RefinementPredicateRequires` |
| `ensure <bool>` | `Ensures` | `RefinementPredicateEnsures` |
| `<value> is Law[args]` in ensure position | `Ensures` | `RefinementPredicateLaw` |
| local `var x: T where p` | assert-fact only (not in SpecSignature) | — |
| struct field `f: T where p` | assert-fact at construction (not in SpecSignature) | — |

Local `where` facts and struct field `where` predicates do not appear in `SpecSignature` because
they are not part of the function's observable contract; they are intra-procedural / intra-construction
discharge obligations.

---

## 6. Refinement subsumption (interval entailment)

The discharge ladder includes a **subsumption check** before the SMT tier: when the current
fact set contains a law whose interval bounds imply the goal law's bounds, the obligation is
discharged statically without emitting a runtime check.

The key function is `refinementPredicatesEntail` / `refinementPredicateIntervalEntails`.

### What the prover checks

Given a known law `InRange(self, lo, hi)` (i.e., `self >= lo and self <= hi`) and a goal law:

- **Goal `Positive` (`self > 0`)**: entailed iff `lo > 0`.
- **Goal `UpperBound[n]` (`self < n`)**: entailed iff `hi < n` (equivalently, `hi <= n-1`).
- **Goal is the same `InRange[lo', hi']`**: entailed iff `lo' <= lo and hi <= hi'`.
- **Point interval `InRange[n, n]`**: checked against any single-sided comparison the constant `n` satisfies.

### Soundness

The check is **sound-only**: it concludes "entailed" only when the implication follows from the
interval bounds.  Weaker-implies-stronger and ambiguous cases fall through to SMT or the runtime
tier — they are never silently accepted.

```elisa
law Positive(self: i64) = self > 0
law InRange(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def need_pos(x: i64 is Positive) -> i64:  return x

def ok(v: i64 is InRange[1, 100]) -> i64:
    return need_pos(v)    # lo=1 > 0 → statically proven, no runtime check

def not_ok(v: i64 is InRange[0, 100]) -> i64:
    return need_pos(v)    # lo=0 is NOT > 0 → falls through to runtime check
```

Subsumption also applies to **return-type positions**: a callee returning `i64 is InRange[1, 10]`
satisfies a caller's `i64 is Positive` return contract without a runtime check (lo=1 > 0).

---

## 8. Extension points

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

## 9. References

- `compiler/src/semantic/refinement_scheme.go` — `SpecSignature`, `SpecBinder`, `RefinementPredicate`
- `compiler/src/semantic/analyzer_where_refinements.go` — all discharge/seed entry points
- `compiler/src/semantic/analyzer_refinement_scheme.go` — `buildSpecSignatureFromFuncDecl`
- `compiler/src/semantic/analyzer_named_refinement_alias.go` — `refine NAME = BASE where PRED` desugaring
- `compiler/src/semantic/named_refinement_alias_test.go` — alias tests
- `compiler/src/semantic/where_struct_field_test.go` — struct field `where` tests
- `compiler/src/semantic/refinement_subsumption_test.go` — interval entailment tests
- `docs/85-contract-algebra.md` — discharge ladder detail, proof-hole semantics
- `docs/95-law-and-refinement-cheatsheet.md` — surface spelling reference
- `docs/90-smt-discharge-tier.md` — SMT tier internals
