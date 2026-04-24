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
- parameter defaults are not accepted in implicit bundle declarations, implicit bundle signatures, or export wrapper signatures

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
- family-level signature effects such as `effects[ConsoleEffect]` satisfy member signals and calls from the same family
- function bodies infer their callable permission surface from effectful operations and local grants
- explicit signature permissions still matter on surfaces without bodies, such as `extern` declarations and function types
- explicit signature permissions do not by themselves satisfy local-grant checking inside the body

Top-level `effectalias` declarations package an error-set clause and a permission clause into one reusable alias. Signatures use one bracketed `effects[...]` row, so aliases, direct errors, and direct capabilities can live in the same place.

```context
effectalias FrontendEffects = error[ParseErr] can[Abort.Panic, Memory.Allocate]

def parse() -> i64 effects[FrontendEffects]:
    return 1

def parse_debug() -> i64 effects[FrontendEffects, Console.Write]:
    return 1

extern register(callback: func() -> void effects[FrontendEffects]) -> void
```

This is compile-time surface only. The alias expands during semantic analysis; it does not create a runtime object or LLVM artifact.

Current rules:

- aliases may be used on function declarations and function types
- aliases may bundle `error[...]`, `can[...]`, or both
- bracketed signature rows may include aliases, direct capability refs such as `Abort.Panic`, and direct errors such as `error ParseErr`
- `effect` declarations and `effectalias` aliases are compile-time surface only; both lower into the existing semantic effect model rather than a runtime object

### Local `can` grants and formatter normalization

Function types and other body-less surfaces use declaration syntax such as `effects[Console.Write]`. Function declarations with bodies can usually omit signature permissions because effect inference records them from local grants and effectful operations. Inside a body, effectful use sites still need an explicit local grant.

```context
def write_once(text: u8&) -> int:
    return puts(text) can Console.Write

def write_pair(left: u8&, right: u8&) -> int:
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
- inferred or explicit surface permissions on the enclosing function or alias (`effects[...]`) do not replace an explicit local grant at the use site
- `-emit fmt` always prints local grants in surface syntax rather than declaration syntax
- the formatter conservatively inlines simple one-statement grant blocks into `... can ...` form for returns, assignments, declarations, tuple binds, discards, `as` rebinds, and expression statements
- the formatter keeps block form for multi-statement regions and for statements it cannot safely rewrite, including statement-position `panic(...)`
- when a granted expression contains `try ... else ...` or `value else fallback`, the formatter parenthesizes the expression so the grant applies to the whole expression

Style guidance:

- prefer an inline grant when one operation needs the permission once
- prefer a `can ...:` block when multiple operations share the same grant or when keeping the grant as a block makes control flow or non-null narrowing clearer

## Named bundles

Bundles are the canonical model for named groups of inputs. `implicit` bundles are ambient dependencies, while `explicit` bundles are reusable named argument packs.

```context
bundle ParseCtx implicit:
    parser: i64
    alloc: i64

bundle Pair explicit:
    left: i64
    right: i64 = 7

def inner() with ParseCtx -> i64:
    return parser + alloc

def add(use Pair) -> i64:
    return left + right

def outer() with ParseCtx -> i64:
    return inner()

def drive(width: i64) -> i64:
    parser: i64 = 7
    alloc: i64 = 9
    with args(use Pair(left:), width:):
        return add(use Pair(right: 5, left:), right: width) + inner() with ParseCtx(..)
```

Implicit bundle values have two call-site surfaces:

```context
with ParseCtx(.., alloc = override_alloc):
    return inner()

