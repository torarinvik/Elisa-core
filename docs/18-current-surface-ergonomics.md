# Current surface ergonomics

This note documents implemented source-language features that landed after many of the older design notes in this folder.

Unlike several of the earlier files here, this is not a forward-looking proposal. It is a practical reference for syntax the current compiler accepts today.

## Variant `is` payload patterns

Variant `is` tests can destructure named payloads directly. Named payload patterns in `is` tests may be partial, so a condition can inspect or bind only the fields it needs.

```elisa
def is_nil_left(node: Lua.Expr) -> bool:
    return node is Lua.Expr.Binary(left: Lua.Expr.Nil)

def right_span(node: Lua.Expr) -> i64:
    if node is Lua.Expr.Binary(right: rhs):
        return rhs.span
    return 0
```

When several payload fields are needed, bind a variant projection alias with `as`. The alias has the exact variant-view type inside the truthy branch, so ordinary field access reads the variant payload without repeating each binding in the condition.

```elisa
def binary_score(node: Lua.Expr) -> i64:
    if node is Lua.Expr.Binary as binary:
        return binary.left.span + binary.right.span
    return 0
```

Use direct payload bindings when the branch only needs one or two values, and use `as alias` when the branch works with the variant as a small record-like view. Prefer a full `match` when several variants are part of the same decision.

Current rules:

- `Variant(field: pattern)` works for enum and tree-category variant `is` tests when the variant declares named payloads
- named payload patterns in `is` expressions and direct condition patterns may omit fields; omitted fields are ignored
- positional payload patterns still use the full variant arity
- positional and named payload patterns cannot be mixed in one variant pattern
- `expr is Variant as alias` binds `alias` as a narrowed variant view in the truthy branch
- ordinary `match` arms remain exhaustive for named payload patterns, so `match value: Type.Variant(field: x): ...` must still name every payload field

## Grouped `is` alternatives

Use `|` after `is` when a value can match any of several variants or enum members. Short checks stay inline.

```elisa
def is_additive(op: TokenKind) -> bool:
    return op is .PLUS | .MINUS
```

For longer variant families, wrap the alternatives in parentheses and put one alternative per line. This is the preferred shape for long declaration-family or AST-kind classifiers.

```elisa
def has_routine_body(decl: Pascal.Decl) -> bool:
    return decl is (
        Pascal.Decl.ProcedureDecl
        | Pascal.Decl.ProcedureQualifiedDecl
        | Pascal.Decl.ProcedureGenericDecl
        | Pascal.Decl.FunctionDecl
        | Pascal.Decl.FunctionQualifiedDecl
        | Pascal.Decl.FunctionGenericDecl
    )
```

The formatter preserves compact alternatives when they are short and rewrites longer groups into the parenthesized vertical form.

## Optional AST payloads

Optional value types can be used directly in structs and tree payloads, which is preferable to paired `has_*` booleans plus dummy sentinel values.

```elisa
struct SMLDatatypeConstructor:
    constructor_span: SMLSpan
    name_id: NameId
    payload_type: SMLType.Type?

tree SML:
    node Decl:
        Structure(name_id: NameId, signature_path?: SMLNamePath, decls: darray[Decl])
```

Constructors use the ordinary optional surface: pass the present value when it exists, or `null` when it does not. Consumers should use `if let` to unwrap the optional.

```elisa
if let payload_type = constructor.payload_type:
    use_payload(payload_type)
```

Optional values can also be passed through a transform only when present. This is the preferred spelling for optional AST payload checks that used to require adapter helpers.

```elisa
def check_expr(self: mutable State&, expr: Expr) -> void:
    pass

def check_format_arg(self: mutable State&, precision: Expr?) -> void:
    precision?.(self.check_expr)
```

When the present branch needs a real block, use an `if let` condition binding. The binding is available only in the truthy branch, and the bound value is the non-optional payload.

```elisa
def check_else_branch(self: mutable State&, else_stmt: Stmt?) -> void:
    if let stmt = else_stmt:
        self.check_stmt(stmt)
        self.record_reachable_branch(stmt.span)
```

`if let` also works for nullable references. In the then-branch the binding has the non-null reference type, so ordinary field access and member calls are allowed without repeating a null guard.

```elisa
struct Node:
    value: i64

def read(node: Node&?) -> i64:
    if let present = node:
        return present.value
    return 0
```

Prefer unified `else` recovery for nullable values and handled error unions. Plain `value else fallback` unwraps an optional or nullable reference and uses the fallback when absent. `else return`, `else raise`, and `else void` make the recovery action explicit.

```elisa
def required_name(name: NameId?) -> NameId error[LookupError]:
    return name else raise LookupError.MissingName

def first_or_default(value: Item?) -> Item:
    return value else Item.Default

def visit_if_present(value: Item?) -> void:
    value else void
```

For error unions, bare `try fallible()` still propagates the error. `try fallible() else ...` handles the error locally and produces the success value.

```elisa
def load_or_default(path: Path) -> Buffer:
    return try read_file(path) else Buffer.Empty

def load_with_log(path: Path) -> Buffer:
    return try read_file(path) else err:
        log_error(err)
        return Buffer.Empty
```

`return?`, `match?`, and `try? ... default` remain accepted as migration syntax, but they are deprecated in favor of the unified `else` forms.

## Membership Candidate Sets

The membership operator accepts list literals and brace candidate sets on its right-hand side. Brace sets are expression-local membership candidates, not standalone set values.

```elisa
def is_small(value: i64) -> bool:
    return value in {1, 2, 3}
```

Use `not in` for direct negated membership. It is equivalent to `not (value in {...})`, but keeps membership checks readable when the candidate set is the center of the expression.

```elisa
def is_large(value: i64) -> bool:
    return value not in {1, 2, 3}
```

Brace candidate sets also support integer-compatible ranges. `a..b` includes the upper bound, while `a..<b` excludes it.

```elisa
def is_digit(ch: i64) -> bool:
    return ch in {'0'..'9'}

def is_small_or_byte(value: i64) -> bool:
    return value in {0..<4, 16..255}
```

When the left-hand side has a const enum type, candidates may use shorthand enum members. The shorthand is resolved from the left-hand side type, so repeated enum qualifiers are unnecessary.

```elisa
const enum TokenKind of u32:
    IF
    LET
    IDENT

def starts_expr(kind: TokenKind) -> bool:
    return kind in {.IF, .LET}
```

Use the fully qualified form when the expected enum type is not obvious from context.

Range bounds may use const enum members too. When the left-hand side is a const enum, both range bounds may be written as shorthand (`.IF`) or as bare member names. The lowering compares the underlying integer storage, so adjacent enum members form an inclusive ordinal range.

```elisa
const enum TokenKind of u32:
    IF
    LET
    IDENT
    NUMBER
    STRING

def is_keyword(kind: TokenKind) -> bool:
    return kind in {.IF..LET}

def is_atom(kind: TokenKind) -> bool:
    return kind in {.IDENT..NUMBER, .STRING}
```

## Negated Type And Variant Tests

Use `is not` for direct negated type, state, variant, and structural pattern tests. It is equivalent to `not (value is Pattern)`, but reads better in guard and early-return code.

```elisa
def is_non_identifier(kind: TokenKind) -> bool:
    return kind is not .IDENT
```

## Pattern Alternatives

Nested match patterns can use `|` for no-binding alternatives. This is useful when several literal or payloadless variants should take the same path inside a larger structural pattern.

```elisa
enum Token:
    Ident
    Keyword
    Other

enum Expr:
    Leaf(kind: Token)

def is_named_leaf(expr: Expr) -> bool:
    match expr:
        Expr.Leaf(Token.Ident | Token.Keyword):
            return true
        _:
            return false
```

OR-pattern alternatives may bind names when every alternative binds the same names with compatible types.

```elisa
enum Token:
    Ident(value: i64)
    Keyword(value: i64)
    Other

enum Expr:
    Leaf(kind: Token)

def leaf_value(expr: Expr) -> i64:
    match expr:
        Expr.Leaf(Token.Ident(value) | Token.Keyword(value)):
            return value
        _:
            return 0
```

For expression-level branches, use the normal `value if condition else fallback` form. When the condition is a direct pattern test, its bindings are available in the true branch.

```elisa
def int_value(node: Expr) -> i64:
    return value if node is Expr.Int(value) else 0
```

For non-optional guard returns, use postfix `return if` when the returned value is the important part and the guard is a short condition.

```elisa
def fallback_type(type_expr: Type?, depth: usize) -> SymbolId:
    INVALID_SYMBOL_ID return if type_expr == null or depth > 32
    return resolve_type_identity(type_expr)
```

This lowers to an ordinary statement-form guard:

```elisa
if type_expr == null or depth > 32:
    return INVALID_SYMBOL_ID
```

Prefer the ordinary `if` form when the condition or returned expression needs multiple lines, diagnostics, mutation, or comments.

When an early optional result depends on several optional inputs, prefer multi-binding `if let`. Each binding unwraps an optional in order, and the branch runs only when all bindings are present.

```elisa
def in_range(lower: i64?, upper: i64?, value: i64?) -> bool?:
    if let lower_value = lower,
           upper_value = upper,
           value_int = value:
        return value_int >= lower_value and value_int <= upper_value
    return null
```

