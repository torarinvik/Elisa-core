# 110 — Progressive correctness: the correctness ladder

Elisa's verification story is built around one idea: **correctness is opt-in at every level**.
Ordinary code carries no proof burden.  Adding a `where` predicate, a `requires` clause, or a
`law` increments the rigor in-place — the same function, the same pipeline, a harder discharge
obligation.  You pay only for what you claim.

This document describes the correctness ladder, the proof modes that serve each rung, and the
two invariants that keep the whole system ergonomic.  Cross-references: docs/109 (unified
pipeline internals), docs/95 (surface spelling cheat-sheet).

---

## 1. The correctness ladder

The six rungs are ordered from least to most rigorous.  Each rung is strictly optional until
the cost of *not* having it becomes higher than the annotation burden.

### Level 0 — Memory safety (always on, zero annotation)

Elisa's ownership/borrow system, region inference, and runtime bounds-check mode (`-fbounds-check`)
eliminate the classic unsafe-C class of bugs with no user annotation.

```elisa
def sum(xs: darray[i64]) -> i64:
    total: i64 = 0
    for x in xs:
        total = total + x
    return total
```

No annotation required.  Regions are inferred.  Bounds are checked at runtime under
`-fbounds-check` (the default in debug builds).  No contracts; no proofs.

### Level 1 — Inline refinements (`where`)

A `where` predicate on a param or return type adds a proof obligation at each call site.  It is
the lightest-weight way to say "this function has a precondition".

```elisa
def get(xs: darray[i64], i: i64 where 0 <= i and i < xs.count) -> i64:
    return xs[i]
```

The compiler tries to prove `0 <= i and i < xs.count` at each call site using the fact lattice.
If it can, no runtime check is emitted.  If it cannot, a `proofLint` warning is issued and a
debug-build assertion guards the call.  Under `-strict`, an unprovable obligation is a hard
compile error.

`where` on a return type is a lightweight postcondition:

```elisa
def abs(n: i64) -> i64 where result >= 0:
    return if n >= 0: n else: -n
```

### Level 1a — Named refinement aliases (`refine`)

When a `where` predicate is used in several binder positions but is not yet complex enough to
warrant a full `law`, name it with `refine`:

```elisa
refine Positive = i64 where self > 0

def clamp_pos(n: Positive) -> Positive:
    return n
```

The alias desugars to the equivalent `where` at each binder position and is representation-erased
(passing a plain `i64` is still type-legal; the proof obligation is discharged at the call site).
`refine` aliases are **binder-position-only**: using one inside `darray[Positive]` is a compile
error.

Parametric aliases carry value arguments substituted at each use:

```elisa
refine IndexOf[T](xs: darray[T]) = i64 where self >= 0 and self < xs.count

def get(xs: darray[i64], i: IndexOf[xs]) -> i64:
    return xs[i]
```

### Level 1b — Struct field invariants (`where` on fields)

A struct field may carry a `where` predicate that is discharged at every construction site:

```elisa
struct Pos:
    x: i64 where x > 0

def make() -> Pos:
    return Pos(x: 5)    # proven: 5 > 0 ✓
```

Reading `p.x` yields plain `i64` (erasure preserved).  Cross-field predicates (`hi > lo`) are
not yet supported in v1 and produce a diagnostic.

### Level 2 — Named laws and refinement types

When a predicate is reused across multiple sites, name it as a law and build a refinement type:

```elisa
law in_range(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

type Index[n: i64] = i64 is in_range[0, n]

def get(xs: darray[i64], i: Index[xs.count]) -> i64:
    return xs[i]
```

`Index[xs.count]` is a refinement type — `i64` with a proven predicate attached.  The base type
(`i64`) governs layout, ABI, and monomorphization.  The predicate is proof metadata only.

#### Refinement subsumption

When the compiler knows a value satisfies a **stronger** law (a narrower interval), it
automatically concludes the value satisfies any **weaker** goal law, with no runtime check:

```elisa
law Positive(self: i64)                    = self > 0
law InRange(self: i64, lo: i64, hi: i64)  = self >= lo and self <= hi

def need_pos(x: i64 is Positive) -> i64:  return x

def caller(v: i64 is InRange[1, 100]) -> i64:
    return need_pos(v)    # lo=1 > 0 → statically entailed, zero runtime cost
```

The entailment check is **sound-only**: `InRange[0, 100]` does NOT entail `Positive` (lo=0 is
not > 0) and falls through to a runtime check.  See docs/109 §6 for full rules.

### Level 3 — Explicit contracts (`requires` / `ensure`)

Contracts spell out preconditions and postconditions as separate clauses, which makes the
observable interface explicit and machine-checkable across module boundaries.

```elisa
def get(xs: darray[i64], i: i64) -> i64:
    requires 0 <= i and i < xs.count
    return xs[i]

def abs(n: i64) -> i64:
    ensure result >= 0
    return if n >= 0: n else: -n
```

