# docs/97 — Named composable contracts (the LEGO bricks of verification)

## 1. Motivation

Elisa already has the verification *primitives*: value contracts (`requires` / `ensure`
with `result` / `old`), frame conditions (`changes` / `preserves`, docs/87), termination
measures (`decreases`, docs/86), and the **law** family (docs/85 — subjectless effect/shape
laws composed with `includes`, frame laws applied with `fulfills`, value-class refinement laws
applied with `is`).

What was missing is the *named, reusable bundle of value + frame obligations* that several
functions share. Today, two functions that must both uphold "the output is a permutation of the
input and is sorted" each restate the same `requires`/`ensure`/`changes` clauses inline. The
specification is duplicated, drifts, and is invisible as an architectural unit.

A **named contract** packages a set of `requires` + `ensure` + `changes` + `preserves`
clauses, parameterised over a subject (and any extra parameters), under one name. A function
then *applies* it by name with `uses ContractName(args)`. Verification becomes architecture: a
contract like `SortedMerge`, `Permutation`, or `AuthenticatedSession` is declared once and reused.

This is deliberately distinct from a `law`:

| construct        | shape                            | applied with     | obligation channel                  |
|------------------|----------------------------------|------------------|-------------------------------------|
| value `law`      | pure bool predicate over subject | `x is Law`       | refinement-fact / SMT premise       |
| effect `law`     | `forbids E, …` / `includes …`    | `fulfills Law`   | effect discharge class              |
| frame `law`      | `changes …` / `preserves …`      | `fulfills p is L`| frame discharge class               |
| **named contract** | bundle of requires/ensure/changes/preserves | `uses C(args)` | **value + frame, composed**         |

A law is a single obligation in one discharge class. A named contract is a *composite spanning
classes* — it is the unit an architect reasons about ("this function honours `SortedMerge`"),
expanded into the existing per-class machinery.

## 2. Declaring a named contract

```
contract SortedMerge(out: darray[i64]&, a: darray[i64], b: darray[i64]):
    requires IsSorted(a)
    requires IsSorted(b)
    ensure IsSorted(out)
    ensure out.count == a.count + b.count
    changes out
```

Grammar (contextual keyword `contract`, like `law`/`lemma`):

```
ContractDecl   = "contract" Name [ GenericParams ] "(" ParamList ")" ":" NEWLINE
                 INDENT ContractClause+ DEDENT
ContractClause = "requires" BoolExpr NEWLINE
               | "ensure"   BoolExpr NEWLINE
               | "changes"  PathList NEWLINE
               | "preserves" PathList NEWLINE
```

Rules:

* The parameter list names the contract's **formals**. The first parameter is conventionally the
  *subject*, but the contract may take any number of parameters (so `SortedMerge` can name the
  output and both inputs). Parameter *types* are written for readability and future checking; the
  current increment matches application arguments **positionally** and does not yet re-type-check
  them against the formals (see §6, stubbed).
* A contract body is exactly a sequence of `requires` / `ensure` / `changes` / `preserves`
  clauses. No executable statements, no `decreases` (a measure is a per-function property, never a
  shared bundle — see the discharge-class rule in §5).
* `ensure` clauses may reference `result` and `old(expr)` exactly as in a function body. `result`
  binds to the *applying function's* return value at expansion time.
* It is represented internally as an `ast.FuncDecl` with `IsContract = true` (reusing the whole
  declaration/generic/module machinery, exactly as `law` reuses it). Its `Requires`,
  `EnsureValues`, `Changes`, and `Preserves` slices hold the bundled clauses.

## 3. Applying a contract: `uses`

A function applies a named contract with a **leading** `uses` clause — in the same leading-contract
region as `requires`/`ensure`/`decreases` (the region-prefix grammar collision, docs on contracts,
forces these to the top of the body, not the signature line):

```
def merge(out: darray[i64]&, x: darray[i64], y: darray[i64]) -> void:
    uses SortedMerge(out, x, y)
    # … body …
```

`uses C(arg1, …, argN)` binds the contract `C`'s formal parameters to the call arguments
positionally and expands, at semantic-analysis time, into the applying function's own contract
slices:

