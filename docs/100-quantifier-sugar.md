# docs/100 — Quantifier concept-sugar

Goal: give contracts and laws a **concept-level vocabulary** for talking about collections, without
adding any new verification machinery. Every form here is **pure desugaring** in the parser: it lowers
onto the existing `QuantifierExpr` canonical form, so the analyzer, the affine/linear tiers, the SMT
array-store model (docs/90), and the backend are all unchanged.

## Baseline that already exists

Quantifiers work in law/spec bodies (docs/90 brick 90-4). The canonical, provable form is a guarded
implication:

```elisa
forall i: (0 <= i and i < n) implies P
```

`implies a` is itself sugar for `(not a) or b` (parser, lowest precedence). The SMT tier already models
an array/darray subject as an SMT `(Array Int Int)`, with `arr[i]` → `(select arr i)` and `arr.count` /
`arr.len` → a per-array length `Int` asserted `>= 0`. So once a concept predicate desugars to the
canonical guarded `forall` over `select`/`count`, it is provable with **no analyzer change**.

The gap this doc closes is the **readable surface**: the guarded-implication boilerplate, and the lack
of an index-range binder.

## Form 1 — range binder `forall i in <range>: P`  *(IMPLEMENTED)*

```elisa
forall i in 0 ..< n:        P        # explicit half-open range
forall i in a.indices:      P        # index sugar (see Form 2)
```

Desugars (parser, single binder only) to the canonical guarded form:

```elisa
forall i: (not (lo <= i and i < hi)) or P
```

i.e. `(lo <= i and i < hi) implies P`. `exists i in <range>: P` is accepted symmetrically (it produces
`exists i: (guard) or P`; note the natural reading of bounded-exists wants `and`, so prefer the explicit
`exists i: (guard) and P` until a dedicated bounded-exists lowering lands — tracked as a follow-up).

Implementation: `parseQuantifier` / `parseQuantifierRange` in
`compiler/src/parser/parser_expr_parsebuiltintypeexpr_to_parsecatchexpr.go`. No new AST node — the
result is an ordinary `ast.QuantifierExpr` whose `Body` is the guarded boolean.

## Form 2 — index binder `a.indices`  *(IMPLEMENTED)*

`a.indices` as a range source is sugar for `0 ..< a.count`:

```elisa
forall i in a.indices: a[i] >= 0
# ≡ forall i: (0 <= i and i < a.count) implies a[i] >= 0
```

This reuses the SMT length symbol for `a.count`, so the range and any `a[i]` read share the same array
symbol. Recognised syntactically in `parseQuantifierRange` (a `FieldExpr` with `Field == "indices"`).

## Form 3 — concept predicates  *(`sorted` IMPLEMENTED as a library law; `no duplicates` planned)*

These are ordinary **library laws** whose bodies are written with Forms 1–2 — the honest "library
predicate desugars onto existing quantifiers" design (mirrors fold-comprehensions being a pure parser
desugar). No keyword is reserved; they are just laws a program (or the prelude) can declare:

```elisa
# sorted: every adjacent pair is ordered
law Sorted(self: darray[i64], n: i64) = forall i in 0 ..< n - 1: self[i] <= self[i + 1]

# no duplicates: distinct indices hold distinct values
law NoDuplicates(self: darray[i64], n: i64) =
    forall i, j: (0 <= i and i < n and 0 <= j and j < n and i != j)
        implies self[i] != self[j]
```

Note on subjects: the SMT array-store model (and the analyzer's law-body type-check) currently backs
**`darray[i64]`** subjects, modeled as `(Array Int Int)` with an explicit length param `n`. Indexing /
`.count` on a fixed-size `array[i64, N]` law subject is not yet modeled by the analyzer (it rejects
`self.count` on `array`) — so library predicates take `darray[i64]` + `n` today. Lifting `array[T,N]`
and `a.indices`-on-darray into the analyzer's array model is a follow-up. The `a.indices` parser
desugar (Form 2) is in place and tested at the AST level regardless.

`sorted(xs)` / `no duplicates in xs` natural-language *call* surfaces (so a contract can read
`requires no duplicates in xs`) are a thin parser rewrite to `xs is NoDuplicates` / `xs is Sorted`.
That rewrite is the **next increment** (see below); the predicates themselves already prove today via
Forms 1–2.

## Trigger / instantiation stability

Unbounded quantifiers can make an SMT solver loop on bad e-matching triggers. The desugaring is chosen
to keep instantiation tame:

1. **Always range-guarded.** Every sugar form emits `(lo <= i and i < hi) implies P`, never a bare
   `forall i: P`. The guard bounds the instantiation domain and gives the solver a concrete `select`
   pattern on `a[i]` to trigger from.
2. **`select`-shaped bodies.** Bodies talk about `a[i]` (→ `(select a i)`), the canonical array trigger.
   Adjacent-pair predicates (`a[i] <= a[i+1]`) keep the offset literal so the trigger term stays a
   single `select` over `i+1`.
3. **One binder per range form.** The `in <range>` form is single-binder; multi-index predicates
   (`NoDuplicates`) stay explicit so their guard is visible and reviewable rather than auto-synthesised.
4. **Spec-only, never executed.** As today, a quantified refinement is proof-only; with SMT off it
   degrades to a spec-only warning (`TestQuantifiedLawSpecOnlyWhenSMTOff`), never a broken runtime check.

## Tests

* `compiler/src/parser/quantifier_sugar_test.go` — desugar **shape** for `a.indices` and `lo ..< hi`.
* `compiler/src/semantic/smt_discharge_test.go`
  (`TestSMTProvesQuantifierRangeSugar` / `TestSMTDeclinesFalseQuantifierRangeSugar`) — the range sugar
  **proves exactly what the canonical form proves**, and a false instance is **declined** (soundness).

## Status / next increments

Landed: Form 1 (range binder), Form 2 (`a.indices`), and concept predicates expressible as library laws
(Form 3 bodies). Proven end-to-end under SMT; violation caught.

Next:
1. Natural-language call surface: `sorted(xs)` and `no duplicates in xs` → `xs is Sorted` / `xs is
   NoDuplicates` (parser rewrite in contract/`requires`/`ensure` position).
2. `all xs[..i] satisfy P` slice-prefix sugar → `forall k in 0 ..< i: P(xs[k])`.
3. Bounded-`exists` lowering (`exists i in r: P` → `exists i: guard and P`).
4. Prelude home for `Sorted` / `NoDuplicates` so they need no per-program redeclaration.
