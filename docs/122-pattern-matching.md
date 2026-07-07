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

### 2.2 Guards live in the body

There is **no arm-header guard clause** today. A value-dependent condition is a plain `if`
(or `if … is …` refinement bind) as the arm's first statement:

```
match node:
    Expr.Binary(left, op, right):
        if left is Expr.Literal(v) and is_commutative(op):
            reassociate(v, op, right)
```

The R1 flow lint (docs/121) accommodates this: `match` is transparent to the nesting
counter and a single trailing guard `if` in an arm is carved out for free.

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

A `_` wildcard discharges the exhaustiveness obligation everywhere.

---

## 5. Wanted — not yet supported

Ordered roughly by how often the absence bites while dogfooding. Each is a candidate for
its own implementation pass; none are implemented today.

### 5.1 Arm-header guards
Rust-style `Pattern if cond:` (or a `where` clause) directly on the arm, so the guard reads
as part of the dispatch instead of a nested body `if`:
```
match node:
    Expr.Binary(l, op, r) if is_commutative(op):   # WANTED
        …
```
Design tension: this partially re-introduces the body-`if` nesting the flow lint steers
away from, but as an *arm header* it stays one level and reads as dispatch. Exhaustiveness
must treat a guarded arm as *non*-covering (a guard can fail), matching Rust.

### 5.2 Range patterns
Numeric / char ranges as a pattern:
```
match c:
    '0'..'9':   digit()      # WANTED
    'a'..'z':   lower()
    0..=127:    ascii()
```
Needs a `MatchRangePattern` node, range exhaustiveness for bounded integer/char domains,
and a decision on inclusive vs half-open spelling (lean `..<` / `..=` to match the loop
range syntax).

### 5.3 Binding the list rest
`MatchRestPattern` matches but discards. We want to **bind** the tail:
```
match tokens:
    [first, ...rest]:   process(first, rest)   # WANTED (rest currently unnamed)
```

### 5.4 `@` / as-bindings (bind whole *and* destructure)
Bind the entire matched value at a narrowed type while also destructuring it. The category
binder `SubCategory s:` does this only at the **top-level arm**; we want it nested:
```
match node:
    Expr.Binary(l, op, r) as whole:   # WANTED — `whole` bound to the Binary, plus its parts
        rewrite(whole, l, op, r)
```

### 5.5 Or-patterns that bind
`A | B` works for value patterns, but alternation that **binds a common variable** across
arms (each alternative must bind the same names at compatible types) is not supported:
```
match tok:
    Token.Ident(name) | Token.Keyword(name):   # WANTED — `name` unified across alternatives
        use(name)
```

### 5.6 Deep exhaustiveness
Exhaustiveness today reasons about **top-level** variant coverage. Nested/literal
exhaustiveness — proving that a set of nested patterns covers all constructor combinations,
or that literal arms cover a bounded domain — is not checked; such matches need a `_`.

### 5.7 Struct rest (`_`) in struct/variant patterns
Match a struct while ignoring unmentioned fields explicitly:
```
match ev:
    KeyEvent(key: k, _):   # WANTED — ignore the rest of the fields
        …
```

### 5.8 Guards with bindings in the refinement position
`guardConditionIntroducesBindings` currently *rejects* bindings inside the standalone
`guard … else` condition, steering to `if … is name:`. If arm-header guards (§5.1) land,
revisit whether a guard may itself introduce an `is`-binding usable by the arm body.

---

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