* each `requires P` of `C`  → appended to the function's preconditions (after substitution),
* each `ensure  Q` of `C`  → appended to the function's postconditions,
* each `changes  S` of `C`  → unioned into the function's frame `changes` set,
* each `preserves S` of `C` → unioned into the function's frame `preserves` set.

Substitution rewrites every reference to a contract formal (e.g. `out`, `a`, `b`) into the
corresponding application argument (e.g. `out`, `x`, `y`). Path roots in `changes`/`preserves`
are rebased the same way (a formal root `out` becomes argument root `out`/`x`/…).

Multiple `uses` clauses compose by **conjunction of value obligations** and **union of frames**
— applying `uses A(...)` and `uses B(...)` is exactly applying all of A's and B's clauses. A
function may freely add its own extra inline `requires`/`ensure`/`changes` alongside `uses`.

## 4. Worked example — two functions sharing one contract

```
law IsSorted(a: darray[i64]) = forall i: 0 <= i < a.count - 1 implies a[i] <= a[i + 1]

contract NonShrinking(out: darray[i64]&, src: darray[i64]):
    requires IsSorted(src)
    ensure out.count >= src.count
    changes out

def copy_sorted(dst: darray[i64]&, s: darray[i64]) -> void:
    uses NonShrinking(dst, s)
    # … fills dst from s …

def grow_sorted(buf: darray[i64]&, s: darray[i64]) -> void:
    uses NonShrinking(buf, s)
    # … appends to buf from s …
```

Both functions inherit, with zero duplication:

* precondition `IsSorted(<their src arg>)` — checked at *their* call sites,
* postcondition `<their out arg>.count >= <their src arg>.count`,
* frame obligation: they may write `<their out arg>` and nothing else caller-visible.

Changing `NonShrinking` in one place re-verifies both functions.

## 5. Composition + discharge-class rules

Composition is purely the **conjunction** of value premises/guarantees and the **union** of frame
sets. The discharge-class discipline of docs/85 is preserved verbatim:

* **value** (`requires`/`ensure`): discharged by the value tier (affine → linear → SMT ladder).
  Conjunction is sound: more premises only strengthen the caller's proof obligation; more
  guarantees only strengthen what the function must prove.
* **frame** (`changes`/`preserves`): discharged by the frame checker (docs/87). Union is the
  correct composition — `changes(A) ∪ changes(B)` is the smallest write-set honouring both.
* **measure** (`decreases`): **never** part of a named contract. A termination measure is a
  property of a *recursion*, not a reusable interface, and laundering a measure into a shared
  premise would let one function's well-foundedness silently stand in for another's. A `decreases`
  clause inside a `contract` body is a hard error.
* **effect** / **shape**: out of scope for this increment. These already compose through `law …
  includes …` / `fulfills`; a future increment may let a `contract` name member laws via
  `includes`, expanding into the function's `Fulfills` set (§6).

A named contract therefore *only* composes the value and frame classes, exactly the two classes
whose composition operator (∧ for value, ∪ for frame) is monotone and sound. It cannot reach
across into measure.

## 6. Implementation status (first increment)

Landed (parser + semantic):

* `contract Name(params):` declaration parsed into `FuncDecl{IsContract: true}` carrying its
  `Requires` / `EnsureValues` / `Changes` / `Preserves` bundles.
* `uses Name(args)` leading clause parsed as `ContractStmt{Kind: ContractUses}` and lifted with
  the other leading contracts.
* A pre-analysis expansion pass (`expandUsesContracts`) substitutes formals→arguments and folds
  the contract's clauses into the applying function's own slices, so the *existing* requires
  discharge, ensure proof, and frame checker see them with no further changes — meaning a `uses`d
  precondition is automatically checked at every call site of the applying function.
* Diagnostics: unknown contract name, arity mismatch, `decreases` inside a contract, and `uses` of
  a non-contract are hard errors.

Stubbed / next increments:

* Parameter **type checking** of `uses` arguments against contract formals (currently positional
  only).
* `contract … includes Law, …` to fold effect/shape member laws into `Fulfills` (effect/shape
  discharge-class composition).
* Generic contracts (`contract Foo[T](...)`) — parsed but not yet specialised per application.
* `uses` on `extern` boundary declarations.