The same form works when the present branch performs diagnostics, mutation, or several statements.

```elisa
if let actual_lower = lower_value,
       actual_upper = upper_value:
    actual_lower > actual_upper then:
        record_diagnostic()
    return
```

This lowers to the same ordinary optional-bind ladder you would write by hand. A short condition can be kept as a statement block with `then:` when that makes the guard read naturally.

When the present branch immediately wants to match on the unwrapped value, combine `if let` with an ordinary `match`.

```elisa
def describe(maybe: Expr?) -> i64:
    if let expr = maybe:
        match expr:
            Expr.Int(value):
                return value
            _:
                return 0
    return -1
```

If the true branch contains another low-precedence operator, wrap it so the intended branch value is clear:

```elisa
return (left == right) if rhs is Value.Int(right) else false
```

Current rules:

- `if let name = value:` accepts value optionals such as `T?` and nullable references such as `T&?`
- `if let a = first, b = second:` runs only when every optional binding is present; bindings are evaluated left-to-right
- inside the then-branch, `name` has type `T` for value optionals and `T&` for nullable references
- `if let` composes with ordinary boolean conditions using `and`, so `if let value = maybe and value > 0:` is valid
- `condition then:` lowers to an ordinary `if condition:` statement block and is intended for short guard-style branches
- `value return if condition` returns `value` only when `condition` is true; otherwise execution continues with the next statement
- `value if expr is Pattern(bindings) else fallback` exposes the pattern bindings only in the true branch
- `value?.(fn)` unwraps `value` only when it is present and calls `fn(unwrapped_value)`
- the result is optional unless the transform returns `void`, in which case the whole expression is `void`
- the callable can be a plain function, an extension/UFCS-style member function such as `self.check_expr`, or another normal callable expression
- optional transform is for one-call argument-position transforms; prefer `if let` when the present case needs several statements or a named payload
- use `expr?.field` and `expr?.method(...)` for member access/calls on the optional payload itself

## Expect pattern binding

Use `expect let Pattern = value` when a test or helper wants to assert a shape and bind its payloads. It is the declarative form of the old `if value is Pattern(...): ... else: assert false` pyramid.

```elisa
def infix_op(expr: Perl.Expr) -> PerlInfixOp:
    can Abort.Panic:
        expect let Perl.Expr.Infix(op, _, _) = expr
        return op
```

The older `expect value as Pattern` spelling remains valid. The `expect let` spelling is often easier to scan when the important thing is the expected shape first and the source value second.

```elisa
expect let Pascal.Decl.TypeDecl(_, PascalType.Type.Name(type_name_id)) = block.decls[0]
assert type_name_id != NAME_TABLE_INVALID_ID
```

Tests can also match whole sequence and tree/struct shapes directly. This keeps common AST assertions declarative without hiding the raw tree constructors.

```elisa
expect block.stmts as [
    Pascal.Stmt.Assign(_, _),
    Pascal.Stmt.IfStmt(_, _, _),
]

expect ast.root as Pascal.Decl.Program(_, _, {
    stmts: [
        Pascal.Stmt.Assign(_, _),
        Pascal.Stmt.IfStmt(_, _, _),
    ],
})
```

Anonymous brace patterns such as `{stmts: [...]}` mean “match these fields on the value whose type is already known”. Use a named brace pattern such as `Block{stmts: [...]}` when spelling the concrete struct type improves locality.

List patterns are exact by default. A final `...` makes the pattern a prefix check:

```elisa
expect block.stmts as [
    Pascal.Stmt.StandardRoutine(^PascalStandardRoutineKind.NEW, _),
    Pascal.Stmt.StandardRoutine(^PascalStandardRoutineKind.DISPOSE, _),
    ...,
]
```

For checks that do not need payload bindings, ordinary assertion conditions can use the existing `is` pattern test directly:

```elisa
assert node is Expr.Int(_)
```

Current rules:

- `expect let Pattern = value` lowers to the same expectation node as `expect value as Pattern`
- blockless `expect let` can bind payload names for following statements
- `expect value as [a, b]` checks sequence shape and length
- `expect value as [a, b, ...]` checks a sequence prefix
- `expect value as {field: pattern}` matches fields on a known struct/tree-block value
- `expect let Pattern = value:` keeps the existing block form and lowers to a match with a panic fallback
- failed expectations panic through the same `Abort.Panic` path as existing `expect ... as ...`
- use `if let name = optional:` for optional unwrapping; `expect let` is for pattern matching over concrete values

## Typed string literal coercion

String literals coerce contextually into the common string carrier forms, so call sites and local declarations no longer need explicit casts just to satisfy `u8&`, `cstr`, or `sview`.

```elisa
extern puts(text: u8&) -> int
extern take_cstr(text: cstr) -> void
extern take_view(text: sview) -> void

def use() -> void:
    raw: u8& = "hello"
    text: cstr = "world"
    view: sview = "slice me"
    window: sview = sview(text, 0, 3)
    puts("ok")
    take_cstr("name")
    take_view("payload")
```

Current rules:

- contextual coercion applies when the expected type is `u8&`, `cstr`, or `sview`
- use `sview(value, start, end)` when constructing a non-owning view over an existing string/byte pointer
- ordinary low-level casts remain available for pointer reinterpretation and non-literal values
- prefer the plain literal in high-level code; keep `.cast[...]` when the expression is not a literal or the conversion is intentionally low-level

## Grammar match terms

Grammar productions can use block-form `match` as a dispatch term when choosing the next parser branch from an expression such as the current token kind.

```elisa
grammar PascalTypeGrammar over Token using ParserState:
    type_ref() -> PascalType.Type:
        type_expr = match state.current_token().kind:
            TokenKind.CARET: pointer_type_ref()
            TokenKind.PACKED: compact_type_ref()
            TokenKind.SET: set_type_ref()
            TokenKind.INDEX | TokenKind.OPERATOR: keyword_named_type_ref()
            _: range_type_ref()
        return type_expr
```

Current rules:

- this is grammar syntax, not a general statement inside a grammar body
- each arm maps one or more simple value patterns to a grammar term
- a wildcard `_` arm is required so dispatch remains explicit
- lowering desugars the grammar match into existing `when(...)` machinery, so the feature is readable surface sugar over the same low-level parser path
- use this for parser dispatch tables; keep ordinary `match` statements for runtime control flow

## Reusable token sets

Ordinary source can name static token-kind sets and use them with the membership operator. This keeps parser support helpers from growing long repeated `kind == A or kind == B` chains.

```elisa
const enum TokenKind of u32:
    IF
    CASE
    FN
    LET
    LPAREN
    PLUS
    MINUS
    STAR
    IDENT
    INTEGER
    STRING

tokenset ExprStart: TokenKind = [
    IF,
    CASE,
    FN,
    LET,
    LPAREN,
    IDENT,
    INTEGER,
    STRING,
]

def is_expr_start(kind: TokenKind) -> bool:
    return kind in ExprStart

def is_small_operator(kind: TokenKind) -> bool:
    return kind in {TokenKind.PLUS, TokenKind.MINUS, TokenKind.STAR}
```

Current rules:

- `tokenset Name = [...]` declares an immutable static list of membership candidates
- `tokenset Name: TokenKind = [...]` declares the element type explicitly
- bare members inside a typed token set are resolved against the element type, so `IF` means `TokenKind.IF`
- the right-hand side must be a list literal
- `value in Name` lowers through the same membership path as `value in [...]`
- `value in {a, b, c}` is the preferred inline form for short fixed membership checks; it lowers to the same equality chain and the brace literal is only valid as the right-hand side of `in`
- this is intended for token classifiers and other small static enum sets; use ordinary arrays when the set is runtime data

## Enum mapping tables

Use `enum map` for small total mappings from one enum-like type to another. This keeps token-to-AST/operator conversion tables declarative while lowering to an ordinary function.

```elisa
enum map binary_op: TokenKind -> PascalBinaryOp:
    STAR => MUL
    SLASH => DIV
    DIV => DIV
    MOD => MOD
    EQ => EQ
    NOTEQ => NOTEQ
    _ => ADD
```

This is equivalent to a generated function:

```elisa
def binary_op(value: TokenKind) -> PascalBinaryOp:
    if value == TokenKind.STAR:
        return PascalBinaryOp.MUL
    if value == TokenKind.SLASH:
        return PascalBinaryOp.DIV
    ...
    return PascalBinaryOp.ADD
```

Current rules:

- `enum map name: SourceEnum -> TargetEnum:` declares a function named `name`
- each non-default arm compares the input `value` against a source enum member and returns a target enum member
- bare arm members are qualified by the declared source or target type, so `STAR => MUL` means `TokenKind.STAR => PascalBinaryOp.MUL`
- fully qualified members are also accepted when the short form would be unclear
- a wildcard `_ => ...` arm is required
- use this for dense classification maps; keep ordinary `match` or `if` when the mapping does extra computation

## Query predicates

Use query predicates for compact scans over iterable data when the loop only computes existence, universality, the first matching item, or a count.

