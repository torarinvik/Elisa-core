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

def run() -> void:
    can FooEffect, ConsoleEffect.Write:
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
- function bodies infer their callable permission surface from effectful operations and local grants
- explicit signature permissions still matter on surfaces without bodies, such as `extern` declarations and function types
- explicit signature permissions do not by themselves satisfy local-grant checking inside the body

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

### Local `can` grants and formatter normalization

Function types and other body-less surfaces use declaration syntax such as `can[Console.Write]`. Function declarations with bodies can usually omit signature permissions because effect inference records them from local grants and effectful operations. Inside a body, effectful use sites still need an explicit local grant.

```context
def write_once(text: any u8&) -> int:
    return puts(text) can Console.Write

def write_pair(left: any u8&, right: any u8&) -> int:
    can Console.Write:
        puts(left)
        return puts(right)

def checked_render() -> int:
    return (try checked() else 1) can Console.Format
```

Current rules for local grants:

- local grants use surface syntax: inline `expr can Family.Member` or block `can Family, Family.Member:`
- local grants are checked against the same effect families as effectful calls and `signal`
- family grants such as `can ConsoleEffect` satisfy member uses such as `ConsoleEffect.Write`
- inferred or explicit surface permissions on the enclosing function or alias (`can[...]` or `effects SomeAlias`) do not replace an explicit local grant at the use site
- `-emit fmt` always prints local grants in surface syntax rather than declaration syntax
- the formatter conservatively inlines simple one-statement grant blocks into `... can ...` form for returns, assignments, declarations, tuple binds, discards, `as` rebinds, and expression statements
- the formatter keeps block form for multi-statement regions and for statements it cannot safely rewrite, including statement-position `panic(...)`
- when a granted expression contains `try ... else ...` or `value else fallback`, the formatter parenthesizes the expression so the grant applies to the whole expression

Style guidance:

- prefer an inline grant when one operation needs the permission once
- prefer a `can ...:` block when multiple operations share the same grant or when keeping the grant as a block makes control flow or non-null narrowing clearer

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

## Tree exact updates and `rewrite ... default`

Exact tree members reuse the same brace-update surface as structs.

```context
tree Lua:
    common:
        span: i64
    @role(expr)
    node Expr:
        Int(value: i64)
        Binary(child left: Expr, child right: Expr)
    block Block:
        items: darray[Expr]

def rotate(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
    in perm:
        return node{left, right}

def rotate_into(owner: Arena, node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    return new[alloc] node{left, right}

def simplify(node: Lua.Node) -> Lua.Node:
    in perm:
        return rewrite node as Lua.Node:
            Lua.Expr.Int(expr):
                default
            Lua.Expr.Binary(expr, left, right):
                expr{left, right}
            Lua.Block(block, items: items):
                default
```

Current rules:

- `member{field, other = value}` works for exact tree members such as nodes, blocks, structs, and exact variants
- tree exact updates preserve the exact member tag and copy every unchanged field from the source handle
- bare tree exact updates still require an active tree owner such as `in perm:` or `in owner:` because the result is a fresh tree value
- `new[owner] member{...}` is the explicit-owner form when no active owner scope should be used
- inside an exact `rewrite` arm, `default` rebuilds the current exact member using the already rewritten child results
- `default` is contextual rather than a new global keyword; outside an exact `rewrite` arm it is rejected
- `default` also rebuilds `children` sequence fields, materializing fresh arrays in the active tree owner when needed

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

## Tree `rewrite`

`rewrite` is the tree-transform spelling for bottom-up tree reconstruction. It uses the selected traversal root for recursion, while preserving the static type of the source expression and each named child binding.

```context
def simplify(node: Expr) -> Expr:
    in perm:
        return rewrite node as Expr:
            Expr.Int(expr):
                default
            Expr.Add(expr, left, right):
                default
```

Current rules:

- `rewrite value as Root:` is fold-backed, but it specializes child-result bindings to the original child edge types instead of forcing every rewritten child to have one uniform result type
- arm heads, guards, exact tree targets, variant targets, wildcard arms, and named child-result bindings follow the existing `fold` arm rules
- named child bindings such as `left` and `right` are the already-rewritten child results
- use a family root such as `Lua.Node` or `ATPLSyntax.Node` when a category has heterogeneous structural children such as expressions, statements, and blocks
- `rewrite` is contextual, so an ordinary function or local named `rewrite` still parses normally in call position such as `rewrite(value)`

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

## Checked `ensures` clauses

Function and extern declarations may carry checked poststate summaries for ref-visible paths.

```context
struct ParseJob[state Pending | Ready | Failed]:
    stage: mutable int

    derive state:
        Pending when self.stage == 0
        Ready when self.stage == 1
        Failed when self.stage == 2

def finish_ok(mutable job: any ParseJob[Pending]&) -> void ensures job => Ready:
    job.stage <- 1

struct HeapPairNode:
    value: i32

extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void ensures node => !
```

Current rules:

- ordinary functions and `extern` declarations may carry `ensures` clauses
- supported effects are named-state poststates, refstate poststates such as `!`, `&`, and `&?`, and `preserve`
- targets use parameter-rooted field paths such as `job`, `team.player`, or `holder.slot`
- bool-returning functions may use branch-sensitive forms like `ensures return true => job => Ready`
- ordinary function bodies must prove every applicable `ensures` clause on each normal return path
- call analysis applies the declared poststate to the caller-visible tracked type after the call when the target argument path is known
- `ensures` is static effect typing, not a runtime contract/assertion feature

## Conservative call-site auto-borrow

The compiler may insert a borrow automatically at call sites when the callee expects a compatible ref and the argument is an obvious addressable lvalue.

```context
struct ScratchArena:
    value: i64

struct Holder:
    arena: ScratchArena

struct Box:
    value: i64

def read_ref(alloc: any ScratchArena&) -> i64:
    return alloc.value

def score_ref(box: any Box&, delta: i64 = 1) -> i64:
    return box.value + delta

def read(box: Box, holder: mutable Holder) -> i64:
    left = read_ref(holder.arena)
    right = box.score_ref(5)
    return left + right
```

Current rules:

- ordinary calls may autoref addressable identifiers, field projections, and supported index projections when a compatible non-null ref is expected
- the same conservative autoref path is used by struct-literal argument resolution, implicit-context argument resolution, and receiver-style extension or UFCS rewriting
- existing refs may upcast storage to `any` automatically when pointee type, refstate, region, and mutability are otherwise already compatible
- mutable ref parameters still require a writable argument path; immutable locals, immutable fields, constants, and other readonly paths are rejected
- nullable ref parameters are not implicit autoref targets
- the compiler does not silently perform broader storage-changing or state-changing coercions beyond that conservative borrow/upcast surface; use explicit `&` and `as` when the intended conversion is not obvious from the lvalue itself