return inner() with ParseCtx(.., alloc = override_alloc)
```

Current rules:

- `bundle Name implicit:` declares an ambient dependency bundle
- `bundle Name explicit:` declares a reusable explicit argument pack
- legacy `context Name:` and `params Name:` still parse, but the formatter emits canonical `bundle` declarations
- `def f(...) with Name -> T` makes implicit bundle fields visible by field name inside the function body
- `def f(use Name)` expands an explicit bundle into the function's explicit parameter set
- `call(use Name(...), other: ...)` applies an explicit bundle at a call site
- `with args(...)` installs ambient explicit arguments for nested calls inside a block
- calls auto-forward when the caller already has the same implicit context in scope
- `with Name(..)` spreads same-named ambient values into the bundle, and explicit overrides win over the spread values
- the implicit bundle surface works as a statement block and as a trailing call bundle
- implicit bundle fields do not accept parameter defaults
- explicit bundle fields may declare defaults

Current v1 restrictions:

- exported wrappers must not target functions with implicit parameters
- `__cast__` hooks must not declare implicit parameters

Explicit bundle call rules:

- pack members participate in the same named-argument resolution as ordinary explicit parameters
- shorthand forms like `left:` work inside pack application just like ordinary named calls
- explicit named arguments outside the pack may override values supplied by pack defaults or ambient `with args(...)` state
- ambient args are compile-time call-resolution sugar, not runtime objects
- top-level explicit bundle declarations are compile-time only and are ignored by code generation when emitting top-level declarations

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
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
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

## Grammar DSL for parsers and tree frontends

The current grammar surface is aimed at handwritten recursive-descent frontends that still want a compact parser DSL for the repetitive parts: tokens, recovery, lists, precedence, postfix/suffix, and prefix forms.

```context
grammar PascalExprGrammar over Token using ParserState:
    cursor state
    alloc alloc
    token:
        IDENT
        INTEGER
        STRING
        LPAREN "("
        RPAREN ")"
        PLUS "+"
        MINUS "-"
        STAR "*"
        SLASH "/"
        DIV "div"
        MOD "mod"
        AND "and"
        NOT "not"
        OR "or"
        EQ "="
        NOTEQ "<>"
        LT "<"
        LTEQ "<="
        GT ">"
        GTEQ ">="
    channel span: Span
    channel node
    expression() -> Pascal.Expr:
        result = precedence(additive):
            atom = choice(
                prefix(.PLUS, .MINUS, .NOT) atom() -> make_unary_expr(alloc, op, operand),
                delimited(.LPAREN, expression(), .RPAREN, ParseMessageKey.ExpectedRightParen),
                name_atom(),
                integer_atom(),
                string_atom()
            )
            multiplicative(left = atom()):
                op = .STAR | .SLASH | .DIV | .MOD | .AND right = atom() -> make_binary_expr(alloc, left, op, right)
            additive(left = multiplicative()):
                op = .PLUS | .MINUS | .OR right = multiplicative() -> make_binary_expr(alloc, left, op, right)
        return result
    name_atom() -> Pascal.Expr:
        seq:
            .IDENT(token)
            expr(make_name_expr(alloc, token))
    integer_atom() -> Pascal.Expr:
        seq:
            .INTEGER(token)
            expr(make_integer_expr(alloc, token))
    string_atom() -> Pascal.Expr:
        seq:
            .STRING(token)
            expr(make_string_expr(alloc, token))
```

### Grammar headers

`grammar Name over Token using ParserState:` declares a grammar over a token value type and parser state type. `extend grammar Name:` adds productions or production alternatives later.

Current header declarations:

- `cursor state` tells lowering which parser-state value owns the current cursor
- `alloc alloc` supplies the active tree/arena owner expression used by generated parser helpers
- `token:` declares token aliases for use as `.IDENT` inside the grammar
- grouped token entries may be bare (`IDENT`) or dotted (`.IDENT`) and may include an optional literal such as `LPAREN "("`
- `channel name` declares a generated mutable channel with inferred/default behavior
- `channel span: Span = combine_span($start.span, $end.span)` declares a typed channel with a default expression
- `uses OtherGrammar` imports productions and token aliases from another grammar

`token:` is the canonical style for token aliases:

```context
token:
    IDENT
    INTEGER
    LPAREN "("
    RPAREN ")"
```

The older repeated declaration form still parses for compatibility, but the formatter normalizes aliases back into the grouped block form:

```context
token .IDENT
token .INTEGER
token .LPAREN "("
token .RPAREN ")"
```

### Core grammar terms

Grammar productions are ordinary named parser functions whose bodies contain grammar terms plus normal expressions.

```context
statement() -> Pascal.Stmt recover(ParseMessageKey.ExpectedStatement, until(.SEMICOLON, .END, token(TokenKind.EOF))):
    node <- statement_core()
    return node

assignment() -> Pascal.Stmt:
    .IDENT(name_token)
    lookahead(.ASSIGN)
    cut
    required(.ASSIGN, ParseMessageKey.ExpectedAssignmentOperator)
    value = expression()
    node <- expr(make_assign_stmt(alloc, name_token, value))
    return node
```

Current core terms:

- `.IDENT` matches a token alias and returns the matched token
- `.IDENT(token)` matches and binds the token in one step
- `"literal"` matches by token text/literal
- `token(TokenKind.EOF)` matches by explicit token kind expression
- `name = term` binds a successful grammar term result
- `name <- term` assigns a grammar term result into an existing binding or channel
- `expr(value)` injects an ordinary expression result into grammar flow
- `guard(cond)` succeeds only when `cond` is true
- `lookahead(term)` matches without consuming
- `cut` commits the current alternative so later fallback alternatives are not attempted
- `required(term, MessageKey)` records a parse error instead of failing when `term` is absent
- `recover(MessageKey, until(...), fallback)` can be attached to terms or productions for synchronized error recovery

### Choice, Sequence, And Prefix

Choices can be written either with `choice(...)` or token/term pipes:

```context
op = .PLUS | .MINUS | .OR
atom = choice(.IDENT(token), .INTEGER(token), grouped_expr())
```

Sequences use block form as the canonical style:

```context
pair = seq:
    .LPAREN
    value = expression()
    .RPAREN
    expr(value)