```elisa
def has_name(names: darray[NameId], wanted: NameId) -> bool:
    return any name in names where name == wanted

def all_nonzero(values: darray[i64]) -> bool:
    return all value in values where value != 0

def first_positive(values: darray[i64]) -> i64?:
    return first value in values where value > 0

def first_enabled_name(entries: darray[Entry]) -> NameId?:
    return entry.name_id for first entry in entries where entry.enabled

def enabled_names(owner: Arena, entries: darray[Entry]) -> darray[NameId]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return entry.name_id for each entry in entries where entry.enabled with alloc

def positive_int_payloads(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return value for each item in items where Expr.Int(value): value > 0 with alloc

def all_names(owner: Arena, entries: darray[Entry]) -> darray[NameId]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return entry.name_id for each entry in entries with alloc

def positive_count(values: darray[i64]) -> usize:
    return count value in values where value > 0
```

Current rules:

- `any name in source where predicate` returns `bool`
- `all name in source where predicate` returns `bool`
- `first name in source where predicate` returns the element as `T?`
- `projection for first name in source where predicate` returns the projected value as `U?`; the `where` clause may be omitted to project the first element
- `projection for each name in source where predicate with owner` returns projected values as `darray[U]`; the `where` clause may be omitted for pure maps, and `with owner` can replace an enclosing `in <arena>:` scope
- pattern filters may add a guard after `:`, as in `where Expr.Int(value): value > 0`; this works for query expressions and iterable `for` loops
- `count name in source where predicate` returns `usize`
- the source uses ordinary iterable expression lowering, such as arrays, dynamic arrays, views, strings, `rows()`, `source.enumerate()`, and tree child views
- range-loop headers such as `0..<n` and special `rev(...)` loop syntax remain explicit-loop territory for now
- the predicate is analyzed in a scope where the loop name is bound to the iterable element type
- pattern-bound names are scoped to the query projection, filter guard, or loop body; they do not leak after the query or loop
- use explicit loops when the body has side effects or needs multiple statements

Iterable `for` loops use the same filter clause after composed sources, so tuple destructuring from `enumerate()` is available in the filter:

```elisa
for index, item in items.enumerate() where item.kind == TokenKind.IDENT:
    result.push(item.name_id)
```

Prefer this inline `where` clause when the predicate is local to the loop. For expression-level boolean queries, use the same regular filter shape:

```elisa
return any item in items where item.kind == TokenKind.IDENT
return all value in values where is_enabled(value)
```

Keep `source.where(predicate)` for reusable view composition that feeds another view helper.

Expression-level filtered views can use explicit binders:

```elisa
return all((items where item: item.kind == TokenKind.IDENT))
return (items where item: item.kind == TokenKind.IDENT).reduce_sum(score_item)
return (items.enumerate() where index, item: index > 0 and item.kind == TokenKind.IDENT).reduce_sum(score_pair)
```

They can also use the same variant and struct pattern filters as loop headers. A
plain pattern keeps matching values; a pattern plus `:` predicate binds payload
names inside the predicate:

```elisa
return (exprs where Expr.Int(value)).reduce_sum(score_expr)
return (exprs where Expr.Int(value): value > 0).reduce_sum(score_expr)
```

The parenthesized form is useful when immediately calling another helper on the filtered view.

## Proof-carrying view helpers

View helpers can be written either as ordinary free functions or as receiver-style calls. The receiver form is syntax sugar: the compiler rewrites the receiver as the first helper argument, so the same optimization facts and lowering paths are preserved.

```elisa
def sum_selected(values: dview[i32]) -> i32:
    source: dview[i32] = values.readonly()
    return (source where value: keep_positive(value)).reduce_sum(identity)

def check(values: bool[8]) -> bool:
    return all value in values where is_enabled(value)

def chunks(values: dview[i32]) -> ChunksExactView[i32]:
    return values.readonly().chunks_exact(4)

def halves(values: dview[i32]) -> SplitView[i32]:
    return values.split_at(8)
```

Current receiver helpers:

- `source.where(predicate)` and `where(source, predicate)`
- `source.enumerate()` and `enumerate(source)`
- `source.any()` / `source.all()` and `any(source)` / `all(source)`
- `source.readonly()` and `readonly(source)`
- `source.split_at(index)` and `split_at(source, index)`
- `source.chunks_exact(width)` and `chunks_exact(source, width)`
- `source.reduce_sum(callback)` and `reduce_sum(source, callback)`

Actual fields and declared methods still win over helper rewriting, so this surface remains compatible with ordinary member access.

## Data-Oriented Helpers

Elisa Core now has a small set of built-in data-oriented containers and layout surfaces used heavily by the Pascal, Lua, and ATPL implementations.

```elisa
const enum RoutineDirective:
    External
    Forward
    CDecl
    VarArgs

directives: mutable Flags[RoutineDirective] = flags.new()
directives.add(RoutineDirective.External)

if directives[RoutineDirective.External]:
    mark_imported()
```

```elisa
params: mutable InlineVec[PascalParamSpec, 8] = inline_vec.new()
params.push(param)
```

```elisa
builder: mutable DArrayBuilder[Pascal.Decl] = owner.builder()
builder.push(decl)
decls: darray[Pascal.Decl] = builder.finish()

for decl in decls.view():
    visit_decl(decl)
```

```elisa
symbols: SymbolTable[NameId, PascalSymbol] = symtab.new(owner)
id: SymbolId = symbols.declare(name_id, PascalSymbol{...})
symbol: PascalSymbol? = symbols.lookup(name_id)

for entry in symbols.entry_view():
    visit(entry.key, entry.value)
```

```elisa
name_id: NameId = names.intern_ci("forward")
forward_id: NameId = names.intern_ci("forward")

if name_table_eq(name_id, forward_id):
    mark_forward()

if name_id in {names.intern_ci("read"), names.intern_ci("write"), names.intern_ci("reset")}:
    mark_standard_io()

if name_id.valid():
    text: sview = name_id.text(names)
```

```elisa
layout soa struct PascalSymbols:
    name_id: NameId
    value_type: PascalSemanticValueType
    flags: Flags[PascalSymbolFlag]

id: RowId[PascalSymbols] = symbols.push(...)
symbols.flags[id].add(PascalSymbolFlag.Routine)
```

Use `Flags[T]` for typed sets of const-enum values that grow or flow through APIs. Use struct-local `bitset` groups when the flags are fixed fields of one storage object.

Use `owner.builder()` when constructing arena-owned dynamic arrays. The expected builder type supplies the element type, which keeps parser and semantic builders close to the data they are assembling.

Use `NameId` for compact storage in ASTs and symbol tables. Prefer `name_id.valid()`, `name_id.invalid()`, and `name_id.text(names)` for ordinary checks. For hot keyword/builtin checks, intern the expected words once and compare ids with `name_table_eq(name_id, cached_id)` rather than repeatedly comparing text. Use `name_id in {cached_a, cached_b, cached_c}` instead of long `or` chains when checking a small fixed set of cached ids. Use `InternedName` as a short-lived view when code needs to carry both the table and id together; it can be explicitly cast back to `NameId`.

## Bit-Level Storage And Layout Modes

Narrow integers, `bitset`, `bitfield`, and explicit struct layout modes make compact representation visible in source.

```elisa
const enum Mode of u4:
    None
    Read
    Write

struct Header:
    flags: bitset:
        has_payload
        is_exported

    layout: bitfield:
        mode: Mode
        arity: u3
        active: u1

struct PackedHeader layout packed:
    tag: u4
    arity: u3
    active: u1

struct CHeader layout c:
    kind: u32
    flags: u32
    size: usize
```

Packed members are storage-level fields, not independently addressable values. Runtime narrowing into `uN` / `iN` storage should be explicit; compile-time overflow is rejected.

## Layout Introspection

Use layout introspection when code needs target-aware size, alignment, or field-offset facts without hand-maintaining numeric constants.

```elisa
struct Header layout c:
    tag: u8
    count: u32
    payload: u64

header_size: usize = size_of(Header)
header_align: usize = align_of(Header)
count_offset: usize = offset_of(Header, count)

same_size: usize = size_of[Header]()
same_align: usize = align_of[Header]
same_offset: usize = offset_of[Header](.count)
```

Current rules:

- `size_of(T)`, `align_of(T)`, and `offset_of(T, field)` return `usize`
- `size_of[T]()`, `align_of[T]`, and `offset_of[T](.field)` are the generic-style spellings and are preferred in new code
- results are computed from the backend target data layout
- `offset_of` currently accepts a direct field name on a lowered struct-like type
- legacy spellings `sizeof`, `alignof`, and `offsetof` are still accepted with deprecation diagnostics, but new code should use the underscore forms
- prefer these builtins in runtime, FFI, packed-layout, and backend test code instead of duplicating ABI constants by hand

## Static Assertions

Use `static assert` when an invariant must be checked while compiling. Constant-only assertions are checked by semantic analysis; target-layout assertions are checked by the backend so they use the selected target data layout.

```elisa
static assert size_of[Header]() == 16, "Header ABI changed"
static assert offset_of[Header](.payload) == 8

def keep() -> void:
    static assert 5 > 3

    static:
        assert align_of[Header] == 8
        if size_of[Header]() == 16:
            assert offset_of[Header](.payload) == 8
```

