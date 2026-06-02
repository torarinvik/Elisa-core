# 64 — Error-set polymorphism (Phase 5b) — design note

**Status:** decided (implement next, as its own focused pass). Grounded in probes run
2026-06-02 against the live analyzer; see "Evidence" below for the exact `.elisa` snippets and
their diagnostics.

## Question

Does a generic *error-set* parameter — `def map[T, U, errorset R](f: func(T) -> U error[R]) -> ... error[R]`
— earn its complexity, given that concrete error sets already flow through `func`-type
params? This is the symmetric mirror of the permission generic param (`[permission E]`),
which already works end-to-end (Phase 5 measurement, docs/63 §0).

## Decision

**Yes — implement it, but as its own focused pass, not tacked onto a long session.** It fills
a genuine, otherwise-impossible expressive gap, and most of the supporting machinery already
exists; the single new and delicate piece (the error-set unifier hole) is exactly the kind of
type-inference change the docs/63 risk note says not to rush.

## Why it's wanted (the gap is real)

Error-propagating higher-order combinators are **monomorphic in their error set today**.
There is no workaround:

- A concrete-error combinator rejects a callback raising a *different* set. `applies(f: func() -> i64 error[IoErr])`
  fed a `func() -> i64 error[NetErr]` → `argument 1 to "applies" expects func() -> i64 | IoErr, got func() -> i64 | NetErr`.
- `error[...]` is **not** an error-side ⊤: it requires at least one tag
  (`error[...] requires at least one qualified error tag`). There is no error analogue of the
  Phase-5a permission `any`.
- The open-suffix form `error[IoErr, ...]` is **not** a polymorphism escape either — it still
  rejects a `NetErr` callback (same diagnostic as the concrete case). `...` means "this set,
  open to more from context", not "any caller's set".

So combinators like `map` / `andThen` / `retry` over a fallible callback cannot be written
once and reused across error types. The permission side *can* do this
(`[permission E]` + `can[E]`); the error side asymmetrically cannot. Closing that asymmetry
is the whole point of Phase 5.

## Why it's tractable (most machinery already exists)

- **Generic-param plumbing** is a proven mirror: `GenericParamPermission` →
  `permissionParamScopes` / `withPermissionParams` / `lookupPermissionParam`, parsed via the
  `permission` keyword in `parseGenericParamListAfterLBracket`. Phase 5b adds a parallel
  `GenericParamErrorSet` kind, an `errorset` keyword, `errorSetParamScopes`, and
  `withErrorSetParams` / `lookupErrorSetParam`.
- **Type-param inference through `func`-type params already works precisely**:
  `applies[T](f: func() -> T) -> T` infers `T=i64` from `applies(makeInt)`, and rejects a
  `bool` expectation (`argument 1 to "applies" expects func() -> bool, got func() -> i64`).
  The call-site binding/return-propagation path that `R` needs is the same one `T` already
  rides (`collectSpecializationBindings` / the func-type assignability check).
- **No backend work.** Effects/error-set generics erase; error unions already reach codegen
  as values via monomorphization (docs/63 §5). Phase 5b is parser + semantic only.

## The one genuinely new piece (the careful part)

Today the type unifier compares error sets by **exact set equality** (that's why Probe B/D
reject mismatched sets). Phase 5b requires teaching it to treat an error-set param `R` as a
**binding hole**: when matching `error[R]` (callee) against `error[NetErr]` (argument),
bind `R := {NetErr}` instead of demanding equality, then substitute `R` into the return type
(`U error[R]` → `U error[NetErr]`). This is new unifier behaviour on `ErrorSetType`, and it is
precisely the "unsound subsumption / inference loops" zone the docs/63 risk note flags. It
must be done with the error-union unification path mapped first, and with adversarial tests
(over-binding, double-bind conflict, R appearing in multiple params, R unbound).

## Implementation order (next session)

1. **Map** the error-union unification path: where `ErrorSetType` equality is enforced during
   func-type assignability and `collectSpecializationBindings`. (Investigation half.)
2. **AST + parser**: `GenericParamErrorSet`, `errorset` keyword (mirror the `permission`
   branch). Bounded, no risk.
3. **Scopes**: `errorSetParamScopes` + `withErrorSetParams` / `lookupErrorSetParam`; resolve
   `error[R]` where `R` is a param to a polymorphic placeholder set.
4. **Unifier hole**: bind `R` from a concrete set at the call site; substitute into the return
   type. The delicate step — land behind adversarial tests.
5. **Subsumption**: `R` propagates covariantly (the callee may raise ⊆ what the caller
   declares), reusing the shared set lattice already built for `includes` (Phase 3b).
6. **Tests**: positive (`map`/`andThen` reused across IoErr and NetErr), negative
   (unbound R, conflicting binds, member misuse), and a blast-radius pass over the emulator
   sources.

## Evidence (probes, 2026-06-02)

| Probe | Snippet | Result |
|---|---|---|
| concrete combinator | `applies(f: func()->i64 error[IoErr])` fed `error[NetErr]` | rejected (exact-set mismatch) |
| `error[...]` as top | `func()->i64 error[...]` | `error[...] requires at least one qualified error tag` |
| open suffix workaround | `func()->i64 error[IoErr, ...]` fed `error[NetErr]` | rejected (no polymorphism) |
| type param via func-param | `applies[T](f: func()->T)->T`, `applies(makeInt)` | infers `T=i64`, clean |
| type param precision | same, expected `bool` | rejected — `expects func()->bool, got func()->i64` |

Conclusion: the inference scaffolding `R` needs already exists for `T`; only the
`ErrorSetType` equality→binding-hole change is new. 5b is worth it and bounded, but its one
delicate step belongs in a focused pass with the unifier mapped first.
