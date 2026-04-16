# Current surface ergonomics

This note documents implemented source-language features that landed after many of the older design notes in this folder.

Unlike several of the earlier files here, this is not a forward-looking proposal. It is a practical reference for syntax the current compiler accepts today.

## Default arguments, named calls, and `..` forwarding

Default values are supported on trailing parameters of ordinary functions and `extern` declarations.

```context
def sum3(x: i64, y: i64 = 1, z: i64 = 2) -> i64:
    return x + y + z

def build(parser: i64, offset: i64) -> i64:
    a: i64 = sum3(z: 9, x: 5)
    b: i64 = consume(.., offset: 5)
    return a + b
```

Current rules:

- omitted arguments are filled from defaults after named-argument resolution
- named calls may fill any subset of parameters as long as every required parameter is satisfied exactly once
- shorthand named arguments use `name:` as sugar for `name: name`
- call forwarding `..` copies same-named value bindings from the current scope into matching parameters
- `..` may appear at most once, must appear before other explicit call arguments, and only combines with named explicit arguments
- forwarding is currently rejected for variadic callees
- parameter defaults are not accepted in context declarations, implicit-context signatures, or export wrapper signatures

If a shorthand named argument such as `missing:` has no in-scope value named `missing`, semantic analysis reports that directly.

## Effect declarations, aliases, and `signal`

Top-level `effect` declarations introduce named effect families directly in source. An effect with members behaves like a permission family with explicit members; `pass` declares a marker effect with no members.

```context
effect FooEffect:
    pass

effect ConsoleEffect:
    Write
    Flush

def run() -> void can[FooEffect, ConsoleEffect.Write]:
    signal FooEffect
    signal ConsoleEffect.Write
```

`signal` is a zero-runtime statement. It does not emit a runtime trap or object; it exists to make effect usage explicit so the surrounding function contract still records and checks the effect.

Current rules for `effect` and `signal`:

- `effect Name:` declares a family directly
- `effect Name: pass` is the marker-effect form with no members
- indented names such as `Write` and `Flush` declare members of that family
- `signal Name` records use of a whole-family effect
- `signal Name.Member` records use of one concrete member
- `signal` participates in the same effect inference and local-grant checking as calls to effectful functions
- family-level grants such as `can[ConsoleEffect]` satisfy member signals and calls from the same family

Top-level `effects` declarations still package an error-set clause and a permission clause into one reusable alias.

```context
effects FrontendEffects = error[ParseErr] can[Abort.Panic, Memory.Allocate]

def parse() -> i64 effects FrontendEffects:
    return 1

extern register(callback: func() -> void effects FrontendEffects) -> void
```

This is compile-time surface only. The alias expands during semantic analysis; it does not create a runtime object or LLVM artifact.

Current rules:

- aliases may be used on function declarations and function types
- aliases may bundle `error[...]`, `can[...]`, or both
- a signature that uses `effects SomeAlias` must not also spell out explicit `error[...]` or `can[...]` clauses on the same surface
- `effect` declarations and `effects` aliases are compile-time surface only; both lower into the existing semantic effect model rather than a runtime object

## Implicit contexts

Implicit contexts let a function declare a named bundle of extra ambient parameters.

```context
context ParseCtx:
    parser: i64
    alloc: i64

def inner() with ParseCtx -> i64:
    return parser + alloc

def outer() with ParseCtx -> i64:
    return inner()

def drive() -> i64:
    parser: i64 = 7
    alloc: i64 = 9
    return inner() with ParseCtx(..)
```

There are two call-site surfaces:

```context
with ParseCtx(.., alloc = override_alloc):
    return inner()

return inner() with ParseCtx(.., alloc = override_alloc)
```

Current rules:

- `context Name:` declares the bundle shape
- `def f(...) with Name -> T` makes those bindings visible by field name inside the function body
- calls auto-forward when the caller already has the same implicit context in scope
- `with Name(..)` spreads same-named ambient values into the bundle, and explicit overrides win over the spread values
- the same bundle surface works as a statement block and as a trailing call bundle
- context fields do not accept parameter defaults

Current v1 restrictions:

- exported wrappers must not target functions with implicit parameters
- `__cast__` hooks must not declare implicit parameters