Current rules:

- `static assert` is valid at top level and inside statement blocks
- `static:` blocks group static-only statements inside runtime functions; inside the block, `assert`, `if` / `elif` / `else`, and `error(...)` are static by context
- `static def` declares a compile-time-only function; static assertions and static expression statements may call simple static functions with compile-time locals, assignments, conditionals, bounded loops, named arguments, defaults, and direct structurally decreasing recursion, while runtime calls are rejected
- static functions must return on all paths; direct recursive static calls must visibly decrease a parameter with `parameter - positive_constant` and have a visible lower-bound base case such as `if n <= 0: return ...`; indirect recursive static cycles are rejected for now, and the evaluator still keeps a depth limit as a backstop
- the condition must type-check as `bool`
- if the condition is a semantic compile-time constant, a false value is reported before backend lowering
- target-aware layout intrinsics are accepted in static assertions and checked during backend lowering
- the optional message must be a compile-time string to appear in the diagnostic

## Grammar recovery policies

Grammars can name reusable recovery policies once and apply them on productions or individual terms.

```elisa
grammar PascalStmtGrammar over Token using ParserState:
    cursor state

    recovery StatementRecovery:
        message ParseMessageKey.ExpectedStatement
        until .SEMICOLON, .END, token(TokenKind.EOF)
        fallback zeroed as Pascal.Stmt

    statement() -> Pascal.Stmt recover StatementRecovery:
        stmt = statement_core() recover StatementRecovery
        return stmt
```

Current rules:

- `recovery Name:` declares a grammar-scoped recovery policy
- `message ...` sets the diagnostic reported through the grammar's configured `record_error` hook
- `until ...` uses the same stop-term surface as inline `until(...)`, but block declarations accept the readable comma-separated form without parentheses
- `fallback ...` is optional and reuses ordinary expression syntax
- `recover Name` works anywhere inline `recover(...)` already works, including production headers and term suffixes
- lowering resolves named policies back into the existing inline recovery machinery, so they are purely compile-time grammar sugar

## Grammar token sets

Grammars can name reusable token/sync sets for repeated recovery and list boundaries.

```elisa
grammar PascalStmtGrammar over Token using ParserState:
    token:
        SEMICOLON ";"
        END "end"
        ELSE "else"

    tokenset StatementSync:
        SEMICOLON
        END
        ELSE

    tokenset StatementOrFileSync:
        StatementSync
        token(TokenKind.EOF)

    recovery StatementRecovery:
        message ParseMessageKey.ExpectedStatement
        until StatementOrFileSync

    block() -> darray[Pascal.Stmt]:
        statements = separated statement() by .SEMICOLON until(StatementOrFileSync)
        return statements
```

Generated start sets can also be referenced with `first(production_name)`. Lowering computes the reachable first tokens for that production after grammar normalization, so a tokenset or recovery clause can stay tied to the actual production entry surface instead of manually duplicating its starter tokens.

```elisa
grammar PascalStmtGrammar over Token using ParserState:
    token:
        BEGIN "begin"
        END "end"
        IDENT

    tokenset StatementStart = first(statement)
    tokenset StatementOrEnd:
        StatementStart
        END

    block() -> darray[Pascal.Stmt]:
        lookahead(StatementStart)
        statements = separated statement() by .END until(StatementOrEnd)
        return statements
```

The same `first(...)` form also works directly in grammar-term position when you want a one-off predictive probe without introducing a named token set first.

```elisa
block() -> Pascal.Stmt:
    lookahead(first(statement))
    statements = separated statement() by .END until(StatementOrEnd)
    return zeroed as Pascal.Stmt
```

Shared helper grammars can define common sync fragments once and importing grammars can compose them into local sets or use them directly in lookahead choices.

```elisa
grammar PascalListGrammar over Token using ParserState:
    token:
        EOF

    tokenset FileEndSync:
        EOF

grammar PascalExprGrammar over Token using ParserState uses PascalListGrammar:
    tokenset RParenSync:
        RPAREN
        FileEndSync

    atom() -> Pascal.Expr:
        lookahead(.LPAREN | FileEndSync)
        return zeroed as Pascal.Expr
```

Current rules:

- `tokenset Name:` declares a grammar-scoped set of stop terms
- token set items can use bare token-kind names, other token-set names, `.TOKEN` terms, string token terms, or explicit `token(TokenKind.X)` matchers
- `first(production_name)` is allowed anywhere a token-set item or `until(...)` stop term is allowed, and lowers to the production's reachable start-token choices
- `first(production_name)` is also allowed in ordinary grammar-term position, which is most useful for one-off `lookahead(first(...))` probes
- `tokenset Name = A, B, token(TokenKind.EOF)` is also accepted for compact one-line sets
- bare `Name` inside `until(...)` or recovery `until Name` is parsed as a token-set reference
- `lookahead(Name)` can reference a token set and lowers as lookahead over a choice of the set terms
- token sets from grammars listed in `uses` are available to the using grammar, matching recovery policies and infix tables; imported set names resolve before bare token-kind fallback
- token-set names can appear in ordinary grammar-term choices, such as `lookahead(.LPAREN | FileEndSync)`
- lowering expands token-set references into the existing explicit token checks, so there is no runtime token-set object
- prefer token sets for recurring parser sync concepts such as `StatementSync`, `BlockEndSync`, `DeclSync`, and `ExprEndSync`

## Grammar token families

When a reusable token union describes a grammar-domain atom rather than a recovery or synchronization boundary, use `token family`. Token families lower through the same token-set expansion machinery, but make the intent at the declaration site clearer.

```elisa
grammar SMLExprGrammar over Token using ParserState:
    token:
        IDENT
        CONSTR_IDENT
        SYMBOL_IDENT
        PLUS "+"
        MINUS "-"
        EQ "="

    token family OperatorName:
        IDENT
        CONSTR_IDENT
        SYMBOL_IDENT
        PLUS
        MINUS
        EQ

    op_name() -> Token:
        token = required(OperatorName, ParseMessageKey.ExpectedOperatorName)
        return token
```

Current rules:

- `token family Name:` declares a grammar-scoped reusable token union
- `token family Name = A | B | C` uses the same compact one-line form as `tokenset`
- token family items accept the same item forms as `tokenset`, including bare token kinds and references to other token sets or families
- token families are imported through `uses`, just like token sets, recovery policies, grammar aliases, and infix tables
- using a token family in grammar-term position, `required(...)`, `lookahead(...)`, or `until(...)` expands to an ordinary choice of token checks
- prefer token families for recurring semantic token domains such as `OperatorName`, `TypeNameStart`, or `PatternAtomStart`; keep `tokenset` for synchronization and list-stop boundaries

## Interleaved grammar support declarations

Grammar support declarations can be colocated with the productions that use them. This keeps local aliases, token sets, channels, recovery policies, infix tables, and token literal declarations near the parser surface they support instead of forcing every helper to the top of the grammar block.

```elisa
grammar PascalTypeDeclGrammar over Token using ParserState:
    type_decl_name() -> Token:
        name = required(.IDENT, ParseMessageKey.ExpectedTypeName)
        required(.EQ, ParseMessageKey.ExpectedTypeEquals)
        return name

    token:
        LPAREN "("
        RPAREN ")"
        COMMA ","

    tokenset EnumEndSync:
        RPAREN
        token(TokenKind.EOF)

    grammar alias enum_member_names = required(.IDENT, ParseMessageKey.ExpectedDeclName) |> separated_by(stop: EnumEndSync)

    enum_type_decl(name: Token) -> Pascal.Decl:
        .LPAREN
        members = enum_member_names
        close = required(.RPAREN, ParseMessageKey.ExpectedRightParen)
        return build_enum_type_decl(name, members, close)
```

Current rules:

- support declarations accepted between productions include `token`, `channel`, `tokenset`, `grammar alias`, `grammar type`, `recovery`, and `infix table`
- grammar environment wiring such as `cursor`, `alloc`, `token_kind`, `current`, `advance`, `expect`, and `record_error` still belongs at the top of the grammar block before productions
- support declarations remain grammar-scoped regardless of where they appear, so a later alias can be referenced by an earlier production after lowering sees the complete grammar block
- formatting preserves the declaration order you wrote; it does not hoist colocated helpers back to the top

## Grammar Constructors

Grammar constructors are compile-time grammar-term templates. They let a grammar name a reusable parser shape and pass grammar terms or token-set references into it.

```elisa
grammar PascalListGrammar over Token using ParserState:
    token:
        COMMA ","

    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)

grammar PascalArgsGrammar over Token using ParserState uses PascalListGrammar:
    token:
        RPAREN ")"

    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)

    args() -> darray[Pascal.Expr]:
        values = separated_by(item: expression(), stop: RParenSync)
        return values
```

The same call can be written as a compile-time grammar pipeline when the first argument is the thing being transformed:

```elisa
grammar PascalArgsGrammar over Token using ParserState uses PascalListGrammar:
    args() -> darray[Pascal.Expr]:
        values = expression() |> separated_by(stop: RParenSync)
        return values
```

Aliases can be parameterized when the call site needs a domain name but still wants to supply local grammar fragments, token sets, or expression values:

