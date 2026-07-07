# docs/122 — Pattern Matching

The reference for Elisa's `match` and pattern surface: what is supported today, the
canonical syntax, and the features we *want* that are not yet implemented. This is a
living document — when a "Wanted" item lands, move it up to "Supported" with its example.

Pattern matching is one of the load-bearing surfaces of the language: it is how the
error-union model, the enum/tree hierarchy, and the flow-checked-loop lints
([docs/121](121-flow-checked-loops.md)) all want you to express control flow. The design
bias is **push discrimination into the pattern, keep the body flat** — a deeper pattern
costs nothing, whereas the equivalent stacked `if`s in the body accrue R1 nesting budget.

---

## 1. Canonical form

```
match scrutinee:
    Pattern1:
        body
    Pattern2:
        body
    _:
        fallback
```

- Arms are `Pattern:` followed by an indented block. **No `case` keyword** — a stray
  `case` is a hard error (`match arms do not use a \`case\` keyword`).
- `match` is also an **expression** (docs/119 §4): the tail form of each arm yields the
  arm's value, so `x = match e: …` and nested match-expressions compose.
- Exhaustiveness is **checked** (see §4). A non-exhaustive match is a compile error, not a
  warning.

---

## 2. Supported patterns

Every pattern node the parser + AST implement today. Arg positions are **recursive** —
anywhere a sub-pattern appears it may be any pattern below (this is what makes nested
destructuring work).

