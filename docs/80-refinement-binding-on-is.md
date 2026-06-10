# 80 — Refinement binding unified on `is`

Status: design locked 2026-06-10; implementation staged (Phase A not yet landed).

## Goal

One refutable "test-and-maybe-bind" operator — `is` — for every refinement
form, replacing the parallel `if let` (optional unwrap) and
`if value in store as Pattern` (store refinement) spellings.

```
if v is value:                  # optional unwrap-bind   (was: if let value = v)
if e is Form.Circle(r: r):      # enum/variant pattern   (already works)
if value in store is Pattern:   # store lookup + pattern (was: ... in store as Pattern)
if value in store:              # bare store membership (boolean)
```

`is` already binds pattern variables (enum-payload patterns, `is … as`
aliasing), so this extends an existing operator rather than inventing one.

## The disambiguation rule (the linchpin)

On the right-hand side of `is`, a target is resolved by the analyzer (which
has type information):

- **Resolves to a known type** (including the lowercase builtins `i64`, `bool`,
  `cstr`, `usize`, …), an enum/tree variant, a struct pattern, or a named
  state → it is a **test** (current behavior, unchanged).
- **A bare, unqualified, lowercase identifier that does NOT resolve to a type**
  → it is a **binding**: it binds the refined value into the branch body.

This leans on Elisa's conventions — user types are Capitalized, variants are
qualified (`Form.Circle`) — so a bare lowercase unknown name is unambiguously a
fresh binding. A Capitalized unknown name stays an error ("unknown type"). This
is the amendment to the bare-"lowercase = binding" rule: lowercase *builtin
types* remain tests; only lowercase names that don't resolve become bindings.
The guidance when an intended type is shadowed by this rule is the same as the
old tree-relation message: qualify the type.

## Refutability

`x is name` is only meaningful when `x` is refutable:

- optional `T?` → binds the unwrapped payload when present (None ⇒ no match);
- enum / tree-category / error-union → binds when the variant matches;
- `value in store` entry → binds when the handle resolves to a present entry.

On a non-refutable value (a plain `i64`), `if x is name:` always matches; that
must be a **compile error** ("irrefutable bind; use `name := x`"), which removes
the "did I mean to test or bind?" footgun.

## Precedence and `in`

`in` is already a membership operator (`x in {a, b}`, charsets). For stores it
is a lookup that yields a refinable entry.

- `value in store is Pattern` parses as `(value in store) is Pattern` — `in`
  (lookup) binds tighter than `is` (refine).
- `if value in store:` alone is a boolean presence test.

## `as` aliasing kept

`as`'s cast meaning is already removed. Inside `is`, `as` still aliases the
whole matched value: `e is Form.Circle(..) as whole` binds `whole` to the entire
matched value while destructuring. This is orthogonal to the bare-ident binding
above and stays.

## Implementation phases

- **Phase A — `is` optional unwrap-bind.** Parser: accept a bare unqualified
  lowercase ident as an `is` target (today it routes to
  `parseTypeExprWithoutErrorUnionSuffix`). Analyzer (`analyzeIsExpr` +
  guard-binding scope, `guardConditionIntroducesBindings`): when the target is a
  bare name that does not resolve to a type and the LHS is optional, register a
  binding of the unwrapped payload, scoped into the branch body. Backend: lower
  "present? → extract payload → bind", reusing the existing is-payload binding
  lowering. Both `if let` and `is` valid during this phase.
- **Phase B — `value in store is Pattern`.** Make the `in store` lookup compose
  with a trailing `is Pattern` (mirror of the existing `… in store as Pattern`
  path in `parseIfClause`), producing the same store-refinement MatchStmt the
  `as` form did.
- **Phase C — refutability guard.** Reject irrefutable `x is name` binds.
- **Phase D — migration + retirement.** Mechanically rewrite the 344 `if let`
  sites (`if let X = Y:` → `if Y is X:`; `and`-guards keep their tail;
  multi-binding `if let a = x, b = y:` → `if x is a and y is b:`) and the 14
  `if … in store as Pattern` sites, then hard-reject `if let` and the store `as`
  form. The JSON-parser self-host programs are covered by checksum tests — they
  must stay byte-identical.

## Notes

- `if let` is heavily used (~344 in-tree `.elisa` sites); the store `as` form has
  ~14 (all in the JSON-parser self-host programs) plus ~25 Go fixtures.
- The `is` / match / guard subsystem is the most intricate part of the analyzer;
  Phase A is the load-bearing one and should land with full-suite green before
  the migration (per the view-unification lesson: semantic-subtlety removals that
  "compile but misbehave" must not be rushed).