## Explicit argument packs with `params` and `with args`

Top-level `params` declarations define reusable explicit named-argument packs.

```context
params Pair:
    left: i64
    right: i64 = 7

def add(use Pair) -> i64:
    return left + right

def build(left: i64, width: i64) -> i64:
    with args(use Pair(left:), width:):
        return add(use Pair(right: 5, left:), right: width)
```

The important surfaces are:

- `params Name:` defines the pack shape and any defaults
- `def f(use Name)` expands the pack into the function's explicit parameter set
- `call(use Name(...), other: ...)` applies the pack at a call site
- `with args(...)` installs ambient explicit arguments for nested calls inside a block

Current rules:

- pack members participate in the same named-argument resolution as ordinary explicit parameters
- shorthand forms like `left:` work inside pack application just like ordinary named calls
- explicit named arguments outside the pack may override values supplied by pack defaults or ambient `with args(...)` state
- ambient args are compile-time call-resolution sugar, not runtime objects
- top-level `params` declarations are compile-time only and are ignored by code generation when emitting top-level declarations

## Brace destructuring, field punning, and record updates

Brace forms now work consistently across destructuring, literal construction, `is` patterns, `match` patterns, and record updates.

```context
struct Row:
    left: int
    right: int
    flag: bool

def run(row: Row, flag: bool) -> int:
    let {left: first, right} = row
    built: Row = Row{left: first, right, flag}
    next: Row = built{flag, right = first}

    if row is Row{left, right: current, flag: row_flag}:
        return current

    match next:
        Row{left, right: current, flag}:
            return current
```

Current rules:

- `let {field}` binds `field` directly from a known struct value
- `let Type{field}` is the typed version when the surface should name the expected struct explicitly
- `field` inside a brace literal or brace pattern is field punning sugar for “use the same name on both sides”
- `field: alias` renames the bound local or supplied expression source
- `base{field, other = expr}` creates a record-update expression by copying `base` and replacing only the mentioned fields

The same brace destructuring grammar also works for store-row values:

```context
for {name_key, depth} in pending.rows():
    total <- total + name_key + depth
```

## Filtered iterable `for`

Iterable loops may now include an inline filter after the source expression.

```context
for {left, right: value} in items if left != 0:
    total <- total + value
```

Current rules:

- the binder runs before the filter, so the filter may reference destructured names such as `left`
- the loop binder may be a simple name or an irrefutable brace destructure pattern
- this works over ordinary iterable sources and store-row iterators such as `rows()`

## `do:` expression blocks

`do:` introduces an expression-valued block with setup statements followed by a final value expression.

```context
def keep() -> i64:
    value = do:
        base = 40
        base + 2
    return value

def call() -> i64:
    return consume(x: do:
        seed = 3
        seed + 1
    )
```

Current rules:

- `do:` may appear anywhere an ordinary expression is accepted
- the body may contain setup statements, but the final line must still be a value expression
- the same surface works in direct assignments, call arguments, named arguments, and list elements
- `do` remains contextual, so `consume(do: 3)` still parses as a named argument rather than a block expression

## Index fallback

Index fallback adds an explicit recovery expression to an index operation.

```context
def read(xs: darray[int], i: usize) -> int:
    return xs[i] else 0
```

Current rules:

- `source[index] else fallback` keeps the ordinary indexing semantics on success and yields `fallback` on the miss path
- the fallback expression must match the indexed element type
- the fallback surface is for value reads only; it is not an assignment target or a ref-binding surface

## `defer` statements

The current compiler accepts two explicit defer modes.

```context
def keep() -> int:
    defer block:
        pass
    defer function:
        pass
    return 0
```

Current rules:

- `defer block:` runs when the current block scope exits
- `defer function:` runs on function exit rather than the nearest nested block exit
- defer bodies are ordinary statement blocks and may capture surrounding locals
- `defer` is contextual, so ordinary identifiers like `defer_value` and calls like `defer(x)` still parse normally outside defer position

## Stores, rows, and dict defaulting sugar

The current surface includes compact syntax for row-oriented stores and defaulting dictionary helpers.