| Pattern | Syntax | AST node |
|---|---|---|
| Wildcard | `_:` | `MatchWildcardPattern` |
| Binding | `name:` (binds the scrutinee) | `MatchBindPattern` |
| Category binder | `SubCategory s:` (docs/77 §2, top-level arm only) | `MatchBindPattern.Binder` |
| Bool / null / char / int / float / hex literal | `true:` `null:` `'a':` `42:` `0xFF:` `-1:` | `MatchLiteralPattern` |
| Qualified value literal | `Color.Red:` `Mode.Idle:` | `MatchLiteralPattern` |
| Pinned value | `^expr:` (match against a *variable's* current value) | `MatchLiteralPattern{Pinned}` |
| String literal | `"keyword":` | `MatchStringLiteralPattern` |
| Variant | `Enum.Variant(a, b):` | `MatchVariantPattern` |
| Module-qualified variant | `M::Color.Red:` `M::Expr.Binary(...):` | `MatchVariantPattern` |
| Struct (positional) | `Type(a, b):` | `MatchStructPattern` |
| Struct (named fields) | `Type(field: pat, other: pat):` | `MatchStructPattern` |
| Struct (brace) | `Type{field: pat}:` | `MatchStructPattern{Brace}` |
| `count(...)` refinement | `count(n):` | `MatchStructPattern` (TypeName `count`) |
| Tuple | `a, b, c:` (top-level comma) | `MatchTuplePattern` |
| List | `[a, b, c]:` | `MatchListPattern` |
| List rest | `[first, second, ...]:` (final element only) | `MatchRestPattern` |
| Bound list rest | `[first, ...rest]:` (binds the tail as a read-only view) | `MatchRestPattern{Name}` |
| Range | `'a'..<'z':` `0..=127:` (int/char; `..<` exclusive, `..=` inclusive) | `MatchRangePattern` |
| As-binding | `Enum.V(a, b) as whole:` / `Type{f: p} as whole:` (nested too) | `MatchVariantPattern.As` / `MatchStructPattern.As` |
| Struct/variant rest | `Type(field: k, _):` (named args + final `_` ignores the rest) | `.Rest` on struct/variant patterns |
| Or / alternation | `A | B | C:` | `MatchOrPattern` |
| Named arg + nested pattern | `Type(field: Nested(x)):` | `MatchPatternArg{Name, Pattern}` |

### 2.1 Nested destructuring

Args are parsed recursively (`parseMatchPatternArg → parseNestedOrMatchPattern`), so a
variant/struct arg may itself be any pattern:

```
match node:
    Expr.Binary(Expr.Literal(a), op, Expr.Literal(b)):   # both operands constant
        fold(a, op, b)
    Expr.Binary(Expr.Literal(v), .Add | .Sub, right):    # one-sided, additive ops only
        partial_fold(v, right)
    _:
        node
```

### 2.2 Arm-header guards

`Pattern if cond:` guards the arm as part of its dispatch; pattern bindings are in
scope in the guard, and a failed guard falls through to the NEXT arm's test:

```
match node:
    Expr.Binary(l, op, r) if is_commutative(op):
        reassociate(l, op, r)
    Expr.Binary(l, op, r):
        keep(l, op, r)
```

Rules:
- A guarded arm **never discharges exhaustiveness** (the guard can fail) — a match whose
  only catch-all is guarded is a compile error.
- A guarded arm never shadows later arms (no false "unreachable arm").
- Fanned-out `A | B if cond:` alternatives share the guard.
- Guards are read-only dispatch and may not introduce `is`-bindings (§5.8): bind in the
  body with `if value is name:` instead. The body-`if` form below remains available and
  the R1 flow lint still carves out a single trailing guard `if` for free:

```
match node:
    Expr.Binary(left, op, right):
        if left is Expr.Literal(v) and is_commutative(op):
            reassociate(v, op, right)
```

### 2.3 Refinement binding outside `match`

`if EXPR is PATTERN:` (docs/80) is the one-armed form — refine-and-bind without a full
`match`. It shares the pattern grammar above:

```
if node is Expr.Binary(l, op, r):
    …
```

---

## 3. Where patterns are accepted

- `match` statements and match-expressions.
- `if … is PATTERN:` / `value in store is PATTERN:` refinement binds.
- `expect let PATTERN = …` and `catch` arms (error-set patterns; `error e:` catch-all).
- `for PATTERN in …` destructuring (tuple/list binding positions).

---

## 4. Exhaustiveness & diagnostics

Checked today:

- **Enum / tree hierarchy** — missing variants are named:
  `non-exhaustive match over "Expr"; missing variants: Unary, Call`.
- **String match expressions** — require a final `_` arm.
- **Integer match expressions** — open scalar; require `_` (docs/119).
- **`catch` over an error set** — missing errors named, or requires `error e:` catch-all.

An **unguarded** `_` wildcard discharges the exhaustiveness obligation everywhere; a
guarded arm never does (§2.2).

---

## 5. Wanted — not yet supported

Landed 2026-07-07 (moved up to §2 with examples): arm-header guards (§5.1), range
patterns (§5.2, spelled `..<`/`..=`; a bare `..` is diagnosed toward the explicit
forms), bound list rest (§5.3, binds a read-only view over the tail; darray/view
sources), as-bindings (§5.4, variant + struct patterns, nested positions included),
struct/variant rest `_` (§5.7, named args + explicit final `_`). Binding or-patterns
(§5.5) turned out to already work — top-level alternation fans out into arms sharing a
body, and nested `MatchOrPattern` alternatives must bind the same names at compatible
types — so it was documentation debt, now locked in by regression tests.

Still wanted:

### 5.1 Deep exhaustiveness (was §5.6)
Exhaustiveness today reasons about **top-level** variant coverage. Nested/literal
exhaustiveness — proving that a set of nested patterns covers all constructor
combinations, or that literal/range arms cover a bounded domain (a `u8` fully tiled by
ranges still needs `_`) — is not checked. This is its own analysis pass (constructor
matrices à la Maranget); deliberately deferred rather than half-shipped: matches keep
needing a `_` until coverage is *proved*, which is the sound default.

### 5.2 Guards that bind (was §5.8)
Arm guards are read-only dispatch: `Pattern if value is name:` is refused (the parser
diagnoses it). Allowing an `is`-binding usable by the arm body needs binding-scope
plumbing through every match emitter's guard path — revisit if dogfooding keeps asking
for it; the body `if … is name:` form covers the need today.

### 5.3 Positional struct/variant rest
`_`-rest applies to named-args patterns only (`Tok.Num(value: v, _)`); in pure
positional patterns a trailing `_` keeps its established one-field-wildcard meaning.
A positional rest would need a different spelling (`..`?) — not worth the ambiguity yet.

### 5.4 Rest binding over strings and fixed arrays
`[x, ...rest]` binds for darray/view sources. String-likes (dstr/cstr) and fixed
arrays are rejected with a clear diagnostic for now (fixed arrays would need an
addressable-base view; strings a char-view decision).

## 6. Non-goals

- **`case` keyword** — deliberately rejected; arms are bare `Pattern:`.
- **Fallthrough** between arms (C `switch` semantics) — arms are isolated blocks.
- **Guards that mutate the scrutinee** — patterns and guards are read-only dispatch.

---

## 7. Cross-references

- [docs/80](80-refinement-binding-on-is.md) — `is`-binding, the one-armed refinement form.
- [docs/77](77-enum-hierarchies-and-sealed-refinement.md) — category arms / the `SubCategory s:` binder.
- [docs/119](119-expression-unification-and-explicit-mutation.md) — match-as-expression.
- [docs/121](121-flow-checked-loops.md) — why deep patterns are free and body `if`-trees are not.