`requires` and `ensure` go through the **same** discharge pipeline as `where` (docs/109 §4).
`SpecSignature` records them as `Requires` / `Ensures` predicates; cross-module callers see them
and must discharge them.

Combining all three forms on the same function is legal — they stack:

```elisa
def safe_div(a: i64, b: i64 where b != 0) -> i64:
    requires a >= 0
    ensure result >= 0
    return a / b
```

### Level 4 — `is Law` postconditions and composed proofs

For serious algebraic properties, write laws that compose and attach them as return contracts:

```elisa
law commutes(self: i64, b: i64) = self + b == b + self

def add(a: i64, b: i64) -> i64 is commutes[b]:
    return a + b
```

The `is Law[args]` form in the return position becomes an `Ensures` slot in `SpecSignature`.
The discharge ladder attempts to prove it; for non-trivial shapes, the SMT tier (z3 via
SMT-LIB2 subprocess) is invoked automatically.  See docs/90 for SMT internals.

Ghost lemmas (proved `def` with `ensure` only, no observable output) can be used to establish
facts that the main proof depends on — **(planned: explicit ghost/lemma keyword; currently
modeled as zero-body `def` with `ensure` in tests)**.

### Level 5 — Typestate, effects, and frame conditions

Effects (`can`, `forbids`) and frame conditions (`changes`) attach behavioral constraints that
hold across function call boundaries, not just at entry/exit.

```elisa
def push(xs: mutable darray[i64], v: i64) can Mutate[xs]:
    changes xs
    xs.push(v)
```

`changes xs` is a **frame condition**: it records that `push` may modify `xs` and must not
affect any other in-scope mutable state.  Callers that require `xs` to be stable after a call
can verify it via the frame lattice.

See docs/87 (frame conditions) and docs/96 (typestate protocols) for the current implementation
status.  Cross-family effect subsumption is **(planned)**.

### Level 6 — Strict mode (unknown = error)

`-strict` / `EnforceStrictProofs` turns every `proofLint` into a hard compile-time error.
Nothing compiles unless every proof obligation is discharged — by the linear prover, by SMT, or
by an explicit `can Unsafe.AssumeProgress:` hatch (see docs/102).

```elisa
# compile with: elisa -strict -smt
def get(xs: darray[i64], i: i64 where 0 <= i and i < xs.count) -> i64:
    return xs[i]
```

In strict mode the compiler refuses to emit a runtime fallback.  This is the mode for
high-assurance modules; ordinary application code does not need it.

---

## 2. The same function across all six rungs

Below, `bounded_get` is written at increasing rigor.  Each version compiles; higher rungs add
obligations without changing the runtime representation or ABI.

```elisa
# Level 0 — no annotation; bounds-check at runtime under -fbounds-check
def bounded_get(xs: darray[i64], i: i64) -> i64:
    return xs[i]

# Level 1 — inline where; proof attempted at each call site
def bounded_get(xs: darray[i64], i: i64 where 0 <= i and i < xs.count) -> i64:
    return xs[i]

# Level 2 — named law + refinement type
law valid_index(self: i64, xs: darray[i64]) = 0 <= self and self < xs.count
def bounded_get(xs: darray[i64], i: i64 is valid_index[xs]) -> i64:
    return xs[i]

# Level 3 — explicit requires contract
def bounded_get(xs: darray[i64], i: i64) -> i64:
    requires 0 <= i and i < xs.count
    return xs[i]

# Level 4 — postcondition + result law
law elem_of(self: i64, xs: darray[i64], i: i64) = self == xs[i]
def bounded_get(xs: darray[i64], i: i64) -> i64:
    requires 0 <= i and i < xs.count
    ensure result is elem_of[xs, i]
    return xs[i]

# Level 5 — frame condition: no mutation of xs
def bounded_get(xs: darray[i64], i: i64) -> i64:
    requires 0 <= i and i < xs.count
    ensure result is elem_of[xs, i]
    changes nothing       # frame: xs is read-only
    return xs[i]

# Level 6 — compile with -strict; every rung above must be fully discharged
# (same source as Level 5; -strict is a compiler flag, not a syntax change)
```

---

## 3. Proof modes

### 3.1 Runtime checks (debug builds)

When a proof obligation cannot be statically discharged and `-strict` is not set, the compiler
emits a runtime assertion at the discharge point.  Under `-fbounds-check` this also covers
array accesses that escaped static analysis.  Cost: zero in release builds (`-O2` strips them);
non-zero in debug builds.

### 3.2 Static proof — linear prover (always on)

The first tier of the discharge ladder (`proveRequiresClause`) pattern-matches arithmetic shapes
— range comparisons, equalities, monotone bound propagation — against the current fact lattice.
It is O(1) per obligation, runs on every build, and requires no external process.

Facts are seeded by `seedParamWhereRefinementFacts` (from param `where` predicates) and
`seedWhereRefinementFact` (from local-variable `where` declarations).  Facts are invalidated
when any variable in their dependency set is mutated (docs/109 §3).