```context
store PendingGotoStore:
    name_key: u32
    depth: u32

def build(values: mutable dict[dstr[key_shape], i64]&, key: dstr[key_shape]) -> i64:
    slot = values.get_or_insert(key):
        42
    for {name_key, depth} in pending.rows():
        return name_key.i64() + depth.i64()
    return slot[0u]
```

Current rules:

- `store Name:` declares a row-oriented storage surface with named fields
- `rows()` yields readonly row values that work with ordinary field access and brace destructuring
- `values.get_or_insert(key): ...` rewrites the trailing block into the default-value argument for `get_or_insert`
- `values.entry(key).get_or_insert(): ...` does the same thing for the entry API surface
- the generic syntax parses for more than one key family, but the current runtime-backed helper surface is primarily validated for `dict[dstr[key_shape], V]` unless matching helper overloads are supplied

## Pool scopes and `parallel for`

The current explicit parallel loop surface is pool-scoped rather than implicit.

```context
def visit(frozen: Expr.Store[Frozen]) -> void can[Pool.Submit, Pool.WaitAll]:
    pool workers(2u):
        parallel for node in frozen:
            pass

def walk_tags(tags: dview[Expr.Tag]) -> void can[Pool.Submit, Pool.WaitAll]:
    pool workers(2u):
        parallel for tag at i in tags:
            if tag == Expr.Tag.Add:
                _ = i
```

Current rules:

- `pool workers(count):` introduces the required enclosing pool scope
- `parallel for item in source:` is the basic form
- `parallel for item at index in source:` adds an explicit index binder
- current sources must be either frozen packed stores or readonly contiguous exact-extent views
- captured outer values must still satisfy the compiler's existing thread-transfer checks
- this is the current implemented parallel loop feature, not just a proposal placeholder

## Cascade blocks and cascade expressions

Receiver-oriented shorthand is available in both statement and expression form.

```context
cascade report:
    .inner.value <- value
    .flag <- true

return cascade row => .ref_count != 0
```

Inside the cascade surface, a leading-dot member path is resolved relative to the cascade target.

Current rules:

- `cascade target:` is the statement form for grouped mutations or multiple related statements
- `cascade target => expr` is the expression form for a single computed value
- leading-dot shorthand is only meaningful inside cascade rewriting positions
- `cascade` is contextual, so a normal identifier named `cascade` still works where the grammar expects an ordinary name

## Lambda literals

Anonymous function literals now have dedicated surface syntax.

```context
def build() -> func(i64) -> i64:
    return lambda (value: i64) -> i64:
        return value + 1

def capture(offset: i64) -> func(i64) -> i64:
    return λ value: value + offset
```

Current rules:

- both `lambda` and `λ` are accepted spellings
- lambdas may use a block body or a single expression body
- shorthand parameter forms like `λ value: ...` rely on the expected function type to provide parameter typing
- lambdas capture surrounding locals and may return closures
- `lambda` is contextual, so a parameter or local named `lambda` still parses as an identifier outside lambda position

## Char literals

Single-quoted character literals are now part of the accepted surface.

```context
const NEWLINE: char = '\n'
const LETTER: char = 'A'
```

Current rules:

- a char literal must decode to exactly one code unit
- the usual escape forms such as `\n`, `\t`, `\r`, `\0`, `\xNN`, and `\uNNNN` are accepted
- the builtin `char` type participates in the normal conversion surface, including helper-backed postfix forms such as `.i64()` when available

## Ordinary casts and postfix cast hooks

The newer concise cast surface is `as`:

```context
alloc: mutable any Arena& = &owner as mutable any Arena&
```

This remains separate from postfix cast hooks:

```context
const enum Op of i8:
    Add = 0
    Sub = 1

def __cast__(op: Op) -> i64:
    if op == Op.Add:
        return 10
    return 20

def score(op: Op) -> i64:
    return op.i64()
```

Current rules:

- `as` is the concise ordinary cast/coercion surface used throughout self-hosted code
- postfix shorthand like `op.i64()` dispatches to a visible exact `__cast__(value: Source) -> Target` hook when one exists
- ordinary explicit casts continue to use normal cast rules rather than hook dispatch
- the postfix hook surface is intentionally exact-source/exact-target rather than a broad overload search

That distinction matters when reading code: `value as T` is an explicit cast, while `value.T()` is a hook-backed conversion shorthand.