```elisa
grammar PascalArgsGrammar over Token using ParserState uses PascalListGrammar:
    token:
        COMMA ","
        RPAREN ")"

    tokenset RParenSync:
        RPAREN
        token(TokenKind.EOF)

    grammar alias expr_items(stop: tokenset, sep: grammar = .COMMA):
        expression() |> separated_by(stop: stop, sep: sep)

    args() -> darray[Pascal.Expr]:
        values = expr_items(stop: RParenSync)
        return values
```

They can also accept expression parameters for the bits that should stay ordinary Elisa core code, such as diagnostics and fallback AST construction:

```elisa
grammar RecoveryGrammar over Token using ParserState:
    grammar type recovered[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:
        item recover(message, until(stop), fallback)

grammar PascalStmtGrammar over Token using ParserState uses RecoveryGrammar:
    condition_or_invalid() -> Pascal.Expr:
        node <- recovered(
            item: condition(),
            message: expr(ParseMessageKey.ExpectedConditionExpression),
            stop: ConditionSync,
            fallback: expr(invalid_expr_at(state.current_token().span))
        )
        return node
```

Current rules:

- `grammar type Name(param, ...):` declares a grammar-scoped compile-time template
- `grammar type Name[T](item: grammar -> T, stop: tokenset) -> grammar -> darray[T]:` is the typed signature form
- `grammarfn` remains accepted as compatibility input for older examples, but new code should use `grammar type`
- parameters can declare grammar-term defaults, as in `sep: grammar = .COMMA`
- expression parameters use `expr` in the signature and are passed with `expr(...)` at the call site
- the body is ordinary grammar syntax; one term expands as that term, multiple terms expand as a `seq`
- `Name(item: expression(), stop: RParenSync)` is the preferred direct call style once a helper has named arguments
- `item |> Name(args...)` is pipeline sugar for passing `item` as the first positional grammar argument, which is useful for combinator-like helpers such as list builders and recovery wrappers
- `apply Name(arg, ...)` remains the explicit lower-level spelling, and is useful for positional experiments or when you want to emphasize compile-time expansion
- positional and named arguments can be mixed only before the first named argument; missing parameters use defaults when present
- `grammar alias name(params...) = term` and block-form `grammar alias name(params...):` use the same parameter, default, and argument rules as grammar functions, but are intended for domain-named fragments and partial specializations rather than general constructors
- arguments are grammar terms, so they can be productions, token terms, required/recoverable terms, lists, `seq`, or token-set references
- typed parameters currently support `grammar`, `grammar -> T`, `tokenset`, and `expr`
- the parser reports same-grammar bad calls such as unknown helpers, missing required arguments, unknown named arguments, duplicate named arguments, too many positional arguments, passing a token set where a grammar term is expected, passing a grammar term where a token set is expected, or passing a grammar term where an expression is expected
- bare parameter names in grammar-term position are replaced by the matching argument
- bare parameter names in `until(...)` position are also replaced, which makes token-set parameters natural
- bare expression parameter names are replaced in grammar expression slots such as recovery messages, fallback values, required/delimited messages, `when` conditions, and precedence arm results
- grammar functions from grammars listed in `uses` are available to the using grammar, matching token sets, recovery policies, and infix tables
- grammars may be helper-only libraries with tokens, token sets, recovery policies, grammar functions, and infix tables but no productions
- expansion happens before token aliases, token sets, recovery policies, and infix tables resolve, so grammar functions compose with those features instead of becoming a runtime feature

## Default arguments, named calls, and `..` forwarding

Default values are supported on trailing parameters of ordinary functions and `extern` declarations.

```elisa
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

```elisa
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

```elisa
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

```elisa
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

```elisa
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

def build(left: i64) -> i64:
    args LocalPair:
        left: i64 = left
        right: i64 = 9
    return add(use LocalPair)
```

Implicit bundle values have two call-site surfaces:

```elisa
with ParseCtx(.., alloc = override_alloc):
    return inner()

return inner() with ParseCtx(.., alloc = override_alloc)
```

Current rules:

- `bundle Name implicit:` declares an ambient dependency bundle
- `bundle Name explicit:` declares a reusable top-level explicit argument pack
- `args Name:` declares a local compile-time-only named argument pack inside a block
- legacy `context Name:` and top-level `params Name:` still parse, but the formatter emits canonical `bundle` declarations; top-level `params Name:` is deprecated in favor of `bundle Name explicit:`
- legacy local `params Name:`, `parameters Name:`, and `bundle Name explicit:` still parse with deprecation diagnostics; use `args Name:`
- `def f(...) with Name -> T` makes implicit bundle fields visible by field name inside the function body
- `def f(use Name)` expands an explicit bundle into the function's explicit parameter set
- `call(use Name(...), other: ...)` applies an explicit bundle at a call site
- `call(use Name)` is the canonical empty bundle application; legacy `call(use Name())` still parses with a deprecation diagnostic
- `with args(...)` installs ambient explicit arguments for nested calls inside a block
- calls auto-forward when the caller already has the same implicit context in scope
- `with Name(..)` spreads same-named ambient values into the bundle, and explicit overrides win over the spread values
- calls with implicit parameters also fall back to same-named in-scope values, which lets generated parser functions with an `alloc` parameter call helpers declared `with AllocCtx` without spelling the allocator repeatedly
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
- local explicit bundles are visible only within the block that declares them, which makes them useful for parser/compiler helper packs without leaking names across the whole file

## Brace destructuring, field punning, and record updates

Brace forms now work consistently across destructuring, literal construction, `is` patterns, `match` patterns, and record updates.

```elisa
struct Row:
    left: int
    right: int
    flag: bool

def run(row: Row, flag: bool) -> int:
    let {left: first, right} = row
    built: Row = Row{left: first, right, flag}
    rebuilt: Row = Row{...built, left: current}
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
- `Type{...base, field: expr}` starts a brace struct literal from an existing value and overrides named fields; this is useful for default packs and small immutable updates
- local `args name:` blocks declare compile-time named argument packs; spread them with `Type{...name}` to split large constructor or struct-literal argument lists into reusable groups
- spreading two local argument packs that provide the same field is a diagnostic; write an explicit named field after the spreads when you intentionally want to override one value
- `base{field, other = expr}` creates a record-update expression by copying `base` and replacing only the mentioned fields

```elisa
args name_ids:
    read_name_id: NameId? = next_read
    write_name_id: NameId? = next_write

args expressions:
    index_expr: Expr? = next_index
    stored_expr: Expr? = null

return Accessors{...current, ...name_ids, default_enabled: true, ...expressions}
```

The same brace destructuring grammar also works for store-row values:

```elisa
for {name_key, depth} in pending.rows():
    total <- total + name_key + depth
```

## Tree exact updates and `rewrite ... default`

Exact tree members reuse the same brace-update surface as structs.

```elisa
tree Lua:
    common:
        span: i64
    @role(expr)
    node Expr:
        Int(value: i64)
        Binary(left: Expr, right: Expr)
    block Block:
        items: darray[Expr]

