# 89 — Effect laws (docs/85 §4, Stage 4)

Status: **design + brick 1.** Implements the **effect** discharge class from docs/85 §4 — the
first *function-level* law class (value laws are about a value, frame laws about a place; effect
laws are about the whole function's behaviour). Builds directly on the existing effect-grant /
permission system (`@hot` is judged against the same set).

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

## 5. What brick 1 covers / defers

**Covered:** effect-law decls (`forbids`, parse + shape validation); subject-free `fulfills
<Law>` parsed and discharged against the inferred effect set; the wrong-class / wrong-form
diagnostics. Also fixed a latent parser gap: `-> RetType changes/preserves/fulfills` mis-parsed
the return type as a legacy region prefix (the disambiguation list only knew `can`/`ensures`).

**Deferred:**
- **shape laws** (`NoAlloc` as a *local-codegen* property, `NoBoundsChecks`, `BranchFree`) —
  the remainder of Stage 4, discharged by codegen analyses rather than the effect set.
- **measure laws** (Stage 5).
- **`includes` composition** of effect laws into larger laws (docs/85 §6 algebra).
- **required-effect laws** (`requires` an effect) — only `forbids` exists now.
