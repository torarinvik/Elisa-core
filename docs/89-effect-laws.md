# 89 — Function-level laws: effect, shape, composite, measure (docs/85 §4, §6, §8, Stages 4–5)

Status: **design + bricks 1–4.** Implements the **function-level** law classes from docs/85 —
the ones about the whole function rather than a value (value laws) or a place (frame laws):
**effect** (brick 1, §4), **shape** (brick 2, §8), **composite** `includes` (brick 3, §6), and
**measure** (brick 4, §8, Stage 5). Effect laws build on the effect-grant / permission system
(`@hot` is judged against the same set); shape laws on the bounds-proof analysis; composite laws
union the others; measure laws are verify-only, riding the existing post-codegen autovec verifier.

## 1. The effect discharge class

docs/85 §4 lists five discharge classes; this brick adds **effect**:

| class | example | discharged by |
|---|---|---|
| value | `Positive`, `Bounded[..]` | fact lattice |
| frame | `changes`, `preserves` | mutation/alias analysis |
| **effect** | **`forbid Memory.Allocate`** | **effect-grant system** |
| shape | `NoAlloc`, `NoBoundsChecks` | local codegen analyses (Stage 4 cont.) |
| measure | `Vectorizes` | debug measure / IR verify (Stage 5) |

The class is derived from the law's body shape (no new keyword), exactly as for frame laws:

- **value law** — `law Positive(self: i64) = self > 0` (an `= <bool-expr>` body).
- **frame law** — `law MovesPlayerOnly(self: Render&) changes self.px` (a `changes`/`preserves`
  clause).
- **effect law** — `law NoAlloc forbids Memory.Allocate, Abort.Panic` (a `forbids` clause). No
  subject parameter — it constrains the whole function, not a value or place.

## 2. Effect laws

```elisa
law NoAlloc forbids Memory.Allocate
law Leaf forbids Memory.Allocate, Memory.Release, Abort.Panic
```

`forbids` takes a comma-separated list of effect refs (the same `Family.Member` notation as
`can`, plus the `Family{M1, M2}` brace sugar and bare-`Family` forms). A bare family
(`forbids Memory`) bars every member; `Memory.Allocate` bars only that effect. An effect law has
no parameter list, no `=` body, and no `changes` clause (mixing is a malformed-law error).

## 3. `fulfills` — applying an effect law to a function

A function declares conformance with the **subject-free** `fulfills <Law>`:

```elisa
def pure_add(x: i64, y: i64) -> i64 fulfills NoAlloc:
    return x + y                 # ok — uses no forbidden effect

def grow() -> i64 fulfills NoAlloc:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.push(7)               # ERROR: uses the `Memory.Allocate` effect, which NoAlloc forbids
        return xs[0]
```

This reuses the `fulfills` keyword from docs/88. The form is told apart by whether `is` follows:
`fulfills r is FrameLaw` (frame, subject-bound) vs `fulfills NoAlloc` (effect, function-level).
Applying an effect law with a subject (`fulfills x is NoAlloc`) is a wrong-form error; a value
`is` of an effect law is a wrong-class error.

## 4. Discharge — against the inferred effect set

The obligation is discharged **after** the function's effect set is finalized
(`fnType.PermissionRefs` = declared ∪ transitively-inferred), in `checkEffectFulfills`. A
conforming function violates the law iff its effect set contains an effect the law forbids.

**Soundness:** `PermissionRefs` is the transitive set the whole effect system — and `@hot` —
already trusts. An effect the function actually uses is in that set, so a real violation is
always caught; over-reporting (an effect appears but is dead) is only a safe false positive. The
check is therefore sound by construction, with no new analysis.

## 5. Shape laws — `NoBoundsChecks` (brick 2)

A **shape law** (docs/85 §8) is a *local, statically-decidable codegen property*. Unlike value /
frame / effect laws it is not a user `law` decl — it is a built-in name a function `fulfills`,
discharged by a dedicated codegen analysis. The first is `NoBoundsChecks`:

```elisa
def sum(xs: darray[i64]) -> i64 fulfills NoBoundsChecks:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i]     # proven in-bounds → no runtime check
    return total

def third(xs: darray[i64]) -> i64 fulfills NoBoundsChecks:
    return xs[2]                    # ERROR: not proven, a runtime bounds check would be emitted
```

**Discharge:** during body analysis every index access that would emit a runtime bounds check
(bounds-requiring type, not statically proven in-bounds — the existing
`indexExprRequiresUncheckedIndexPermission` predicate) is collected into
`currentFunctionGuardedIndexes`. After the body is analyzed, a function `fulfills NoBoundsChecks`
is violated iff that list is non-empty.

**Soundness / honesty:** the law certifies *provably* no checks. A `trusted` unchecked access
emits no runtime check but is *asserted*, not proven, so it is still counted as a violation — the
conservative reading (over-reporting is a safe false positive). This keeps the shape law a
genuine static guarantee, not a restatement of "the backend happened to elide it."