def rotate(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
    in perm:
        return node{left, right}

def rotate_into(owner: Arena, node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return new[alloc] node{left, right}

def make_binary(alloc: mutable Arena&, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
    return node[span = left.span + right.span] Lua.Expr.Binary(left: left, right: right)

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
- `node Tree.Member(...)` is canonical sugar for tree construction through an in-scope `alloc` binding
- `node[span = expr] Tree.Member(...)` injects the common `span` field without repeating it in the constructor arguments
- `node[alloc = owner, span = expr] Tree.Member(...)` is the explicit-owner form for parser helpers that name the arena something other than `alloc`
- in the fact-core model, `node[...]` is a `produce` transform: it creates a fresh tree value with representation, allocator/store, and common-field facts rather than a pure value expression
- `left.span + right.span` is the canonical span algebra form for span-like parser ranges; it first uses a visible `SpanLike` static-interface impl when present, then falls back to legacy helper functions such as `combine_span`
- inside an exact `rewrite` arm, `default` rebuilds the current exact member using the already rewritten child results
- `default` is contextual rather than a new global keyword; outside an exact `rewrite` arm it is rejected
- `default` also rebuilds `children` sequence fields, materializing fresh arrays in the active tree owner when needed

## Nested tree categories

Tree categories can be nested when a branch of the tree wants its own sparse set of exact shapes. A flat payload keeps the shape dense and horizontal:

```elisa
tree Lua:
    @role(expr)
    node Expr:
        Unary(expr: Expr)
        Binary(op: LuaOperationType, left: Expr, right: Expr)
```

Moving the binary operators into a nested category makes the vertical alternatives explicit:

```elisa
tree Lua:
    @role(expr)
    node Expr:
        Unary(expr: Expr)
        node Binary:
            Add(left: Lua.Expr, right: Lua.Expr)
            Sub(left: Lua.Expr, right: Lua.Expr)
            Div(left: Lua.Expr, right: Lua.Expr)
```

This declares `Lua.Expr.Binary` as a real tree category under `Lua.Expr`. Its exact variants are named `Lua.Expr.Binary.Add`, `Lua.Expr.Binary.Sub`, and so on. Values from the nested category are assignable to the parent category, so code can return a specific sparse shape through the broader dense handle:

```elisa
def make_add(alloc: mutable Arena&, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
    return node[alloc = alloc] Lua.Expr.Binary.Add(left: left, right: right)

def classify(node: Lua.Expr) -> i64:
    match node:
        Lua.Expr.Binary.Add(left: _, right: _):
            return 1
        _:
            return 0
```

Within a tree family, unqualified sibling names still resolve when they are unambiguous. Use qualified names such as `Lua.Expr` when two trees expose the same local node name, or when the code benefits from saying exactly which family owns the child. Structural `child` and `children` relations are inferred for tree-family payloads; keep explicit `link` for non-structural references that should not be traversed as owned children.

## Lexer DSL for mixed-mode frontends

The current lexer surface is aimed at handwritten frontends that want generated helpers for regular token tables without giving up manual cursor control for the irregular parts of a language.

```elisa
lexer PascalLex:
    token_kind PascalTokenKind
    tokens PascalTokenGrammar
    keyword_compare pascal_sview_eq_keyword
    keywords fallback IDENT:
        "unit" -> UNIT
        "begin" -> BEGIN
    literals longest fallback EOF:
        ":=" -> ASSIGN
        ";" -> SEMICOLON

def read_ident_or_keyword(self: mutable LexerState&) -> PascalToken:
    kind: PascalTokenKind = pascal_lex_keyword_kind(self.source[start_offset:self.offset])
    ...

def next_token(self: mutable LexerState&) -> PascalToken:
    literal_kind, literal_len = pascal_lex_match_literal(self.source, self.offset)
    ...
```

Current rules:

- prefer generated `keywords` tables for fixed reserved words; keep a thin manual wrapper when call sites or tests want a stable helper name such as `lua_lookup_keyword`
- prefer generated `literals` tables for fixed punctuation and operator tokens; use `longest` when longer literals must win over prefixes
- `tokens GrammarName` is the default way to import literal and keyword aliases from a shared grammar token manifest instead of restating them in the lexer
- `keyword_compare helper` is the escape hatch for regular-but-non-exact keyword surfaces such as case-insensitive matching
- generated token and keyword tables expose assertion helpers so tests can validate the whole table without restating every row:

```elisa
grammar PascalTokenGrammar:
    token_lookup token_kind_for_text
    token:
        PROGRAM "program"
        BEGIN "begin"
        ASSIGN ":="

@test
def pascal_keyword_lookup_matches_surface_tokens() -> void:
    can Abort.Panic:
        token_kind_for_text_assert_table()
        assert_eq(token_kind_for_text("PROGRAM"), TokenKind.PROGRAM)
        assert_eq(token_kind_for_text("?"), TokenKind.EOF)
```

For lexer-local keyword tables, the generated helper follows the lexer name:

```elisa
lexer PascalLex:
    keywords fallback IDENT:
        "begin" -> BEGIN

@test
def pascal_lexer_keyword_table_is_complete() -> void:
    can Abort.Panic:
        pascal_lex_assert_keyword_table()
```

- keep layout and comment skipping, string and char literal readers, modes, interpolation, directives and includes, numeric edge cases, and context-sensitive operators manual unless the surface is proven regular
- treat lexer support as mixed-mode: generated helpers own lookup tables, while handwritten code still owns cursor movement, state transitions, diagnostics, and context-sensitive decisions

## Grammar DSL for parsers and tree frontends

The current grammar surface is aimed at handwritten recursive-descent frontends that still want a compact parser DSL for the repetitive parts: tokens, recovery, lists, infix tables, precedence, postfix/suffix, and prefix forms.

```elisa
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
    infix table ExprTable(additive):
        atom = choice(
            prefix(.PLUS, .MINUS, .NOT) atom() -> make_unary_expr(op, operand),
            delimited(.LPAREN, expression(), .RPAREN, ParseMessageKey.ExpectedRightParen),
            name_atom(),
            integer_atom(),
            string_atom()
        )
        multiplicative(left = atom()):
            op = .STAR | .SLASH | .DIV | .MOD | .AND right = atom() -> make_binary_expr(left, op, right)
        additive(left = multiplicative()):
            op = .PLUS | .MINUS | .OR right = multiplicative() -> make_binary_expr(left, op, right)
    expression() -> Pascal.Expr:
        result = infix(ExprTable)
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

- `grammarenv Name over Token using ParserState:` declares reusable parser-environment defaults for grammars that share the same state/token surface
- `grammar Name with EnvName:` applies those defaults while still allowing local grammar headers to override individual fields
- `cursor state` tells lowering which parser-state value owns the current cursor
- `alloc alloc` supplies the active tree/arena owner expression used by generated parser helpers
- `token_kind MyTokenKind` tells lowering which enum/type owns dotted token aliases such as `.IDENT`; it defaults to `TokenKind`
- `eof MyTokenKind.EOF` tells recovery loops which token-kind expression is EOF; it defaults to `TokenKind.EOF`
- `token_field kind` tells lowering which field on the token stores its kind/tag; it defaults to `kind`
- `current current_token` tells lowering which parser-state method returns the current token; it defaults to `current_token`
- `advance advance_token` tells lowering which parser-state method advances recovery loops; it defaults to `advance_token`
- `expect expect` tells lowering which parser-state method consumes a literal token; it defaults to `expect`
- `expect_kind expect_kind` tells lowering which parser-state method consumes a token kind; it defaults to `expect_kind`
- `record_error record_parse_error` tells recovery lowering where to report parse messages; it defaults to `record_parse_error`
- `token:` declares token aliases for use as `.IDENT` inside the grammar
- grouped token entries may be bare (`IDENT`) or dotted (`.IDENT`) and may include an optional literal such as `LPAREN "("`
- `channel name` in a grammar header declares a generated mutable channel shared by every production in that grammar
- `channel span: Span = $start.span + $end.span` declares a typed channel with a default expression
- `channel name` at the top of a production body declares a production-local channel, which is preferred for helper tuple/struct results
- `grammar type Name[...]` declares a reusable higher-order grammar combinator; this is the canonical replacement for older `grammarfn` declarations
- `grammar alias name = term`, `grammar alias name(params...) = term`, and their block forms give compile-time grammar terms reusable names, so call sites can say `args = call_args`, `args = expr_items(stop: RParenSync)`, or `statements = block_statement_items` while keeping the lower-level `separated_by(...)` or recovery shape available in the header
- `infix table Name(result):` hoists a reusable named-precedence ladder into grammar header scope so productions can say `result = infix(Name)` instead of inlining every level
- if a production falls through without an explicit `return` and its return type is either a named tuple or a known struct in the current scope, lowering synthesizes the success value from matching channel names
- struct-return synthesis only uses channels that correspond to struct fields; unrelated grammar-wide channels such as `node` are ignored instead of producing invalid helper struct literals
- if a nested `seq` arm ends by assigning a declared channel, the assignment is treated as channel state rather than the arm's semantic value; this keeps channel-synthesized helper productions from needing a trailing `pass`
- `expr[T](value)` gives an inline grammar expression term an explicit result type, which lets `seq`, `separated`, and related list combinators keep transformed element types without introducing a one-off helper production
- `singleton[T](value)` builds a one-item `darray[T]` inside grammar lowering, using the grammar allocator when one is configured
- `empty[T]` builds a typed empty `darray[T]` in grammar space, so fallback branches do not need to escape to `expr[darray[T]]([])`
- list comprehensions such as `[value for item in source]` and `[value for item in source if cond]` build a `darray` directly in grammar code, so inline list transforms and filter-style expansions do not need dedicated `maplist` or `flatmaplist` helpers
- postfix, suffix, and precedence arms can use an indented block form when bindings would otherwise get cramped on one line

```elisacore
grammarenv SMLGrammarEnv over SMLToken using SMLParserState:
    cursor state
    alloc alloc
    token_kind SMLTokenKind
    eof SMLTokenKind.EOF
    token_field kind
    current current_token
    advance advance_token
    expect expect
    expect_kind expect_kind
    record_error record_parse_error

grammar SMLExprGrammar with SMLGrammarEnv:
    token:
        IDENT
        INTEGER
    grammar type recovered_expr(stop: tokenset) -> grammar -> SML.Expr:
        expression() |> recovered(message: expr(ExpectedExpression), stop: stop, fallback: expr(invalid_expr_at(state.current_token().span)))

extend grammar PerlExprGrammar:
    postfix_expr() -> Perl.Expr:
        node <- postfix(left = primary_expr()):
            .ARROW:
                member = member_tail()
                -> make_perl_member_expr(left, member.name_token, member.close_token)

    expression() -> Perl.Expr:
        node <- precedence(left = term()):
            op = .PLUS:
                right = term()
                -> make_perl_infix_expr(left, op, right)
```
- `uses OtherGrammar` imports productions and grammar-scoped helper declarations from another grammar, including token aliases, recovery policies, and infix tables

Inside grammar sequence result positions, `+` is the canonical way to compose list-producing grammar values. Prefer it over tiny helper functions whose only job is to allocate, append the left list, append the right list, and return the merged result.

```elisa
const_prefixed_decl_sections() -> darray[Pascal.Decl]:
    node <- seq:
        const_decls = const_decl_section()
        type_decls = optional_type_decl_section()
        var_decls = optional_variable_decl_section()
        const_decls + type_decls + var_decls
    return node
```

This is grammar DSL list composition, not a promise that general-purpose `darray + darray` is available in arbitrary expression code. Drop to explicit `in alloc:` plus `.push` / `.extend` when ownership, allocation, or mutation needs to be controlled directly.

When branching on parser state, snapshot cursor-dependent values before multiple guarded branches if any branch could consume input. This keeps alternatives from accidentally observing a later cursor position.

```elisa
declarations() -> darray[Pascal.Decl]:
    kind = expr(state.current_token().kind)
    const_decls = when(kind == TokenKind.CONST, const_prefixed_decl_sections(), empty[Pascal.Decl])
    type_decls = when(kind == TokenKind.TYPE, type_prefixed_decl_sections(), empty[Pascal.Decl])
    var_decls = when(kind == TokenKind.VAR, variable_decl_section(), empty[Pascal.Decl])
    node <- const_decls + type_decls + var_decls
    return node
```

Channel synthesis is useful for parser result shapes that want several tracked fields without repeating the final assembly step. For local helper results, the lightest form is a named tuple:

```elisa
grammar PascalAssignStmtGrammar over Token using ParserState:
    cursor state
    channel name_id: NameId
    channel value: Pascal.Expr
    channel span: Span = $start.span + $end.span

    assignment_spec() -> (name_id: NameId, value: Pascal.Expr, span: Span):
        .IDENT(name_token)
        lookahead(.ASSIGN)
        cut
        name_id <- expr(name_token.lexeme_key)
        required(.ASSIGN, ParseMessageKey.ExpectedAssignmentOperator)
        value <- expression()
        pass
```

That lowers through the normal grammar try/public wrappers and finishes with a synthesized success value shaped like `(name_id, value, span)`, so callers can keep using field-style access such as `spec.name_id` and `spec.span` without introducing a dedicated helper struct.

Typed grammar expression terms are useful when a grammar wants to transform a parsed token into a richer value inline and keep that element type through a list combinator:

```elisa
grammar PascalProgramHeaderGrammar over Token using ParserState:
    cursor state

    program_header() -> (param_name_ids: darray[NameId]):
        param_name_ids <- delimited(
            .LPAREN,
            separated required(seq(.IDENT(param_token), expr[NameId](param_token.lexeme_key)), ParseMessageKey.ExpectedProgramParamName) by .COMMA until(.RPAREN, token(TokenKind.EOF)),
            .RPAREN,
            ParseMessageKey.ExpectedProgramHeaderRightParen
        )?
        pass
```

Without the `[NameId]` annotation, lowering only sees an untyped `expr(...)` term and list inference has to fall back to a helper production.

Singleton terms and list comprehensions cover the next common parser-helper shapes: wrap one parsed value in a list, transform each element from a parsed list, and filter out unwanted items without bouncing out to hand-written allocation helpers.

```elisa
grammar PascalFrontend over Token using ParserState:
    cursor state

    const_decl_group() -> darray[Pascal.Decl]:
        spec = const_decl_spec()
        node <- singleton[Pascal.Decl](build_const_decl(spec.name_token, spec.value))
        return node

    variable_decl_group() -> darray[Pascal.Decl]:
        header = variable_decl_header()
        node <- when(
            header.type_token.kind == TokenKind.IDENT,
            [
                node[span = name_token.span + header.type_token.span] Pascal.Decl.VarDecl(
                    name_id: name_token.lexeme_key,
                    type_name_id: header.type_token.lexeme_key
                )
                for name_token in header.names
                if name_token.kind == TokenKind.IDENT
            ],
            empty[Pascal.Decl]
        )
        return node
```

Use `empty` when a branch produces no list items. Use `singleton` when a production produces exactly one list item. Use a list comprehension when each source item yields at most one result value and an optional filter decides which items survive. When one source item truly needs to expand into multiple result values, compose list-valued branches explicitly with `+` or drop to a helper that makes the flattening step obvious.

When the result shape is shared more broadly, the same mechanism also works for structs:

```elisa
struct BuiltSummary:
    items: darray[i64]
    checksum_total: i64
    arg_count: usize
    close_span: Span

grammar ExprGrammar over Token using ParserState:
    cursor parser
    channel items: darray[i64] = []
    channel checksum_total: i64 = 0
    channel arg_count: usize = 0
    channel close_span: Span = $end.span

    arg_summary() -> BuiltSummary:
        pass
```

That lowers through the normal grammar try/public wrappers and finishes with a synthesized success value shaped like `BuiltSummary(items:, checksum_total:, arg_count:, close_span:)`.

`token:` is the canonical style for token aliases:

```elisa
token:
    IDENT
    INTEGER
    LPAREN "("
    RPAREN ")"
```

The older repeated declaration form still parses for compatibility, but the formatter normalizes aliases back into the grouped block form:

```elisa
token .IDENT
token .INTEGER
token .LPAREN "("
token .RPAREN ")"
```

### Core grammar terms

Grammar productions are ordinary named parser functions whose bodies contain grammar terms plus normal expressions.

```elisa
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

```elisa
op = .PLUS | .MINUS | .OR
atom = choice(.IDENT(token), .INTEGER(token), grouped_expr())
```

For larger alternatives, block `choice:` is the preferred readable form:

```elisa
node <- choice:
    seq:
        .IDENT(name_token)
        expr(build_name(name_token))
    seq:
        .INTEGER(int_token)
        expr(build_int(int_token))
```

Sequences use block form as the canonical style:

```elisa
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

```elisa
prefix(.PLUS, .MINUS, .NOT) atom() -> make_unary_expr(alloc, op, operand)
```

This desugars to the existing lower-level terms:

```elisa
seq:
    op = choice(.PLUS, .MINUS, .NOT)
    operand = atom()
    expr(make_unary_expr(alloc, op, operand))
```

The generated names are currently `op` and `operand`; use those in the result expression.

### Lists And Delimiters

The list-family helpers use readable DSL-style forms as the canonical style.

```elisa
statements = separated statement() by .SEMICOLON until(.END, token(TokenKind.EOF))
names = separated required(.IDENT, ParseMessageKey.ExpectedDeclName) by .COMMA until(.COLON, token(TokenKind.EOF))
decls = [variable_decl_group()] while token in tokens != [.BEGIN, token(TokenKind.EOF)]
args = delimited(.LPAREN, separated expression() by .COMMA until(.RPAREN, token(TokenKind.EOF)), .RPAREN, ParseMessageKey.ExpectedRightParen)?
maybe_name = .IDENT?
```

Current list-family terms:

- `term?` succeeds with an optional result
- `term?` is the canonical optional grammar spelling; `optional term` and `optional(term)` remain accepted for compatibility, but the formatter emits postfix `?`
- `repeat term until(...)` parses zero or more items and returns the collected list
- `[term] while token in tokens != [stop1, stop2]` is the canonical flattening form for zero or more list-producing items
- bracket `while` forms are the canonical flattening repeat spelling; `flatrepeat term until(...)` remains accepted for compatibility
- `separated term by sep until(...)` is the canonical separated-list form
- `list term separated by sep until(...)` and function-style `separated(term, sep, until(...))` remain accepted, but the formatter emits `separated term by sep until(...)`
- `delimited(open, body, close, MessageKey)` parses `open`, returns `body`, and requires `close`
- `until(...)` accepts token aliases, literal tokens, explicit `token(...)` terms, or other recoverable terms

### Infix, precedence, suffix, and postfix

Grammar-scoped infix tables are the preferred surface for reusable expression ladders. They keep the grammar header readable and let productions opt into the shared ladder with a single `infix(Name)` use.

```elisa
infix table ExprTable(additive):
    atom = choice(integer(), name(), grouped())
    left multiplicative(left = atom()):
        op = .STAR | .SLASH -> make_binary_expr(alloc, left, op, right)
    left additive(left = multiplicative()):
        op = .PLUS | .MINUS -> make_binary_expr(alloc, left, op, right)

expression() -> Pascal.Expr:
    result = infix(ExprTable)
    return result
```

Current infix/precedence rules:

- `infix table Name(result_level):` defines a reusable named-precedence ladder in grammar header scope
- `infix(Name)` expands through the existing precedence lowering machinery, so it is grammar sugar rather than a separate parser path
- `left`, `right`, and `nonassoc` may prefix looping levels to synthesize the common `right` operand automatically and control whether the level chains, recurses once, or stops after one match
- one-off inline ladders use the same prefixes as `precedence left(left = atom()): ...`, which keeps the reusable and one-off surfaces aligned
- the argument to `precedence(result_level)` names the level whose value is returned when you want an inline one-off ladder instead of a reusable table
- `level = seed` defines a seed/helper level
- `level(left = lower_level()): ...` defines a left-associative looping level
- each arm parses an operator term, optional right-side bindings, and a `-> result` expression
- arms are attempted in declaration order
- recursive calls to named levels are resolved inside the precedence block

For a standalone non-frontend example, the same surface works well for tiny DSLs and calculator-style grammars:

```elisa
grammar Arithmetic over Token using ParserState:
    cursor state

    infix table Expr(compare):
        atom = choice(integer_atom(), grouped_expr())
        right power(left = atom()):
            .CARET -> build_power(left, right)
        left additive(left = power()):
            op = .PLUS | .MINUS -> build_binary(left, op, right)
        nonassoc compare(left = additive()):
            op = .EQ | .LT | .GT -> build_compare(left, op, right)

    expression() -> Expr:
        result = infix(Expr)
        return result
```

Suffix and postfix are related loop surfaces for expression tails and statement-like continuations:

```elisa
condition() -> Pascal.Expr:
    node <- suffix(left = expression()):
        op = .EQ | .NOTEQ right = expression() -> make_binary_expr(alloc, left, op, right)
    return node
```

Use `suffix` when the seed value should be repeatedly transformed by arms. Use `postfix` for postfix-tail parsing where that spelling better communicates the grammar role.

### Lowering and inspection

The grammar DSL lowers into ordinary Elisa core functions. The compiler exposes inspection modes for debugging generated parser code:

```sh
go run ./src -emit lower path/to/file.elisa
go run ./src -emit grammar-lowered path/to/file.elisa
```

Current implementation notes:

- grammar sugar lowers to existing AST terms where possible, rather than introducing runtime parser objects
- `prefix(...)` currently lowers to `seq(op = choice(...), operand = ..., expr(...))`
- token aliases are rewritten before lowering, so `.IDENT` can map onto the real token kind expression
- the grammar header can now decouple token value and token-kind names, for example `grammar SMLExprGrammar over SMLToken using SMLParserState:` with `token_kind SMLTokenKind` and `eof SMLTokenKind.EOF`
- span algebra `left.span + right.span` resolves through a visible `protocol SpanLike` / `static interface SpanLike` impl when available, and still recognizes legacy helper functions such as `combine_span` or `lua_span_union` for compatibility
- recovery and required terms depend on the grammar `cursor` declaration to restore or advance parser state correctly
- tree AST construction remains ordinary Elisa core code, so teams can use canonical `node[span = ...] Tree.Node(...)` sugar or drop to low-level `new[alloc] Tree.Node(span: ..., ...)` when exact control is clearer

## Filtered iterable `for`

Iterable loops may now include an inline filter after the source expression.

```elisa
for {left, right: value} in items where left != 0:
    total <- total + value

for token in tokens where token.kind == TokenKind.IDENT:
    names.push(token.NameId())

for decl in block.decls where Pascal.Decl.LabelDecl(labels):
    validate_labels(labels)

for decl in block.decls where Pascal.Decl.LabelDecl:
    validate_label_decl(decl)

for decl in block.decls where Pascal.Decl.LabelDecl(labels) for label in labels:
    validate_label(label)
```

Current rules:

- the binder runs before the filter, so the filter may reference destructured names such as `left`
- the loop binder may be a simple name, an irrefutable brace destructure pattern, or a typed variant filter pattern
- the filter may be an ordinary boolean expression or a pattern predicate
- a variant pattern filter may omit payload parentheses when it only tests the variant kind and does not bind payload fields
- loop headers can chain another `for` clause to express a simple nested iteration without adding an extra indentation level
- this works over ordinary iterable sources and store-row iterators such as `rows()`

## `do:` expression blocks

`do:` introduces an expression-valued block with setup statements followed by a final value expression.

```elisa
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

```elisa
def read(xs: darray[int], i: usize) -> int:
    return xs[i] else 0
```

Current rules:

- `source[index] else fallback` keeps the ordinary indexing semantics on success and yields `fallback` on the miss path
- the fallback expression must match the indexed element type
- the fallback surface is for value reads only; it is not an assignment target or a ref-binding surface

## `defer` statements

The current compiler accepts two explicit defer modes.

```elisa
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

```elisa
store PendingGotoStore:
    name_key: u32
    depth: u32

def build(values: mutable dict[cstr[key_shape], i64]&, key: cstr[key_shape]) -> i64:
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
- the generic syntax parses for more than one key family, but the current runtime-backed helper surface is primarily validated for `dict[cstr[key_shape], V]` unless matching helper overloads are supplied
- packed and row store values should be read through the fact-core lens: mutable local stores carry store-dependency facts, `freeze(move store)` consumes the local store and rebases handles onto frozen-store facts, and row scans may add optimization facts such as readonly, contiguous, or exact extent

## Pool scopes and `parallel for`

The current explicit parallel loop surface is pool-scoped rather than implicit.

```elisa
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

```elisa
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

```elisa
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

```elisa
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

```elisa
const NEWLINE: char = '\n'
const LETTER: char = 'A'
```

Current rules:

- a char literal must decode to exactly one code unit
- the usual escape forms such as `\n`, `\t`, `\r`, `\0`, `\xNN`, and `\uNNNN` are accepted
- the builtin `char` type participates in the normal conversion surface, including helper-backed postfix forms such as `.i64()` when available

## Ordinary casts, low-level casts, and postfix cast hooks

The concise ordinary cast surface is `as`:

```elisa
alloc: mutable Arena& = &owner as mutable Arena&
```

Low-level reinterpretation stays deliberately loud:

```elisa
raw: void& = bytes.cast[void&]
words: uintptr& = state_bits.cast[uintptr&]
field_ref: Field& = node.field.ref[Field&]
```

This remains separate from postfix value-cast hooks:

```elisa
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

The same hook-backed conversion is also available as a type-constructor call:

```elisa
def score(op: Op) -> i64:
    return i64(op)
```

Current rules:

- `as` is the concise ordinary cast/coercion surface used throughout self-hosted code
- `.cast[T]` is the explicit low-level reinterpret cast surface
- `.ref[T&]` is the explicit lvalue/reference reinterpretation surface
- postfix shorthand like `op.i64()` dispatches to a visible exact `__cast__(value: Source) -> Target` hook when one exists
- prefix type-constructor shorthand like `i64(op)` uses the same cast path and the same exact `__cast__` hook lookup as `op.i64()`
- optional postfix shorthand like `text.int?()` dispatches to a visible exact `__cast__(value: Source) -> int?` hook when one exists
- ordinary explicit casts continue to use normal cast rules rather than hook dispatch
- the postfix hook surface is intentionally exact-source/exact-target rather than a broad overload search
- legacy expression-arrow casts such as `value -> T` are deprecated; `->` remains for function signatures, grammar signatures, effects, and other arrow-shaped declarations

That distinction matters when reading code: `value as T` is an explicit ordinary cast, `value.cast[T]` is a low-level reinterpretation, `slot.ref[T&]` is a reference reinterpretation, `value.T()` and `T(value)` are hook-backed conversion shorthands, and `value.T?()` is the same hook mechanism returning an optional.

## Checked `ensures` clauses

Function and extern declarations may carry checked poststate summaries for ref-visible paths.

```elisa
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
- in the fact-core model, `ensures` is an explicit `ensure` transform on normal return paths; error paths need their own explicit story and do not silently inherit success poststates

## Conservative call-site auto-borrow

The compiler may insert a borrow automatically at call sites when the callee expects a compatible ref and the argument is an obvious addressable lvalue.

```elisa
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

## Scoped arena shorthand

Tests and parser/runtime helpers often need a scratch arena plus an active allocation owner:

```elisa
region scratch(8192)
in scratch:
    owner: mutable Arena& = scratch.ref[mutable Arena&]
    values: darray[int] = [1, 2, 3]
```

The compact form is:

```elisa
with arena scratch(8192) as owner:
    values: darray[int] = [1, 2, 3]
```

For one-off dynamic array literals, the owner can also be attached directly to
the literal:

```elisa
values: darray[int] = [1, 2, 3] in owner
mapped: darray[int] = [value + 1 for value in values if value > 0] in owner
prepended: darray[int] = [first, ...rest] in owner
combined: darray[int] = [...left, ...right] in owner
```

The direct owner form is only for arena-backed dynamic array literals and
comprehensions. Fixed array literals remain pure values:

```elisa
values: int[3] = [1, 2, 3]
```

Region-owned structs use the same owner vocabulary. `struct Expr in owner:` is
sugar for `struct Expr[region owner]:`, and `Expr[scratch]` / `Expr[owner]`
specialize that owner to a named region, region parameter, or visible `Arena`
value.

When the struct also has type parameters, keep the type parameters in brackets
and put the owner-region sugar after them.

```elisa
struct Box[T] in owner:
    value: T
    next: owner Box[T, owner]&?

box: Box[i64, scratch] = Box{
    value: 42,
    next: null
}
```

Projection queries can also filter with a pattern and use the pattern payload
bindings in the projected value:

```elisa
ints: darray[i64] = value for each item in exprs where Expr.Int(value)
first_name: cstr? = name for first member in members where Member.Named(name)
```

This is sugar for the explicit region-plus-`in` shape:

- `scratch` is a named arena region
- the body runs as though it were inside `in scratch:`
- `owner` is bound inside the body as a `mutable Arena&`
- allocation ownership stays visible at the statement boundary
- the lower-level `region` and `in` forms remain available when tests or runtime code need to inspect or control the pieces separately
