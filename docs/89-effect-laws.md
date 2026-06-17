# 89 — Effect & shape laws (docs/85 §4, §8, Stage 4)

Status: **design + bricks 1–2.** Implements the **effect** discharge class (brick 1) and the
first **shape** law (brick 2) from docs/85 §4 — the *function-level* law classes (value laws are
about a value, frame laws about a place; effect/shape laws are about the whole function). Effect
laws build on the effect-grant / permission system (`@hot` is judged against the same set); shape
laws build on the bounds-proof analysis.

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

## 6. What bricks 1–2 cover / defer

**Covered:** effect-law decls (`forbids`, parse + shape validation); subject-free `fulfills
<Law>` parsed and discharged against the inferred effect set; the built-in `NoBoundsChecks` shape
law discharged against the bounds-proof analysis; the wrong-class / wrong-form diagnostics for
both. Also fixed a latent parser gap: `-> RetType changes/preserves/fulfills` mis-parsed the
return type as a legacy region prefix (the disambiguation list only knew `can`/`ensures`).

**Deferred:**
- **more shape laws** (`BranchFree`, `NoRealloc`, `NoAlloc`-as-codegen) — same `isBuiltinShapeLaw`
  registry + a per-law body analysis; the registry and discharge dispatch are shaped to grow.
- **user-defined shape/effect laws via `includes`** (docs/85 §6 algebra) — composing built-in
  shape predicates into named laws.
- **measure laws** (Stage 5).
- **`includes` composition** of effect laws into larger laws (docs/85 §6 algebra).
- **required-effect laws** (`requires` an effect) — only `forbids` exists now.