Like effect laws, a shape law is function-level: applied with the subject-free `fulfills
NoBoundsChecks` (a subject is a wrong-form error), and a value `is NoBoundsChecks` is a
wrong-class error.

## 6. Composite laws — `includes` (brick 3)

The docs/85 §6 composition algebra, restricted to the function-level classes. A **composite law**
unions its members' obligations under one name:

```elisa
law NoAlloc forbids Memory.Allocate
law NoPanic forbids Abort.Panic
law HotKernel includes NoAlloc, NoPanic, NoBoundsChecks

def kernel(xs: darray[i64]) -> i64 fulfills HotKernel:   # one name, three obligations
    ...
```

`fulfills HotKernel` discharges the **union**: the forbidden-effect set
(`{Memory.Allocate, Abort.Panic}`, resolved transitively through nested `includes`) checked
against the function's effect set, **and** every required built-in shape (`NoBoundsChecks`)
audited. Members may be effect laws, built-in shape laws, or other composites.

`checkFunctionLevelFulfills` is the single discharge pass for all subject-free `fulfills`: it
computes a law's effective forbid-set (`lawEffectiveForbids`) and shape set
(`lawEffectiveShapes`) — both transitive, both cycle-safe — so effect, shape, and composite laws
flow through one uniform check. Validation (at the composite's decl): no body / no subject; every
member resolves to a function-level law (a value or frame member is rejected — `includes` composes
only function-level classes); the include graph is acyclic.

## 7. Measure laws — `Vectorizes` (brick 4, Stage 5)

A **measure law** (docs/85 §8) is an *emergent, codegen-dependent* property — vectorization,
inlining, a cycle budget. It is the deliberately weak class: **never proved, never build-gating,
never a composable premise** (§8 hard rule). It is *verified post-hoc* and surfaced as a warning.
Like shape laws it is a built-in name, not a user `law` decl. The first is `Vectorizes`:

```elisa
def scale(xs: darray[i64]) -> i64 fulfills Vectorizes:
    total: mutable i64 = 0
    for i in 0..<xs.count:
        total <- total + xs[i] * 2     # if this loop fails to vectorize → -Wperf warning
    return total
```

**Discharge:** `fulfills Vectorizes` carries *no static obligation*. Instead the analyzer tags the
function's range loops `AutovecExpected` (in `analyzeForStmt`, gated on
`currentFunctionExpectsVectorize`), opting them into the **existing** post-optimization autovec
verifier (`verifyAutovecExpectations`): a marked loop that lacks `llvm.loop.isvectorized` after the
pipeline produces a `-Wperf` warning. The tag changes no codegen — it is purely a measurement
hook. A failure is *always* a warning, never a compile error — that is what makes it measure-class.

**The §8 hard rule is enforced:** a measure law cannot be `includes`d into another law (a class
error, not merely unsupported), so a measure property can never masquerade as a provable premise
that something else relies on transitively. Subject form and value `is` are wrong-form /
wrong-class errors, as for the other function-level classes.

## 8. What bricks 1–4 cover / defer

**Covered:** effect-law decls (`forbids`); the built-in `NoBoundsChecks` shape law; composite
laws (`includes`) unioning effect + shape obligations transitively + cycle-safe; the `Vectorizes`
measure law (verify-only via the autovec verifier, non-composable); subject-free `fulfills <Law>`
through one uniform discharge pass; the wrong-class / wrong-form / non-composable diagnostics for
all four. Also fixed a latent parser gap: `-> RetType changes/preserves/fulfills` mis-parsed the
return type as a legacy region prefix (the disambiguation list only knew `can`/`ensures`).

The discharge-class ladder (docs/85 §4) is now complete: **value · frame · effect · shape ·
measure**, plus **composite** composition over the function-level classes.

**Observability (brick 5):** every function-level `fulfills` discharge is recorded in the
`--explain` proof report (docs/85 §10) — a clean effect/shape/composite law as `proven (contract)`,
a measure law as `measured (-Wperf)`, a violation as `refuted`. The report summary now counts a
`measured` bucket alongside proven/runtime/refuted, and (a latent fix) folds `proven (linear)` into
the proven count. So the "always known to the user" principle now covers all six classes, not just
value refinements.

**Deferred:**
- **more built-in shape laws** (`BranchFree`, `NoRealloc`, `NoAlloc`-as-codegen) — same
  `isBuiltinShapeLaw` registry + a per-law body analysis in `dischargeShapeRequirement`.
- **more measure laws** (`Inlined`, cycle/alloc budgets) — same `isBuiltinMeasureLaw` registry +
  a post-codegen verifier hook.
- **required-effect laws** (`requires` an effect) — only `forbids` exists now.