```

Current sequence rules:

- block `seq:` is the formatter output wherever a term can own an indented block
- comma-separated `seq(a, b, c)` and comma-free `seq(a b c)` remain accepted for compact nested positions
- inline `seq(...)` is mainly for places where a block cannot syntactically appear, such as inside `choice(...)` arguments
- the value of a sequence is the value of its final term
- if any non-recovered term fails, the sequence restores the cursor snapshot and fails

Prefix operators have dedicated sugar:

```context
prefix(.PLUS, .MINUS, .NOT) atom() -> make_unary_expr(alloc, op, operand)
```

This desugars to the existing lower-level terms:

```context
seq:
    op = choice(.PLUS, .MINUS, .NOT)
    operand = atom()
    expr(make_unary_expr(alloc, op, operand))
```

The generated names are currently `op` and `operand`; use those in the result expression.

### Lists And Delimiters

The list-family helpers use readable DSL-style forms as the canonical style.

```context
statements = separated statement() by .SEMICOLON until(.END, token(TokenKind.EOF))
names = separated required(.IDENT, ParseMessageKey.ExpectedDeclName) by .COMMA until(.COLON, token(TokenKind.EOF))
decls = flatrepeat variable_decl_group() until(.BEGIN, token(TokenKind.EOF))
args = delimited(.LPAREN, separated expression() by .COMMA until(.RPAREN, token(TokenKind.EOF)), .RPAREN, ParseMessageKey.ExpectedRightParen)?
maybe_name = .IDENT?
```

Current list-family terms:

- `term?` succeeds with an optional result
- `optional term` and `optional(term)` remain accepted for compatibility, but the formatter emits postfix `?`
- `repeat term until(...)` parses zero or more items and returns the collected list
- `flatrepeat term until(...)` parses zero or more list-producing items and flattens them
- `separated term by sep until(...)` is the canonical separated-list form
- `list term separated by sep until(...)` and function-style `separated(term, sep, until(...))` remain accepted, but the formatter emits `separated term by sep until(...)`
- `delimited(open, body, close, MessageKey)` parses `open`, returns `body`, and requires `close`
- `until(...)` accepts token aliases, literal tokens, explicit `token(...)` terms, or other recoverable terms

### Precedence, suffix, and postfix

Named precedence blocks define a small expression parser without hand-writing loops.

```context
expression() -> Pascal.Expr:
    result = precedence(additive):
        atom = choice(integer(), name(), grouped())
        multiplicative(left = atom()):
            op = .STAR | .SLASH right = atom() -> make_binary_expr(alloc, left, op, right)
        additive(left = multiplicative()):
            op = .PLUS | .MINUS right = multiplicative() -> make_binary_expr(alloc, left, op, right)
    return result
```

Current precedence rules:

- the argument to `precedence(result_level)` names the level whose value is returned
- `level = seed` defines a seed/helper level
- `level(left = lower_level()): ...` defines a left-associative looping level
- each arm parses an operator term, optional right-side bindings, and a `-> result` expression
- arms are attempted in declaration order
- recursive calls to named levels are resolved inside the precedence block

Suffix and postfix are related loop surfaces for expression tails and statement-like continuations:

```context
condition() -> Pascal.Expr:
    node <- suffix(left = expression()):
        op = .EQ | .NOTEQ right = expression() -> make_binary_expr(alloc, left, op, right)
    return node
```

Use `suffix` when the seed value should be repeatedly transformed by arms. Use `postfix` for postfix-tail parsing where that spelling better communicates the grammar role.

### Lowering and inspection

The grammar DSL lowers into ordinary llcontext functions. The compiler exposes inspection modes for debugging generated parser code:

```sh
go run ./src -emit lower path/to/file.llcontext
go run ./src -emit grammar-lowered path/to/file.llcontext
```

Current implementation notes:

- grammar sugar lowers to existing AST terms where possible, rather than introducing runtime parser objects
- `prefix(...)` currently lowers to `seq(op = choice(...), operand = ..., expr(...))`
- token aliases are rewritten before lowering, so `.IDENT` can map onto the real token kind expression
- recovery and required terms depend on the grammar `cursor` declaration to restore or advance parser state correctly
- tree AST construction remains ordinary llcontext code, so teams can use low-level `new[alloc] Tree.Node(...)` directly or wrap common span/child patterns in helpers such as `make_binary_expr(...)`

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
def visit(frozen: Expr.Store[Frozen]) -> void effects[Pool.Submit, Pool.WaitAll]:
    pool workers(2u):
        parallel for node in frozen:
            pass

def walk_tags(tags: dview[Expr.Tag]) -> void effects[Pool.Submit, Pool.WaitAll]:
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
alloc: mutable Arena& = &owner as mutable Arena&
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

def finish_ok(mutable job: ParseJob[Pending]&) -> void ensures job => Ready:
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

def read_ref(alloc: ScratchArena&) -> i64:
    return alloc.value

def score_ref(box:  Box&, delta: i64 = 1) -> i64:
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