### 3.3 SMT tier (optional, `z3` subprocess)

When the linear prover returns `requiresUnknown`, `trySMTProveRequires` serializes the
obligation and the current fact set to SMT-LIB2 and calls `z3` as a subprocess.  Only `unsat`
concludes the obligation is proved; `sat` or `unknown` fall through to the runtime-check tier.

Enabled by `AnalyzeOptions.EnableSMT`; disabled with `-nosmt`.  Covers nonlinear arithmetic,
bit-masks, `forall`/`exists` quantifiers, and sign-extension reasoning (docs/90, docs/94).
Average cost: ~0.08 ms per obligation.

### 3.4 Strict mode (unknown = error)

`-strict` / `EnforceStrictProofs`: any obligation that reaches the runtime-fallback tier is a
**hard compile error**.  Use for security-critical, high-assurance, or formally-verified modules.
The escape hatch `can Unsafe.AssumeProgress:` allows a block-scoped override (docs/102).

### 3.5 Relationship between proof modes and the discharge ladder

```
Obligation arrives
       │
       ▼
 Fact lattice + linear prover  ──► proven   → no runtime check, no warning
       │ unknown
       ▼
   SMT tier (if -smt)          ──► proven   → no runtime check, no warning
       │ unknown / disabled
       ▼
  Runtime fallback              ──► proofLint warning + debug assertion
       │ if -strict
       └──────────────────────► hard compile error
```

---

## 4. The two ergonomic invariants

### 4.1 Runtime type identity never depends on the prover

`T where p`, `T is Law`, and any refinement form resolve to `T` for:

- `SameType` / `AssignableTo` — both directions return `true`.
- ABI layout and struct packing — computed from `T` only.
- Monomorphization keys — the predicate is stripped before keying a generic instance.
- Dispatch tables — based on `T`, not on any refinement.

Consequence: a plain `T` value may always be passed where `T where p` is expected.  The proof
obligation is discharged at the call site (or runtime-checked), but the program is never
**ill-typed** because of an unproven predicate.  Refinements are proof metadata; they are not
a second type-level hierarchy.

This invariant is enforced by `typeEraseWhere` in the type resolver, which strips `WhereRefinementTypeExpr` nodes before structural type checks.

### 4.2 Refinements erase for layout but survive in verification signatures

`SpecSignature` (docs/109 §2) is the canonical representation of a function's verification
contract.  It survives:

- Module export/import — cross-module callers see the `Requires`/`Ensures`/`ParamPredicates`
  and must discharge them.
- Interface summary generation — interfaces carry `SpecSignature` for every method.
- Generic specialization — each specialized copy inherits the predicate expressions (substituted
  into the concrete type) from the generic original.
- `where` refinements specifically: `SpecSignature` fields are populated even for anonymous
  `where` predicates, so they are not lost when the function is imported from another module.

The combination of invariants 4.1 and 4.2 means: **you always get runtime safety for free;
you pay for proof precision incrementally; your types are not stratified by what the prover knows**.

---

## 5. Aspirational / planned features

Features marked **(planned)** in this document are not yet implemented:

| Feature | Status |
|---|---|
| Explicit `ghost` / `lemma` keyword for zero-body proof helpers | planned |
| Cross-family effect subsumption (`includes` for value laws) | planned |
| Interprocedural frame condition enforcement | planned (docs/87 §4) |
| `preserves` annotation (read-frame dual of `changes`) | planned (docs/87 §4) |
| Set-polymorphic effect families | planned |

---

## 6. Quick-reference: which rung for what

| Situation | Recommended rung |
|---|---|
| Ordinary application code | Level 0 (+ `-fbounds-check` in debug) |
| Library function with a non-obvious precondition | Level 1 (`where`) or Level 3 (`requires`) |
| Named, reusable one-off predicate (binder-only) | Level 1a (`refine N = T where p`) |
| Struct field that must always satisfy an invariant | Level 1b (field `where`) |
| Shared predicate used at many call sites | Level 2 (named `law` + refinement type) |
| Stronger refinement implying a weaker one | Level 2 (subsumption: no annotation needed) |
| Public API with documented postcondition | Level 3 (`ensure`) |
| Algebraic property that must be machine-checkable | Level 4 (`is Law` postcondition) |
| Mutation-discipline / protocol conformance | Level 5 (effects + frame conditions) |
| Safety-critical / formally-verified module | Level 6 (`-strict`) |

---

## 7. References

- docs/109 — unified refinement pipeline (internals: SpecSignature, seeding, discharge, subsumption)
- docs/95 — surface spelling cheat-sheet (law / is / where / refine / requires / ensure / subsumption)
- docs/85 — contract algebra: discharge ladder detail, proof-hole semantics
- docs/90 — SMT discharge tier internals
- docs/94 — bit-level decode / mask reasoning
- docs/87 — frame conditions (`changes` / `preserves`)
- docs/96 — typestate protocols
- docs/102 — loop-termination / `can Unsafe.AssumeProgress:` hatch
