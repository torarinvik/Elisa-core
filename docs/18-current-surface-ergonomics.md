# Current surface ergonomics

> The former `grammar`, `grammarenv`, `lexer`, `tokenset`, `charset`, and
> `keywordmap` declaration DSLs have been removed. Historical sections describing
> those forms are retained temporarily for migration archaeology and are not part
> of the supported language surface.

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

## Anonymous `where` refinement types

`T where predicate` is accepted in binder-like type positions as anonymous refinement sugar. The
predicate is written against the value being bound, so parameter and local refinements name the
binder directly, while return refinements use `result`.

```elisa
def at(xs: darray[i64], index: i64 where 0 <= index and index < xs.count) -> i64:
    return xs[index]

def clamp(n: i64, limit: i64) -> i64 where 0 <= result and result < limit:
    value: i64 where 0 <= value and value < limit = n
    return value
```

Current behavior:

- the parser preserves `T where predicate` syntax in parameters, returns, and local typed bindings
- the semantic type is representation-erased to `T`; anonymous `where` predicates do not create a
  distinct runtime carrier, ABI shape, `SameType` identity, or `AssignableTo` relationship
- constant anonymous predicates are checked for `bool` type today
- TODO lowering: anonymous `where` predicates should lower to the same proof/check pipeline as a
  compiler-synthesized value law over the bound subject

Restrictions:

- use `T is Law` when the predicate is named, reusable, externally documentable, or should seed a
  first-class refinement fact through existing law machinery
- use `requires` for obligations the caller must establish before entering a function
- use `ensure` for named postconditions or facts a function promises to establish for its callers
- keep `T where predicate` for local, anonymous binder refinements; it is not a subtype operator and
  must not affect storage, layout, overload resolution, or assignment compatibility

## Grouped `is` alternatives

Use bracketed `is [A | B | C]` when a value can match any of several variants or enum members. Short checks stay inline and the bracketed form keeps the intent obvious.

The parser also accepts the unbracketed multi-target form `value is A | B | C` for short inline tests. The bracketed spelling is usually clearer once the alternatives get longer or use qualified names.

```elisa
def is_additive(op: TokenKind) -> bool:
    return op is [.PLUS | .MINUS]

def is_rel(kind: Tok) -> bool:
    return kind is .LT | .LTEQ | .GT

def is_scalar(value: Expr) -> bool:
    return value is Expr.Int | Expr.Bool | Expr.Char
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

The formatter preserves compact bracketed alternatives when they are short and rewrites longer groups into the parenthesized vertical form.

## Optional AST payloads

Optional value types can be used directly in structs and enum payloads, which is preferable to paired `has_*` booleans plus dummy sentinel values.

```elisa
struct SMLDatatypeConstructor:
    constructor_span: SMLSpan
    name_id: NameId
    payload_type: SMLType.Type?

enum SMLDecl:
    Structure(name_id: NameId, signature_path: SMLNamePath?, decls: darray[SMLDecl])
```

Constructors use the ordinary optional surface: pass the present value when it exists, or `null` when it does not. Consumers should use `if value is name:` to unwrap the optional.

```elisa
if constructor.payload_type is payload_type:
    use_payload(payload_type)
```

Optional values can also be passed through a transform only when present. This is the preferred spelling for optional AST payload checks that used to require adapter helpers.

```elisa
def check_expr(self: mutable State&, expr: Expr) -> void:
    pass

def check_format_arg(self: mutable State&, precision: Expr?) -> void:
    precision?.(self.check_expr)
```

When the present branch needs a real block, use an `is` condition binding. The binding is available only in the truthy branch, and the bound value is the non-optional payload.

```elisa
def check_else_branch(self: mutable State&, else_stmt: Stmt?) -> void:
    if else_stmt is stmt:
        self.check_stmt(stmt)
        self.record_reachable_branch(stmt.span)
```

The same `is` bind form also works for nullable references. In the then-branch the binding has the non-null reference type, so ordinary field access and member calls are allowed without repeating a null guard.

```elisa
struct Node:
    value: i64

def read(node: Node&?) -> i64:
    if node is present:
        return present.value
    return 0
```

Prefer explicit `get` recovery for nullable values, optionals, and checked accesses. `get value else fallback` unwraps an optional or nullable reference and uses the fallback when absent. `else return`, `else raise`, and `else void` make the recovery action explicit.

```elisa
def required_name(name: NameId?) -> NameId error[LookupError]:
    return get name else raise LookupError.MissingName

def first_or_default(value: Item?) -> Item:
    return get value else Item.Default

def visit_if_present(value: Item?) -> void:
    get value else void
```

Use `get` for optional values and checked accesses when you want the absence or bounds check to stay visible in the source.

```elisa
def first_or_zero(xs: view[i32], i: usize) -> i32:
    return get xs[i] else 0

def require_value(value: i32?) -> i32:
    return get value else raise LookupError.MissingName
```

For error unions, bare `try fallible()` still propagates the error. `try fallible() else ...` handles the error locally and produces the success value.

```elisa
def load_or_default(path: Path) -> Buffer:
    return try read_file(path) else Buffer.Empty

def load_with_log(path: Path) -> Buffer:
    return try read_file(path) else err:
        log_error(err)
        return Buffer.Empty

def write_best_effort(value: Buffer) -> void:
    try write_file(value) else void
```

The same recovery actions are available for handled error unions: `try fallible() else return fallback`, `try fallible() else raise Error.Tag`, and `try fallible_void() else void`.

Error-union signatures use `T error[...]`, not the removed pipe form. Closed
rows can name whole families or specific tags, and open rows add trailing `...`
to keep the selected family rows open for compatible `try` / `raise`
propagation.

```elisa
error FileError:
    NotFound
    PermissionDenied

error NetworkError:
    Timeout
    Disconnected

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def load_any(use_disk: bool) -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
    if use_disk:
        return try read_disk()
    return try read_network()
```

Current rules for error-union signature rows:

- `T error[FileError]` names the closed whole-family form
- `T error[FileError.NotFound]` names a closed explicit-tag subset form
- `T error[FileError, ...]` keeps that family open in row form
- `T error[FileError.NotFound, ...]` keeps a representative tag row open rather than closed
- mixed-family open rows such as `error[FileError.NotFound, NetworkError.Timeout, ...]` are allowed
- equivalent full-family closed subsets canonicalize back to the family form, so listing every tag in one family formats as `error[Family]`
- legacy `T | ErrorSet` return syntax is no longer supported; use `T error[SomeSet]` instead
- legacy `error[Set.*]` and `error[Set.*, ...]` shorthands are no longer supported; use `error[Set]`, `error[Set, ...]`, or explicit tag rows instead

Removed compatibility note: the compiler no longer accepts older implicit absence-recovery spellings such as `value else fallback`, `xs[i] else fallback`, or `try optional_value else fallback`. Use explicit `get ... else ...` for nullable/optional and checked-access recovery, and use `try ... else ...` only for handled error unions.

```elisa
a: i64 = get maybe else 11
return get xs[i] else 0
return try read_value() else 11
```

When the handled branches want to name success and error payloads directly, use `catch expr:`. Each arm names the handled outcome and supplies the replacement expression or block.

```elisa
def load_flag(flag: bool) -> i64:
    return catch read_value(flag):
        loaded:
            loaded
        error err:
            log_error(err)
            1
```

The old recovery shortcuts `return?`, `match?`, and `try? ... default` have been removed. Use the explicit recovery surface: `get value else ...` or `get access else ...` for optional/checked-access recovery, and `try expr else ...` for handled error unions.

For multi-binding optional recovery, use ordinary `if value is name:` blocks or factor the recovery into helper functions.

```elisa
if lower is lower_value:
    if upper is upper_value:
        if value is value_int:
            return value_int >= lower_value and value_int <= upper_value
return false
```

For early-returning optional payloads, write the fallback explicitly.

```elisa
def first(found: i64?) -> i64?:
    return get found else return null
```

Optional values match directly; use a `null:` arm for the absent case and regular payload patterns for the present case.

```elisa
match maybe:
    null:
        return 0
    Expr.Int(value):
        return value
    _:
        return 2
```

The same form works as a match expression:

```elisa
def score(maybe: Expr?) -> i64:
    match maybe:
        null:
            return 0
        Expr.Int(value):
            return value
        _:
            return 2
```

Handled error-union fallback uses `try ... else ...`.

```elisa
value: i64 = try read_value() else 2
```

Packed/tree store refinement uses `match value in store:` with pattern arms. The old conditional form `if value in store as Pattern:` is a parser error.

```elisa
match node in store:
case Expr.Int(value: value):
    return value
```

The older dedicated statement surface `open value in store as Pattern:` has
been removed entirely. Migrate old code to `match value in store:` when the
store proof should scope a block or a set of pattern arms.

When a successful refinement needs to survive as a first-class value, packed
enums and trees use exact view types. Packed enums spell the refined witness as
`packedview[Enum.Variant]`. Trees use the bare exact member type, such as
`Lua.Expr.Binary`; the older `treeview[Lua.Expr.Binary]` spelling has been
removed.

```elisa
def score_lit(view_node: packedview[Expr.Int]) -> int:
    return view_node.span

def fold(node: Expr, store: Expr.Store[Frozen]) -> int:
    if node as Expr.Int:
        lit: packedview[Expr.Int] = node
        return score_lit(lit) + lit.value
    return 0
```

Payload destructuring also works directly inside the `if value as Pattern:`
surface, including unnamed payload positions and already-refined
`packedview[...]` parameters.

```elisa
def read_pair(node: Expr, store: Expr.Store[Frozen]) -> int:
    if node as Expr.Pair(left, right):
        return left + right + node.span
    return 0

def read_view(view_node: packedview[Expr.Int]) -> int:
    if view_node as Expr.Int(value: value):
        return value + view_node.span
    return 0
```

```elisa
def score_binary(view_node: Lua.Expr.Binary) -> i64:
    return view_node.left.span + view_node.right.span + view_node.span

def child_span(node: Lua.Expr) -> i64:
    if node is Lua.Expr.Binary:
        return score_binary(node)
    return node.span
```

Current rules:

- `packedview[Enum.Variant]` is the first-class exact packed-variant type after a successful packed refinement
- exact tree variants use the bare concrete member type such as `Lua.Expr.Binary`
- `treeview[Lua.Expr.Binary]` has been removed; write the bare concrete member type
- these exact refined types can appear in parameters, returns, and local bindings
- `if value as Expr.Variant(payload...)` supports both named and unnamed payload destructuring
- exact `packedview[...]` values may be re-matched with the same `if value as Pattern:` surface
- packed refinement can also infer active store provenance from a value that came from `store[index]`, including through an intermediate alias or field projection, so an explicit `in store` clause is not always required once the handle already carries that provenance

Frozen packed stores also expose dense node keys and arena-backed per-node side
tables. Use `dense_key(node, frozen_store)` to derive a stable `NodeKey[Expr]`
for a packed enum value or `packedview[...]` proven to come from that exact
frozen store root. Use `node_table_fill` to allocate one element slot per frozen
node.

```elisa
def inspect(owner: Arena) -> i32:
    store: Expr.Store[Local] = Expr.Store(owner)
    in store:
        left: Expr = new Expr.Lit(span: 1, value: 3)
        right: Expr = new Expr.Lit(span: 2, value: 4)
        _ = new Expr.Add(span: 5, left: left, right: right)

    frozen: Expr.Store[Frozen] = freeze(move store)
    node: Expr = frozen[2]
    key: NodeKey[Expr] = dense_key(node, frozen)
    table: NodeTable[Expr, i32] = node_table_fill[Expr, i32](owner, frozen, -1)
    table[key] <- 7
    return frozen[key].span + table[key]
```

Current rules:

- `dense_key(node, frozen)` requires a packed enum value or `packedview[...]` proven to come from the same exact frozen store root
- `NodeKey[Expr]` may index that exact `Expr.Store[Frozen]` root or a `NodeTable[Expr, T]` built from the same root
- `node_table_fill[Expr, T](owner, frozen, init)` returns `NodeTable[Expr, T]` with one slot per frozen packed node
- `NodeTable.values` exposes the backing storage as `view[T]`
- `node_table_fill` currently requires explicit specialization in v1
- these helpers are for packed enum frozen stores rather than ordinary enums or tree stores

Local packed-store roots may also produce handles through direct indexing, such
as `node: Expr = store[index]` or `node: Expr = box.store[0]`, when the local
store provenance remains in scope. Dense `NodeKey[...]` indexing is still tied
to exact frozen roots rather than local stores.

Frozen packed stores also expose a dense readonly tag view through `.tags`.
This is useful for scan-oriented loops and slice-based inspection when the pass
only needs the packed tag stream rather than the full nodes.

```elisa
def count_ints(frozen: Expr.Store[Frozen]) -> usize:
    nodes: view[Expr] = frozen[1:frozen.count]
    tags: view[Expr.Tag] = frozen.tags
    prefix: view[Expr.Tag] = frozen.tags[0:1]
    count: mutable usize = 0
    for tag in tags:
        if tag == Expr.Tag.Int:
            count <- count + 1
    return count
```

Current rules:

- `frozen.count` is the dense packed-node extent of that frozen store root
- `frozen[i]` yields the packed enum value at that dense frozen-store index
- `frozen[a:b]` yields an ordinary readonly `view[Expr]` slice over the frozen packed nodes
- `frozen.tags` exposes the packed tag stream as `view[Expr.Tag]`
- prefer iterating the view directly, as in `for tag in frozen.tags:`, over
  indexing `0..<frozen.count` unless the numeric index itself is needed
- packed-store root index results are readonly; assignment through `frozen[i] <- ...` is rejected
- slicing a frozen tag view keeps the ordinary readonly `view[Expr.Tag]` surface
- packed-store slice index results are readonly; assignment through `chunk[i] <- ...` is rejected
- tag-view index results are readonly; assignment through `tags[i] <- ...` is rejected

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

For statement-oriented early exits, the parser also accepts `guard condition else ...` and the equivalent alias `require condition else ...`. These are best kept for short guard-style exits where the `else` action is the entire point of the statement.

```elisa
guard maybe != null else return 0
require enabled else return INVALID_SYMBOL_ID
```

When an early optional result depends on several optional inputs, chain `is` bindings with `and`. Each binding unwraps an optional in order, and the branch runs only when all bindings are present.

```elisa
def in_range(lower: i64?, upper: i64?, value: i64?) -> bool?:
    if lower is lower_value and upper is upper_value and value is value_int:
        return value_int >= lower_value and value_int <= upper_value
    return null
```

The same form works when the present branch performs diagnostics, mutation, or several statements.

```elisa
if lower_value is actual_lower and upper_value is actual_upper:
    actual_lower > actual_upper then:
        record_diagnostic()
    return
```

This lowers to the same ordinary optional-bind ladder you would write by hand. A short condition can be kept as a statement block with `then:` when that makes the guard read naturally.

`elif value is name:` continues the same optional-bind family in the fallback branch, which keeps multi-stage optional selection flatter than a second nested `if`.

```elisa
if lower is actual_lower and upper is actual_upper:
    return
elif fallback is actual_fallback:
    return
```

When the present branch immediately wants to match on the unwrapped value, combine an `is` bind with an ordinary `match`.

```elisa
def describe(maybe: Expr?) -> i64:
    if maybe is expr:
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

Slice operands also work with `is` binds. In that form, the slice acts as a checked view construction: the branch runs only when the slice bounds are valid, and the bound name has the resulting bounded view type inside the branch.

```elisa
def sum_prefix(xs: darray[i32]&) -> i32:
    if xs[0:3] is s:
        return s[0] + s[1] + s[2]
    return -1
```

Current rules:

- `if value is name:` accepts value optionals such as `T?` and nullable references such as `T&?`
- `if first is a and second is b:` runs only when every optional binding is present; bindings are evaluated left-to-right
- `place with T{ ... }` updates several fields of one PLACE without repeating it:

  ```elisa
  ui_widgets[index] with UiWidget{
      kind <- kind
      parent <- parent
      x <- 0.0
      hovered <- false
  }
  ```

  Each line is an ordinary `field <- value` assignment. A bare identifier place is assigned
  through directly (so it works on a `mutable T&` parameter); any other place is bound ONCE
  to a hidden reference, so the place expression is evaluated once and the fields are
  assigned in the order written. A value ends at the comma: `{ kind <- 7, x <- 1.5 }`. The type name is optional (`ws[2] with { kind <- 6 }`); when present, the place
  must have that type. A bare field name puns to the local of the same name, so
  `ws[2] with { kind, parent }` is `kind <- kind` and `parent <- parent`; struct literals
  accept the same shorthand (`Widget{ kind, parent, x: 0.0 }`). Use `with` for partial
  updates and a whole struct literal for a reset, since a literal must name every field.
  It is parser-level sugar, identical in both compilers.
- a struct literal `T{b: f(), a: g()}` evaluates its field initializers left-to-right in the order they are WRITTEN, whatever order the fields are declared in; side effects observe that order (a literal whose initializers register things in a table registers them in source order)
- `elif value is name:` is the optional-bind continuation form for an `if` chain
- inside the then-branch, `name` has type `T` for value optionals and `T&` for nullable references
- `if source[a:b] is name:` performs a checked slice bind and exposes the bounded view only when the slice is in range
- `is` binds compose with ordinary boolean conditions using `and`, so `if maybe is value and value > 0:` is valid
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
- use `if optional is name:` for optional unwrapping; `expect let` is for pattern matching over concrete values

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

Iteration style is intentionally iterable-first. For ordinary traversal, use
`for item in source:`, query predicates such as `any item in source where ...`,
or comprehensions such as `[project(item) for item in source]`. When the index
is part of the logic, use `source.enumerate()` and bind both values:
`for index, item in source.enumerate():`. Use numeric ranges such as `0..<n`
for numeric algorithms, fixed-count loops, explicit stride/bounds control, or
temporary cases where the source has no iterable surface yet.

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

def enabled_entries(owner: Arena, entries: darray[Entry]) -> darray[Entry]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return each entry in entries where entry.enabled with alloc

def positive_int_payloads(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return value for each item in items where Expr.Int(value): value > 0 with alloc

def positive_int_payloads_explicit(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return value for each item in items where item is Expr.Int(value): value > 0 with alloc

def positive_after_index(items: darray[Expr]) -> bool:
    return any index, item in items.enumerate() where item is Expr.Int(value): value > index

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
- `each name in source where predicate with owner` returns the original elements as `darray[T]`; omit the projection when the query should keep the source element unchanged
- pattern filters may add a guard after `:`, as in `where Expr.Int(value): value > 0`; this works for query expressions and iterable `for` loops
- explicit-subject pattern filters are accepted as an equivalent readability form, as in `where item is Expr.Int(value): value > 0`; they lower to the same typed narrowing as the shorter pattern filter
- multi-bind queries and `enumerate()` filters may name the narrowed subject explicitly, as in `index, item ... where item is Expr.Int(value)`; the subject must be one of the query binders
- `count name in source where predicate` returns `usize`
- the source uses ordinary iterable expression lowering, such as arrays, dynamic arrays, views, strings, `rows()`, `source.enumerate()`, and tree child views
- numeric range-loop headers such as `0..<n` are for numeric/index-only loops, explicit bounds or strides, and cases where no iterable source exists; they are not the idiomatic spelling for ordinary collection traversal
- reverse traversal should use the iterable source form `rev(source)`, for example `for item in rev(items):`
- loop-header typed `where` payloads are visible in the loop body
- query predicates are analyzed in a scope where the query binder is bound to the iterable element type, or each multi-bind name is bound to its tuple/struct field type
- query pattern-bound names are scoped to the query projection and filter guard; they do not leak after the query expression
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

Do not build reusable filtered-view chains for local predicates. Write the
predicate directly in the loop/query header, or use a list comprehension when a
filtered collection is the desired result. This keeps the lowering as one
obvious loop rather than introducing a lazy iterator pipeline.

## Proof-carrying view helpers

View helpers can be written either as ordinary free functions or as receiver-style calls. The receiver form is syntax sugar: the compiler rewrites the receiver as the first helper argument, so the same optimization facts and lowering paths are preserved.

```elisa
def sum_selected(values: view[i32]) -> i32:
    source: view[i32] = values.readonly()
    total: mutable i32 = 0
    for value in source where keep_positive(value):
        total <- total + identity(value)
    return total

def check(values: bool[8]) -> bool:
    return all value in values where is_enabled(value)

def chunks(values: view[i32]) -> ChunksExactView[i32]:
    return values.readonly().chunks_exact(4)

def halves(values: view[i32]) -> SplitView[i32]:
    return values.split_at(8)
```

Current receiver helpers:

- `source.enumerate()` and `enumerate(source)`
- `source.any()` / `source.all()` and `any(source)` / `all(source)`
- `source.readonly()` and `readonly(source)`
- `source.split_at(index)` and `split_at(source, index)`
- `source.chunks_exact(width)` and `chunks_exact(source, width)`
- `source.reduce_sum(callback)` and `reduce_sum(source, callback)`

Actual fields and declared methods still win over helper rewriting, so this surface remains compatible with ordinary member access.

## Inspectable Lexer Compression

Lexer helpers follow Elisa Core's pyramid-of-abstraction rule: source syntax may compress intent, but every layer must remain inspectable. A declaration should lower to named helper functions or data with predictable cost, and generated helpers should be easy to compare with equivalent handwritten code.

The first lexer-compression primitive is `charset`, an ASCII byte character set declaration for common lexer predicates:

```elisa
charset IdentStart = 'a'..'z' | 'A'..'Z' | '_'
charset Digit = '0'..'9'
charset IdentContinue = IdentStart | Digit

def is_ident_start(ch: char) -> bool:
    return ch in IdentStart

def is_ident_continue(ch: char) -> bool:
    return ch in IdentContinue
```

This MVP accepts ASCII character literals, inclusive ranges, and references to other `charset` declarations. References are expanded during semantic analysis, so membership checks such as `ch in IdentContinue` lower through the existing membership-candidate path as ordinary range/equality comparisons. Unknown references, cycles, duplicate expanded characters, descending ranges, and non-ASCII literals are rejected. Later implementations may choose a compact table or generated helper when that is clearer or faster, but the lowered representation should remain visible and documented.

`keywordmap` is the matching string-to-token primitive. The source declares the keyword table once, and lowering produces a normal function with a string `match`, so the exact dispatch remains visible in formatted lowered output:

```elisa
keywordmap lua_keyword: sview -> LuaTokenKind:
    "and" => .AND
    "break" => .BREAK
    _ => .NAME
```

The input carrier can be `sview` for lexer slices or `cstr` for null-terminated frontend token lookup helpers:

```elisa
keywordmap token_kind_for_text: cstr -> TokenKind:
    "program" => .PROGRAM
    "+" => .PLUS
    _ => .EOF
```

The `_` arm is required and acts as the fallback for non-keyword identifiers. Duplicate keyword entries are rejected by the parser. The MVP lowers to a straightforward string match; later slices may add table/trie/perfect-hash lowering when benchmarks justify it, as long as the selected representation remains inspectable.

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
params: mutable InlineVec[PascalParamSpec, 8] = inlinevec.new(owner)
params.push(param)
```

```elisa
decls: darray[Pascal.Decl] @owner = []
decls.push(decl)

for decl in decls.view():
    visit_decl(decl)
```

```elisa
symbols: IndexMap[NameId, PascalSymbol] = indexmap.new(owner)
id: SymbolId = symbol_table_id_from_index(symbols.set(name_id, PascalSymbol{...}))
symbol: PascalSymbol? = symbols.get(name_id)

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

For `layout soa struct` stores, `push(...)` returns a typed row handle such as
`RowId[PascalSymbols]` rather than the mutable receiver. Code may name that
handle family directly or through a local alias such as `type SymbolRow =
RowId[SymbolRows]` when the row id should travel through APIs.

The older `soa SymbolRows:` declaration form has been removed; use
`layout soa struct SymbolRows:`.

That typed row handle is also the indexing key for SoA columns, so expressions
such as `symbols.flags[id]` and other `store.field[row_id]` reads stay tied to
the originating store type rather than using an untyped integer row number.

Use `Flags[T]` for typed sets of const-enum values that grow or flow through APIs. Prefer `flags.new()` plus `.add(...)`, `.remove(...)`, and `flags[Enum.Member]` membership checks over hand-built integer masks; reserve `flags_from_bits(...)` and `flags_bits(...)` for interop or serialization boundaries. Generic helpers may mention `Flags[T]` before `T` is specialized, but concrete instantiations must use a `const enum` element type. Use struct-local `bitset` groups when the flags are fixed fields of one storage object.

Use `InlineVec[T, N]` for tiny hot lists where most values fit inline but occasional spill to an arena-owned `darray` is acceptable. Parser and semantic scratch lists such as params, labels, directives, and duplicate-detection sets are the intended shape.

Use plain `darray[T]` declarations when constructing arena-owned dynamic arrays. `xs: darray[T] @owner = []` keeps the owner relationship visible at the declaration site, while an active `in owner:` scope can infer the region for `xs: mutable darray[T] = []`. The old builder wrapper surface has been removed.

Native frontend suites should consume these helpers through the runtime surface that their runner already links, not by including implementation files such as `collections.elisa` directly into a test module. Direct implementation includes duplicate runtime globals and helper symbols when the native runner also links `native_runtime_support.elisa`. The generated `collections.elisai` interface is kept in sync with the implementation as the declaration source of truth. For native dogfood frontends, keep existing `darray` construction style until generic extern collection helpers either lower through concrete wrappers or the backend can specialize those linked runtime declarations with the same ABI as in-module generic helpers.

The old tree declaration surface has been removed. Use ordinary enum/struct
shapes, explicit typed handles where needed, and `layout soa struct` rows when a
frontend really wants columnar storage. After `freeze(move store)`, prefer
ordinary row views and helper functions for scan-oriented work rather than
tree-specific tags or columns.

```elisa
int_count: usize = count node in frozen.Expr where node.kind == .Int
fast_int_count: usize = count node in frozen.Expr.where_kind(.Int)
target_count: usize = count node in frozen.Expr where span == target
target_ints: usize = count node in frozen.Expr where kind == .Int and span == target
span_sum: i64 = reduce_sum(frozen.Expr.column("span"), add_i64)
```

Use ordinary query `where` clauses for row filters. Inside a frozen row-view query, common fields can be used directly in the filter; hot equality predicates over frozen SoA rows, such as `where span == target` or `where kind == .Int and span == target`, lower to direct tag/column comparisons instead of predicate function calls. `where_kind` remains available for explicit kind-only filters. The older `tree_tags(frozen, "Expr")` and `tree_column(frozen, "Expr", "span")` helper forms have been removed; use row-view queries or `.column(...)` instead. The design goal is that a tree can be tuned for recursive traversal, vectorized passes, or parallel chunk processing by changing layout metadata rather than rewriting the compiler frontend.

For enum-backed row views, payload-field filters should be guarded by `kind` so
the field is only read on variants that actually carry it:

```elisa
enum PascalDecl:
    ConstDecl(name_id: NameId, ...)
    FunctionDecl(name_id: NameId, ...)

matches: usize =
    count decl in frozen.Decl
    where kind == .FunctionDecl and name_id == target
```

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
- legacy spellings `sizeof`, `alignof`, and `offsetof` have been removed; use the underscore forms
- prefer these builtins in runtime, FFI, packed-layout, and backend test code instead of duplicating ABI constants by hand

## Static Assertions

Use `static assert` when an invariant must be checked while compiling. Constant-only assertions are checked by semantic analysis; target-layout assertions are checked by the backend so they use the selected target data layout.

```elisa
static assert size_of[Header]() == 16, "Header ABI changed"
static assert offset_of[Header](.payload) == 8

static assert:
    fields(Header).count == 2
    offset_of[Header](.payload) == 8

def keep() -> void:
    static assert 5 > 3

    static:
        assert align_of[Header] == 8
        if size_of[Header]() == 16:
            assert offset_of[Header](.payload) == 8
```

Current rules:

- `static assert` is valid at top level and inside statement blocks
- `static assert:` groups assertion conditions in an indented block; each line is checked as an independent static assertion and may optionally use `condition, "message"`
- `static:` blocks group static-only statements inside runtime functions; inside the block, `assert`, `if` / `elif` / `else`, and `error(...)` are static by context
- `static def` declares a compile-time-only function; inside its body, plain `assert ...` and `error(...)` are static by context, and static assertions or static expression statements may call simple static functions with compile-time locals, assignments, conditionals, `match` over compile-time literal values, bounded loops, iterable loops over const lists, named arguments, defaults, constant tuple/list literals and aggregate locals with constant indexing, simple compile-time `darray` builders via `push` and `extend`, compile-time aggregate `.count`, static reflection through `variants(T)` / `fields(T)`, compile-time `any` / `all` / `first` / `count` / `each` queries over const-built lists, optional fallback with `else`, and direct structurally decreasing recursion, while runtime calls are rejected
- static iterable loops and compile-time queries can use tuple, list, and struct pattern filters when the source values are const-evaluable; non-matching elements are skipped the same way as runtime `where` filters
- non-void static functions must return on all paths; `void` static functions may terminate by falling through; direct recursive static calls must visibly decrease a parameter with `parameter - positive_compile_time_integer` and have a visible lower-bound base case such as `if n <= 0: return ...`, `return base if n <= 0 else recurse`, or `if n > 0: recurse else return ...` for signed counters, with analogous `n == 0` / `n != 0` forms for unsigned counters; indirect recursive static cycles are rejected for now, and the evaluator reports a call-depth limit if a compile-time computation grows too large
- the condition must type-check as `bool`
- if the condition is a semantic compile-time constant, a false value is reported before backend lowering
- target-aware layout intrinsics are accepted in static assertions and checked during backend lowering
- the optional message must be a compile-time string to appear in the diagnostic

Target constants are also available inside `static if`, `static elif`, and
`static assert` conditions:

```elisa
static if ELISA_TARGET_OS_MACOS:
    const ABI_NAME: string = "apple"
static elif target.features.posix:
    const ABI_NAME: string = "posix"
static else:
    const ABI_NAME: string = "other"

static if target.debug:
    const BUILD_MODE: string = "debug"
static elif DEBUG:
    const BUILD_MODE: string = "debug-compat"
static else:
    const BUILD_MODE: string = "release"

static if PLATFORM_WINDOWS:
    const PLATFORM_KIND: string = "windows"
static elif PLATFORM_APPLE and ARCH_ARM64:
    const PLATFORM_KIND: string = "apple-arm64"
static elif PLATFORM_LINUX and ARCH_X86_64:
    const PLATFORM_KIND: string = "linux-x64"
```

Current rules:

- `static if` and `static elif` conditions must reduce to compile-time booleans
- legacy flat target booleans such as `ELISA_TARGET_OS_MACOS`, `ELISA_TARGET_OS_LINUX`, and `ELISA_TARGET_OS_POSIX` are available in static conditions
- the structured `target` namespace exposes compile-time strings and booleans such as `target.os`, `target.arch`, `target.debug`, `target.release`, nested feature flags like `target.features.posix`, and target-library flags like `target.libc.gnu_strerror_r`
- compatibility aliases such as `PLATFORM_WINDOWS`, `PLATFORM_LINUX`, `PLATFORM_APPLE`, `ARCH_X86_64`, `ARCH_ARM64`, and `DEBUG` continue to work in static conditions

## Static Reflection

Static code can inspect declaration shapes without hand-maintained parallel tables. Use `variants(T)` for const enums, enums, and tree categories, and `fields(T)` for structs and record-like tree members.

```elisa
enum Maybe:
    None
    Some(value: i64)

struct Pair:
    left: i64
    right: bool

static def payload_variants() -> i64:
    total: mutable i64 = 0
    for variant in variants(Maybe):
        if variant.has_field("value"):
            total <- total + 1
    return total

static assert variants(Maybe).count == 2
static assert fields(Pair).count == 2
static assert payload_variants() == 1
```

Current rules:

- `variants(T)` returns a compile-time list of records with `name`, `index`, `tag`, `field_count`, and `fields`
- each entry in `variant.fields` and `fields(T)` has `name`, `index`, and `mutable`
- `variant.has_field("name")` is available in static expressions
- reflected lists can be used by static `for` loops and compile-time query expressions such as `first`, `any`, and `count`
- the first slice is static-only; runtime reflection values are not emitted

## Static Declaration Generation

`static generate:` executes a declaration-level static block and splices emitted declarations into the surrounding declaration list.

```elisa
enum Expr:
    Int(value: i64)
    Bool(value: bool)

static generate:
    for variant in variants(Expr):
        emit def is_${variant.name}(expr: Expr) -> bool:
            return expr is Expr.${variant.name}

def keep(expr: Expr) -> bool:
    return is_Int(expr) or is_Bool(expr)
```

Current rules:

- `static generate:` is declaration-level only
- declaration `emit` is valid inside `static generate:`
- sequence-rewrite `emit` is also valid inside `rewrite ... as sequence[T]:` arms
- `${expr}` may appear inside generated identifiers or member paths; the expression must evaluate to a compile-time string, integer, or reflection record with a `name`
- generated declarations are parsed as normal Elisa declarations and then use the normal semantic and backend paths
- generated declarations are inserted where the generator appears, so ordinary declaration visibility and duplicate-name diagnostics apply

Inside sequence rewrites, `emit` appends values into the output sequence being built. `emit value` appends one element, `emit all values` appends every element from a `darray` or `view`, and `emit nothing` leaves the current arm without output.

```elisa
def compact(items: view[u32]) -> darray[u32]:
    return rewrite items as sequence[u32]:
        item when item != 0:
            emit item

def concat(left: view[u32], right: view[u32]) -> darray[u32]:
    segments: darray[view[u32]] = [left, right]
    return rewrite segments as sequence[u32]:
        segment:
            emit all segment
```

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
- parameter defaults are not accepted in export wrapper signatures

If a shorthand named argument such as `missing:` has no in-scope value named `missing`, semantic analysis reports that directly.

## Permission declarations, aliases, and `signal`

Top-level `permission` declarations introduce named authority families directly in source. A permission with members behaves like a capability family with explicit members; `pass` declares a marker permission with no members.

```elisa
permission FooPermission:
    pass

permission ConsolePermission:
    Write
    Flush

def run() -> void:
    can FooPermission, ConsolePermission.Write:
        signal FooPermission
        signal ConsolePermission.Write
```

`signal` is a zero-runtime statement. It does not emit a runtime trap or object; it exists to make permission usage explicit so the surrounding function contract still records and checks the authority requirement.

Current rules for `permission` and `signal`:

- `permission Name:` declares a family directly
- `permission Name: pass` is the marker-permission form with no members
- indented names such as `Write` and `Flush` declare members of that family
- `signal Name` records use of a whole-family permission
- `signal Name.Member` records use of one concrete member
- `signal` participates in the same permission inference and local-grant checking as calls to permission-requiring functions
- family-level signature permissions such as `can[ConsolePermission]` satisfy member signals and calls from the same family
- function bodies infer their callable permission surface from effectful operations and local grants
- explicit signature permissions still matter on surfaces without bodies, such as `extern` declarations and function types
- explicit signature permissions do not by themselves satisfy local-grant checking inside the body

Top-level `alias` declarations name a reusable **capability set**, referenced via `can`:

```elisa
alias FrontendCaps = Abort.Panic, Memory.Allocate

def parse() -> i64 can[FrontendCaps]:
    return 1

def parse_debug() -> i64 can[FrontendCaps, Console.Write]:
    return 1

extern register(callback: func() -> void can[FrontendCaps]) -> void
```

`alias` is compile-time surface only — it expands during semantic analysis into the existing
permission model; it creates no runtime object or LLVM artifact.

**Capabilities and errors are separate, un-bundleable channels.** Capabilities go in `can[...]`;
errors live in the **type system**. An error-bearing signature writes its error union directly
in the return type, optionally named with a `type` alias:

```elisa
type FrontendResult = i64 error[ParseErr]

def parse_checked() -> i64 error[ParseErr] can[FrontendCaps]:   # errors + capabilities, side by side
    return 1
```

Current rules:

- `alias` names a capability set; reference it in `can[...]` (signature) or `can Name:` (block).
- capabilities (`can`) and errors (`error[...]`) are separate channels — there is no syntax that
  bundles them, so they cannot be mixed in one clause.
- `permission` declarations and `alias` capability sets are compile-time surface only; both lower
  into the existing semantic permission/effect model rather than a runtime object.

> Removed: the old `effectalias`/`effects[...]` keywords (which bundled errors + capabilities) and
> the legacy `effect` declaration alias (use `permission`).

### Local `can` grants and formatter normalization

Function types and other body-less surfaces use declaration syntax such as `can[Console.Write]`. Function declarations with bodies can usually omit signature permissions because effect inference records them from local grants and effectful operations. Inside a body, effectful use sites still need an explicit local grant.

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
- inferred or explicit surface permissions on the enclosing function or alias (`can[...]`) do not replace an explicit local grant at the use site
- `-emit fmt` always prints local grants in surface syntax rather than declaration syntax
- the formatter conservatively inlines simple one-statement grant blocks into `... can ...` form for returns, assignments, declarations, tuple binds, discards, `as` rebinds, and expression statements
- the formatter keeps block form for multi-statement regions and for statements it cannot safely rewrite, including statement-position `panic(...)`
- when a granted expression contains `try ... else ...` or `get value else fallback`, the formatter parenthesizes the expression so the grant applies to the whole expression

Style guidance:

- prefer an inline grant when one operation needs the permission once
- prefer a `can ...:` block when multiple operations share the same grant or when keeping the grant as a block makes control flow or non-null narrowing clearer

### Trusted implementation grants and unsafe capabilities

Use `trusted ...:` when a function uses a permission internally but does not expose that permission to callers. This is the surface for safe wrappers around low-level operations: the trusted block is still locally checked, but the granted permissions are not inferred into the enclosing function type.

```elisa
extern raw_pointer_cast(value: uintptr) -> heap u8& can[Unsafe.PointerCast]

def as_byte_ptr(value: uintptr) -> heap u8&:
    trusted Unsafe.PointerCast:
        return raw_pointer_cast(value)
```

Use ordinary `can ...:` instead when the caller must uphold the invariant:

```elisa
def as_byte_ptr_unchecked(value: uintptr) -> heap u8&:
    can Unsafe.PointerCast:
        return raw_pointer_cast(value)
```

`trusted` accepts the same comma-separated permission list surface as `can`.
This is especially relevant around globals, where plain reads use
`Global.Read`, writes use `Global.Write`, and mutable globals additionally need
`Unsafe.MutableGlobal`:

```elisa
global mutable counter: int = 0

def read_counter() -> int:
    trusted Global.Read, Unsafe.MutableGlobal:
        return counter

def set_counter(value: int) -> void:
    can Global.Write, Unsafe.MutableGlobal:
        counter <- value
```

The same distinction applies to FFI, indexing, globals, and thread sharing. A safe wrapper should check or establish the invariant nearby, then use a narrow `trusted` block only around the operation that needs authority:

```elisa
extern malloc(bytes: usize) -> heap void&? can[Memory.Allocate]

def require_alloc(bytes: usize) -> heap void& error[MemoryError.OutOfMemory]:
    can Memory.Allocate:
        trusted Unsafe.RawExtern:
            raw: heap void&? = malloc(bytes)
        return raw else raise MemoryError.OutOfMemory
```

Unchecked APIs expose the invariant to their caller instead:

```elisa
def get_unchecked[T](items: T&, index: usize) -> T can[Unsafe.UncheckedIndex]:
    return items[index]
```

Current builtin unsafe capabilities are:

- `Unsafe.PointerCast` for representation-changing pointer/reference casts
- `Unsafe.PointerArithmetic` for raw pointer offset math
- `Unsafe.GuestHostPointerCast` for crossing from guest-address or boundary pointer representations into host references
- `Unsafe.IndirectCall` for indirect calls through reinterpretation surfaces such as `value.call_as[...]`
- `Unsafe.UncheckedIndex` for indexing without a proven or checked bound
- `Unsafe.RawExtern` for direct calls across a raw foreign boundary
- `Unsafe.MutableGlobal` for shared mutable global state
- `Unsafe.ThreadShare` for transferring mutable or non-frozen references across threads before stronger ownership facts prove safety
- `Unsafe.StaleRef` for using a view or borrowed storage value after an invalidating container/storage operation
- `Unsafe.Alias` for explicit mutable aliasing that violates the one-writer-or-many-readers rule
- `Unsafe.BufferReinterpret` for reinterpreting bounded buffers as unbounded pointer/string-style views
- `Unsafe.Leak` for intentionally abandoning a region/resource cleanup obligation

Related non-unsafe permission families used by the same local-grant surface include:

- `Global.Read` for reading globals
- `Global.Write` for writing globals

Examples of these narrower escape hatches:

```elisa
def run(p: void&?) -> int:
    trusted Unsafe.IndirectCall:
        return p.call_as[func(int) -> int](7)

struct Header:
    a: u32
    b: u32

struct Blob:
    data: mutable darray[u8]

def header_at(self: Blob&, off: usize) -> Header&:
    trusted Unsafe.BufferReinterpret:
        return self.data[off].ref[u8&].cast[Header&]
```

Current notes:

- `value.call_as[func(...)-> T](args...)` is the indirect-call primitive; in strict unsafe-permission mode it is gated by `Unsafe.IndirectCall`
- wider reinterpret casts from byte-buffer interior elements, such as `self.data[off].ref[u8&].cast[Header&]`, are gated by `Unsafe.BufferReinterpret` in strict mode
- `leak region_name` satisfies the region-consumption obligation but is an explicit unsafe opt-out; in strict mode it is gated by `Unsafe.Leak`

Keep trusted blocks narrow. The preferred style is to wrap only the operation whose invariant has been checked nearby, not a whole function body. This keeps low-level code inspectable without pushing every trusted implementation detail into caller-facing permissions.

The strict unsafe-permission analysis path currently gates:

| Capability | Strict-mode gate |
| --- | --- |
| `Unsafe.PointerCast` | numeric/reference and representation-changing pointer-like casts |
| `Unsafe.PointerArithmetic` | raw reference plus/minus integer offset math |
| `Unsafe.UncheckedIndex` | scalar/byte reference indexing, plus fixed-size array indexing that has no proof-carrying bound yet |
| `Unsafe.RawExtern` | direct calls to `extern` functions |
| `Unsafe.MutableGlobal` | reads/writes of `global mutable` declarations |
| `Unsafe.ThreadShare` | `spawn1` / `pool_submit1` transfers or result payloads containing non-static refs |
| `Unsafe.StaleRef` | view/slice use after `clear`, `reserve`, `push`, or similar storage-invalidating operations |
| `Unsafe.Alias` | duplicate mutable refs, shared+mutable ref overlap, and live local borrow conflicts at calls |

The first proof-carrying bounds slice is intentionally small. In strict mode, a fixed-size array or dynamic array index is treated as safe when the compiler can see a simple proof from a range loop, branch guard, early-return guard, or `assert`:

```elisa
def sum4(items: i32[4]) -> i32:
    total: mutable i32 = 0
    for i in 0..<4:
        total <- total + items[i]
    return total
```

An equivalent unproven index requires `Unsafe.UncheckedIndex` or a checked/fallback indexing form:

```elisa
def read_at(items: i32[4], index: usize) -> i32 can[Unsafe.UncheckedIndex]:
    return items[index]
```

Use the unsafe report to keep the public unsafe surface countable:

```sh
go run ./src -emit unsafe path/to/file.elisa
```

The report runs strict unsafe analysis and lists caller-visible `Unsafe.*` requirements by capability and function. Trusted implementation blocks are intentionally not counted as caller-facing API; keep those blocks narrow so a code review can still inspect the exact unsafe operation and the nearby invariant that justifies it.

Future proof sources should include enumerate-derived facts and a deliberate `assume` form. `assert` is the runtime-checked proof path; `assume` should require a separate unsafe capability rather than silently manufacturing facts.

The default compiler path remains compatibility-oriented while runtime and generated sources migrate into trusted wrappers. Strict mode is the audit surface: it turns low-level footguns into named, searchable permissions without adding runtime branches or Rust-style lifetime analysis.

### Member-set brace sugar

`can[...]` and `error[...]` accept a brace shorthand for selecting several members of one family without repeating the family name. It expands to the dotted form, so `can[Disk{Read, Write}]` is exactly `can[Disk.Read, Disk.Write]` and `error[E{Bad1, Bad2}]` is exactly `error[E.Bad1, E.Bad2]`.

```elisa
extern scan() -> i64 can[Disk{Read, Write}]
def read_or_fail() -> i64 error[E{Bad1, Bad2}]:
    return 0
```

Current rules:

- the brace form is pure surface sugar; it expands to the dotted refs/tags during parsing
- a brace subset such as `error[E{Bad1}]` stays a proper subset (it does not widen to the whole family)
- it works in both the permission-ref position (`can[...]`) and the error-set position (`error[...]`)

### Subsumption-declaring families

A permission family may declare that it *includes* other whole families. A whole-family grant of the including family then satisfies any required member of an included family, transitively.

```elisa
permission Disk:
    Read
    Write

permission IO:
    includes Disk

extern read_disk() -> i64 can[Disk.Read]

def build() -> i64:
    can IO:                 # IO includes Disk -> satisfies the Disk.Read call
        return read_disk()
```

Current rules:

- `includes A, B` lines inside a `permission` body declare that family subsumes A and B
- subsumption is transitive (`App: includes IO`, `IO: includes Disk` ⇒ `can App:` satisfies a `Disk.Read` requirement)
- an unrelated family grant never satisfies a requirement (no spurious subsumption)
- unknown includes and include cycles are rejected at declaration time
- the same shared set lattice backs both permission families and error sets, with mirrored variance (errors are produced/covariant, permissions are required/contravariant)

### Checked `can X as Y:` cast

`can X as Y:` discharges the member uses `X` inside the block and surfaces the declared superset `Y` as the function's inferred capability instead. It is sound only when `Y` subsumes `X` (via `includes`); otherwise it is rejected and you must declare the `includes` relation or use `trusted`.

```elisa
def via_io() -> i64 can[IO]:
    can Disk.Read as IO:    # legal because IO includes Disk
        return read_disk()
```

Current rules:

- `as` is the checked, non-trusted re-attribution path: legal iff `Y ≥ X` in the lattice
- the block surfaces `Y` (not the concrete members used) as the inferred `can[...]`
- an unsound cast is rejected with a suggestion to declare `includes` or use `trusted`
- `trusted X:` remains the only drop (it removes the effect from the surface entirely); `as` never drops
- effects are erased, so the cast has no backend cost

### The `any` top permission

`can[any]` is the explicit erasure escape (for FFI, stored heterogeneous closures, and dynamic dispatch). A `can[any]` grant satisfies every concrete requirement; a `can[any]` *requirement* is satisfied only by another `any` grant (or a `trusted` block), never by a concrete grant.

```elisa
def build() -> i64:
    can any:                # satisfies any concrete requirement below
        return read_disk()
```

Current rules:

- `any` is reserved: it cannot be declared as a family and has no member access (`any.Read` is rejected)
- a `can[any]` grant discharges every concrete member/family requirement
- a `can[any]` requirement falls out of no concrete grant — only `any`/`trusted` discharge it

### Capability-set aliases (`alias`)

`alias Name = ref, ref` names a fixed set of permission refs for reuse. Unlike `includes`
(whole-family subsumption) it captures an exact cross-family member set — the right tool for a
recurring member combo such as `Segment.Host, Unsafe.SegmentMutation`. The same `alias`
declaration is usable both in a signature `can[Name]` and in a local `can Name:` grant block.

```elisa
alias HostSeg = Segment.Host, Unsafe.SegmentMutation

def map_segment() -> i64 can[HostSeg]:    # signature: expands to Segment.Host, Unsafe.SegmentMutation
    can HostSeg:                            # local grant: same expansion
        return install_segment()
```

Current rules:

- `alias Name = ...` is a top-level declaration; the right-hand side is a comma-separated permission-ref list (the same surface as `can[...]`, including brace groups like `Disk{Read, Write}`)
- an alias expands to its refs wherever permission refs are resolved — local `can <alias>:` / `expr can <alias>` grants and signature `can[<alias>]` clauses alike
- aliases may reference other aliases (expansion iterates; accidental cycles are broken by a depth cap)
- an alias name may not collide with a permission family name
- it is compile-time surface only: it expands during analysis and lowers into the existing permission model with no runtime object
- `-emit fmt` preserves the `alias Name = ...` declaration and still inlines simple one-statement `can <alias>:` blocks into `... can <alias>` form

> (`alias` replaces the former `grant` keyword, which had the same meaning.)

### Set-polymorphic effects and error sets

Permission and error sets can be ordinary inferred generic parameters, so a higher-order function propagates exactly its callback's effects/errors. `[permission E]` binds an effect-set parameter; `[errorset R]` binds an error-set parameter. Function types spell trailing effects as `func(...) -> T can[E] error[R]`.

```elisa
def run[permission E](f: func() -> void can[E]) -> void can[E]:
    f()

def applyDouble[errorset R](f: func() -> i64 error[R]) -> i64 error[R]:
    v: i64 = try f()
    return v * 2
```

Current rules:

- the function-type spelling is `func(Params) -> Ret can[...] error[...]`; error sets ride the error-union return type
- `[permission E]` / `[errorset R]` are inferred from the argument at each call site and substituted into the return type, so feeding an `IoErr` callback to `applyDouble` yields `error[IoErr]` and a `NetErr` callback yields `error[NetErr]`, with mismatches rejected
- addition is union-with-a-literal (`can[E, Console.Write]` = the callback's effects plus the function's own); there is no row polymorphism and no subtraction
- a callback that supplies nothing to bind reports `cannot infer permission/error-set parameter`
- monomorphization resolves the parameters to concrete sets per instantiation; effects are erased and error unions reach codegen as values, so there is no runtime cost beyond the existing error-union representation

## Named bundles (removed)

The param-aggregation family — `bundle Name implicit/explicit:`, `context Name:`, top-level and
local `params`/`parameters`/`args` packs, signature `def f(...) with Ctx`, `def f(use Pack)`,
call-site `use Pack(...)` / `f(...) with x = v`, the `with name = ...:` scope statement, and
`with args(...)` ambient-argument scopes — has been removed. There is one way to pass arguments:
ordinary parameters. Group recurring arguments with a plain `struct` and pass it explicitly.
(`with arena ...:` statements, fold `... with acc`, and query `... with <owner>` are unrelated
features and remain.)

## Brace destructuring, field punning, and record updates

Brace forms now work consistently across destructuring, literal construction, `is` patterns, `match` patterns, and record updates.

```elisa
struct Row:
    left: int
    right: int = 0
    flag: bool = false

def run(row: Row, flag: bool) -> int:
    let {left: first, right} = row
    built: Row = Row{left: first}
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
- struct fields may declare defaults with `field: Type = expr`; omitted named fields and omitted trailing positional fields are filled from those defaults
- default expressions are cloned and checked at each literal site, while `Type{...base, field: expr}` keeps omitted fields from the spread base instead of overwriting them with defaults
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

## Removed tree exact updates and `rewrite ... default`

The separate tree/member update surface has been removed. Use ordinary struct or
enum-variant helpers, and use `new[owner] Enum.Variant(...)` when the operation
allocates a fresh packed value.

```elisa
def make_binary(alloc: mutable Arena&, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
    return new[alloc] Lua.Expr.Binary(span: left.span + right.span, left: left, right: right)
```

Current rules:

- the removed `node` constructor spellings are rejected by the parser
- exact updates belong on ordinary structs or on explicit helper functions
- packed enum construction is a normal `produce` transform in the fact-core model
- `left.span + right.span` is the canonical span algebra form for span-like parser ranges; it first uses a visible `SpanLike` static-interface impl when present, then falls back to legacy helper functions such as `combine_span`
- the contextual `rewrite ... default` form should not be revived; write the
  traversal helper explicitly when a pass really needs one

## Removed nested tree categories

Nested tree categories have been folded into enum hierarchy and ordinary helper
style. Model sparse shapes with enum variants or nested enum names; keep
traversal relationships in helpers instead of introducing `child`, `children`,
or `link` tree-only declarations.

```elisa
enum LuaExpr:
    Unary(expr: LuaExpr)
    BinaryAdd(left: LuaExpr, right: LuaExpr)
    BinarySub(left: LuaExpr, right: LuaExpr)
    BinaryDiv(left: LuaExpr, right: LuaExpr)

def classify(node: Lua.Expr) -> i64:
    match node:
        Lua.Expr.BinaryAdd(left: _, right: _):
            return 1
        _:
            return 0
```
## Removed tree `visit` expressions

The tree-specific `visit value:` expression has been removed. Use ordinary
`match` on enum variants, or write a named helper when a traversal needs shared
state.

```elisa
def score(node: Lua.Expr) -> i64:
    match node:
        Lua.Expr.Nil(expr):
            expr.span
        Lua.Expr.Int(expr):
            expr.value
        Lua.Expr.Binary(expr):
            expr.left.span + expr.right.span
```

## Tree attributes and projected attribute sequences

Tree families can define computed field-like attributes with `attribute`.

```elisa
attribute Lua.Expr.checksum -> i64:
    Lua.Expr.Int(expr):
        return expr.value
    Lua.Expr.Binary(expr, left, right):
        return left.checksum + right.checksum

def checksum_of(node: Lua.Expr) -> i64:
    return node.checksum
```

Attributes may also be declared on a broader family root and may return an
error union when computing the attribute can fail.

```elisa
attribute Lua.Node.checksum -> i64 error[LuaFrontendError]:
    Lua.Expr.Binary(node, left, right):
        lua_binary_checksum(node.span, left.checksum, right.checksum)
    _:
        0
```

Projected attribute reads work on child sequences too:

```elisa
attribute Lua.Expr.node_count -> usize:
    Lua.Expr.Int(_):
        return 1
    Lua.Expr.Binary(expr, left, right):
        total: mutable usize = 1
        for child_count in children.node_count:
            total <- total + child_count
        return total

attribute Lua.Expr.is_leaf -> bool:
    Lua.Expr.Int(_):
        return true
    Lua.Expr.Binary(_):
        return false

def all_children_leaf(node: Lua.Expr) -> bool:
    return all(children(node).is_leaf)
```

Current rules:

- `attribute Receiver.name -> T:` declares a computed tree attribute on a tree category, exact variant, block, struct member, or family root type
- attribute bodies use the same visit-arm shape as `visit`
- attribute return types may be ordinary values or error unions
- `node.attr` reads the computed attribute through ordinary field-like syntax
- attribute arms may use the implicit `children` projected sequence when the matched member has structural children
- sequence projections such as `children.node_count` and `children(node).is_leaf` produce projected attribute sequences rather than eagerly materialized arrays
- projected attribute sequences work in loops, queries, and aggregators such as `all(...)` and `any(...)`
- tree attributes are computed views, not stored tree payload fields

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
- `extend grammar Name uses OtherGrammar:` is accepted when an extension block needs to import additional productions or grammar-scoped helpers at the extension site
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
- `$start` and `$end` inside a channel default refer to the first and last token values captured by the current production extent, so helpers like `span($start, $end)` and `$start.span + $end.span` can derive the production span without restating token names
- `channel name` at the top of a production body declares a production-local channel, which is preferred for helper tuple/struct results
- `channel_name <- term` assigns the current result of a grammar term into a declared grammar channel; this works both for nested `seq` terms and for ordinary production bodies, including direct expression assignments such as `span <- expr(tok.span)`
- `grammar type Name[...]` declares a reusable higher-order grammar combinator; this is the canonical replacement for older `grammarfn` declarations
- `grammar alias name = term`, `grammar alias name(params...) = term`, and their block forms give compile-time grammar terms reusable names, so call sites can say `args = call_args`, `args = expr_items(stop: RParenSync)`, or `statements = block_statement_items` while keeping the lower-level `separated_by(...)` or recovery shape available in the header
- `infix table Name(result):` hoists a reusable named-precedence ladder into grammar header scope so productions can say `result = infix(Name)` instead of inlining every level
- if a production falls through without an explicit `return` and its return type is either a named tuple or a known struct in the current scope, lowering synthesizes the success value from matching channel names
- struct-return synthesis only uses channels that correspond to struct fields; unrelated grammar-wide channels such as `node` are ignored instead of producing invalid helper struct literals
- if a nested `seq` arm ends by assigning a declared channel, the assignment is treated as channel state rather than the arm's semantic value; this keeps channel-synthesized helper productions from needing a trailing `pass`
- bare `pass` is a valid no-op grammar term, which is useful for empty productions or for channel-driven helper productions whose semantic result is carried entirely by channels
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
                new[alloc] Pascal.Decl.VarDecl(
                    span: name_token.span + header.type_token.span,
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
decls = flatrepeat variable_decl_group() until(.BEGIN, token(TokenKind.EOF))
args = delimited(.LPAREN, separated expression() by .COMMA until(.RPAREN, token(TokenKind.EOF)), .RPAREN, ParseMessageKey.ExpectedRightParen)?
maybe_name = .IDENT?
```

Current list-family terms:

- `term?` succeeds with an optional result
- `term?` is the canonical optional grammar spelling; `optional term` and `optional(term)` remain accepted for compatibility, but the formatter emits postfix `?`
- `term* until(...)` parses zero or more items and returns the collected list
- `term+ until(...)` parses one or more items and returns the collected list
- `flatrepeat term until(...)` parses zero or more list-producing items and flattens them into one accumulator
- the old `repeat ...`, `list ...`, and `[term] while token in tokens != [...]` spellings have been removed
- `separated term by sep until(...)` is the canonical separated-list form
- function-style `separated(term, sep, until(...))` remains accepted, but the formatter emits `separated term by sep until(...)`
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
- span algebra `left.span + right.span` resolves through a visible `protocol SpanLike` impl when available, and still recognizes legacy helper functions such as `combine_span` or `lua_span_union` for compatibility
- recovery and required terms depend on the grammar `cursor` declaration to restore or advance parser state correctly
- AST construction remains ordinary Elisa core code; use enum constructors or
  `new[alloc] Enum.Variant(span: ..., ...)` when exact allocation control is needed

## Filtered iterable `for`

Iterable loops are the idiomatic spelling for traversing data. Add an inline
`where` filter after the source expression when the loop should skip items
without adding a nested `if`. Use a numeric range only when the counter is the
thing being computed, the loop needs explicit bounds/stride, or the source has
no iterable category yet.

```elisa
for {left, right: value} in items where left != 0:
    total <- total + value

for token in tokens where token.kind == TokenKind.IDENT:
    names.push(token.NameId())

for decl in block.decls where Pascal.Decl.LabelDecl(labels):
    validate_labels(labels)

for decl in block.decls where decl is Pascal.Decl.LabelDecl(labels):
    validate_labels(labels)

for decl in block.decls where Pascal.Decl.LabelDecl:
    validate_label_decl(decl)

for decl in block.decls where Pascal.Decl.LabelDecl(labels) for label in labels:
    validate_label(label)
```

Current rules:

- prefer `for item in source:` for ordinary traversal; use `source.enumerate()`
  when both index and value are needed
- the binder runs before the filter, so the filter may reference destructured names such as `left`
- the loop binder may be a simple name, an irrefutable brace destructure pattern, or a typed variant filter pattern
- the filter may be an ordinary boolean expression or a pattern predicate
- `where name is Variant(payload)` is an explicit-subject spelling for the same pattern predicate and binds payload names in the guard, projection, or loop body
- multi-bind query and loop headers can narrow a selected subject with `where item is Variant(payload)`; payload names are scoped to the query expression or loop body
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

## Checked index recovery

Checked index recovery uses `get ... else ...` to make the bounds-check path explicit.

```elisa
def read(xs: darray[int], i: usize) -> int:
    return get xs[i] else 0
```

Current rules:

- `get source[index] else fallback` keeps the ordinary indexing semantics on success and yields `fallback` on the miss path
- the fallback expression must match the indexed element type
- the fallback surface is for value reads only; it is not an assignment target or a ref-binding surface
- the older `source[index] else fallback` spelling has been removed

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
- `defer function:` is currently only supported in the outermost function scope
- defer bodies are ordinary statement blocks and may capture surrounding locals
- defer bodies cannot `return` from the enclosing function
- a linear value scheduled for deferred consumption by a `defer function:` body cannot be consumed again inline
- `defer` is contextual, so ordinary identifiers like `defer_value` and calls like `defer(x)` still parse normally outside defer position

## SoA Rows And Dict Helpers

The old `store Name:` and `soa Name:` row-store declaration spellings have been removed. Use ordinary structs with explicit `darray[...]` fields, or `layout soa struct` only when the remaining compiler-known SoA layout is required.

```elisa
layout soa struct PendingGotoStore:
    name_key: u32
    depth: u32

def build(owner: Arena, values: mutable dict[cstr[key_shape], i64]&, key: cstr[key_shape]) -> i64:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.reserve(8)
        pending.push(1, 2)
        pending.push(3, 4)
        pending.truncate(1)

        slot = values.get_or_insert(key, 42)

        for {name_key, depth} in pending.rows():
            return name_key.i64() + depth.i64()

        pending.clear()
        return slot[0]
```

Current rules:

- `store Name:` and `soa Name:` declarations are parser errors
- `layout soa struct Name:` is the remaining compiler-known SoA declaration form
- `pending.reserve(n)` preallocates row capacity and requires a mutable store receiver
- `pending.push(a, b, ...)` appends one row in declared field order
- `pending.truncate(n)` keeps the first `n` rows
- `pending.clear()` removes all rows
- row-store growth helpers require an active `in <arena>:` scope
- plain row stores return the mutable store receiver from `reserve`, `push`, `truncate`, and `clear`, so these helpers chain like other mutable collection helpers
- `rows()` yields row values that work with ordinary field access and brace destructuring
- SoA store roots expose `store.count` as a readonly field-style item count
- field-style `store.rows` is accepted as a zero-argument alias for `store.rows()` and produces the same iterable row-view value
- row values from `rows()` are mutable field projections, so `row.name_key <- ...` and `row.depth <- ...` are allowed inside an ordinary `for row in pending.rows():` loop
- `rows()` is iterable, so ordinary helper surfaces such as `.enumerate()` and `rev(...)` compose with it
- SOA store roots also expose `store.valid(row_id)` to check whether a `RowId[Store]` still names a live row in that exact store root
- `for ref row in pending.rows():` is rejected; `rows()` is not an addressable array-like ref-binder source
- the current runtime-backed dict helper family also exposes `values.get(key)`, `values.put(key, value)`, `values.contains(key)`, `values.remove(key)`, `values.clear()`, and `values.reserve(n)` when matching helper overloads are in scope
- `values.get(key)` returns an optional mutable slot reference in the tested runtime-backed helper family, so `if values.get(key) is found: ...` is the common read/update shape
- `values.entry(key)` exposes entry-oriented helpers such as `.found`, `.value`, `.insert(value)`, and `.get_or_insert(value)`
- the old block-default form `values.get_or_insert(key): ...` has been removed; pass the default value explicitly as `values.get_or_insert(key, value)`
- the old entry block-default form `values.entry(key).get_or_insert(): ...` has been removed; pass the default value explicitly as `values.entry(key).get_or_insert(value)`
- the generic syntax parses for more than one key family, but the current runtime-backed helper surface is primarily validated for `dict[cstr[key_shape], V]` unless matching helper overloads are supplied
- packed and row store values should be read through the fact-core lens: mutable local stores carry store-dependency facts, `freeze(move store)` consumes the local store and rebases handles onto frozen-store facts, and row scans may add optimization facts such as readonly, contiguous, or exact extent

## `clone[...]` builtin

`clone[target](value)` is the current deep-copy surface for owner-backed dynamic
arrays, packed enum values, and other cloneable aggregate shapes.

```elisa
enum LuaExpr:
    Int(span: i64, value: i64)

struct LuaBlock:
    span: i64
    items: darray[LuaExpr]

struct Pair:
    items: darray[u32]
    root: LuaBlock

def clone_pair(owner: mutable Arena&, source_items: view[u32], block: Lua.Block) -> Pair:
    can Abort.Panic, Memory.Allocate:
        in owner:
            return Pair{
                items: clone[darray[u32]](source_items),
                root: clone[Lua.Block](block),
            }
```

Bounded string views can also be persisted into owned bytes:

```elisa
def persist(owner: mutable Arena&, text: sview) -> dstr:
    can Abort.Panic, Memory.Allocate:
        in owner:
            return clone[dstr](text)
```

Current rules:

- `clone[darray[T]](view_or_array)` deep-copies into an owner-backed dynamic array
- `clone[dstr](text)` and `clone[darray[u8]](text)` are the current owner-backed string-persistence surfaces for `sview`
- tree categories, exact variants, blocks, and other tree members can be cloned with `clone[Tree.Member](value)`
- ordinary structs may be cloned when their fields themselves support the required clone or copy path
- allocating clone targets require an active `in owner:` scope
- reference targets such as `clone[i64&](value)` are rejected in v1

## Pool scopes and `parallel for`

The current explicit parallel loop surface is pool-scoped rather than implicit.

```elisa
def visit(frozen: Expr.Store[Frozen]) -> void can[Pool.Submit, Pool.WaitAll]:
    pool workers(2):
        parallel for node in frozen:
            pass

def walk_tags(tags: view[Expr.Tag]) -> void can[Pool.Submit, Pool.WaitAll]:
    pool workers(2):
        parallel for tag at i in tags:
            if tag == Expr.Tag.Add:
                _ = i

def sum_chunks(chunks: ChunksExactView[i32]) -> void can[Pool.Submit, Pool.WaitAll]:
    pool workers(2):
        parallel for chunk in chunks:
            _ = chunk[0] + chunk[1]
```

Current rules:

- `pool workers(count):` introduces the required enclosing pool scope
- `parallel for item in source:` is the basic form
- `parallel for item at index in source:` adds an explicit index binder
- current sources must be either a frozen packed store or a readonly dense view whose facts prove readonly, contiguous, unit-stride, and exact extent
- readonly tag views such as `frozen.tags` and readonly `ChunksExactView[...]` values are accepted through that same dense-view path
- the body cannot mutate an outer binding such as `total <- total + 1`
- the body cannot `return` from the enclosing function
- the body cannot nest another `parallel for`
- the body cannot destroy, mark, restore, or reset an outer region, and it cannot restore from an outer checkpoint
- captured outer values must still satisfy the compiler's existing thread-transfer checks
- this is the current implemented parallel loop feature, not just a proposal placeholder

Strict-concurrency migration notes:

- `pool` / `parallel for` is the preferred current structured surface for
  ordinary data-parallel work
- direct `spawn1`, `pool_submit1`, `detach`, raw thread helpers, and raw
  condition-variable helpers are low-level compatibility/runtime surfaces
- `predicate_wait(cv, move guard, predicate)`, `predicate_notify_one(cv)`, and
  `predicate_notify_all(cv)` are the current stdlib replacement surface for raw
  condition-variable waits/notifications; domain-specific wrappers should build
  on them and expose the protected-state predicate they wait for
- direct atomic helpers over `atomic[T]` (`load`, `store`, `exchange`,
  `compare_exchange`, RMW helpers, and `fence`) are also low-level surfaces;
  strict user code should hide them behind named protocol wrappers
- `AtomicCell[T]` is the current stdlib wrapper for ordinary atomic state:
  use `atomic_cell(value)`, `atomic_load_acquire`, `atomic_store_release`,
  `atomic_exchange_acqrel`, `atomic_compare_exchange_acqrel`, and the i64
  `atomic_fetch_add_acqrel` / `atomic_fetch_sub_acqrel` helpers instead of
  choosing raw memory orders at every call site
- strict-mode direction is to keep those raw calls available for trusted
  wrappers while nudging user code toward structured task scopes, linear
  escaped handles, typed predicate waits, bounded queues, and domain-protected
  state
- current semantic analysis reports the legacy raw concurrency calls as
  deprecations so projects can start auditing them before promotion to hard
  strict-mode errors; the diagnostics name the preferred wrappers such as
  `predicate_wait`, `predicate_notify_one`, `AtomicCell[T]`, `nursery:`,
  pool scopes, and task groups
- `AnalyzeOptions.EnforceStrictConcurrency`, exposed as CLI flag
  `-Wconcurrency`, currently performs that promotion for the legacy raw
  concurrency calls while leaving unrelated deprecations as migration warnings
- `-Wperf` is orthogonal: it turns performance-friction diagnostics into hard
  errors, including concurrency performance hazards such as spawning a fresh OS
  thread in each loop iteration instead of using a persistent pool/nursery
  shape, or acquiring a mutex once per loop iteration instead of batching,
  sharding, or reducing locally; atomic RMW/CAS hot loops get the same treatment
  because they often serialize on one cache line
- `-Wstrict` is the umbrella preset for shipped strict code: it enables unsafe
  permission enforcement, progress-safety analysis, `-Wconcurrency`, and
  `-Wperf` together; project targets can request the same policy with
  `warnings.strict: true`

## Tuple-bind statements

Tuple values can be unpacked directly into local names with a statement-form tuple binder. Use `=` when the binder introduces new locals, and `<-` when it rebinds existing locals from another tuple-shaped value.

```elisa
node, checksum = built

left, right <- pair
```

The same binder shape is also available in tuple-producing loops and queries:

```elisa
for left, right in pairs where left < right:
    total <- total + left + right
```

Current rules:

- `a, b = value` declares fresh locals from a tuple-shaped source
- `a, b <- value` reassigns existing locals from a tuple-shaped source
- tuple binders participate in the same name-based filtering surface used by `for ... where ...` and `each ... where ...`

## Removed cascade blocks and expressions

The old receiver-oriented `cascade` shorthand has been removed. Write the target explicitly or introduce a local alias when several statements share the same receiver.

```elisa
report.inner.value <- value
report.flag <- true

return row.ref_count != 0
```

Current rules:

- `cascade target:` and `cascade target => expr` are rejected by the parser
- leading-dot member shorthand is no longer available through cascade rewriting

## Lambda literals

Anonymous function literals now have dedicated surface syntax.

```elisa
def build() -> func(i64) -> i64:
    return lambda (value: i64) -> i64:
        return value + 1

def fast_build() -> func(i64) -> i64:
    return lambda (value: i64) => value + 1

def capture(offset: i64) -> func(i64) -> i64:
    return λ value: value + offset
```

Current rules:

- both `lambda` and `λ` are accepted spellings
- lambdas may use a block body or a single expression body
- `lambda (params) => expr` is accepted as an expression-body spelling; formatting canonicalizes it to the ordinary inline-expression form
- shorthand parameter forms like `λ value: ...` rely on the expected function type to provide parameter typing
- block-bodied lambdas require either an explicit return type or a contextual function type
- lambdas capture surrounding locals and may return closures
- `lambda` is contextual, so a parameter or local named `lambda` still parses as an identifier outside lambda position

## Tree `fold`

`fold value as Root into T:` is the bottom-up tree reduction surface. Each arm
matches an exact tree member and receives folded child results rather than raw
child handles.

```elisa
def score(node: Lua.Expr) -> i64:
    return fold node as Lua.Node into i64:
        Lua.Expr.Nil(expr, children):
            expr.span + children.len.i64()
        Lua.Expr.Int(expr, children):
            expr.value + children.len.i64()
        Lua.Expr.Call(expr, callee, args: arg_values):
            callee + arg_values.len.i64() + expr.span
        Lua.Expr.Binary(expr, left, right):
            left + right + expr.span
```

Optional and sequence child fields preserve their shape in the folded bindings:

```elisa
def score(node: Lua.Stmt) -> i64:
    return fold node as Lua.Node into i64:
        Lua.Block(block, children):
            children.len.i64() + block.span
        Lua.Stmt.IfStmt(stmt, condition, then_block, elseifs: elseif_values, else_block):
            optional_i64_value(else_block) + condition + then_block + elseif_values.len.i64() + stmt.span
        Lua.Stmt.NumericFor(stmt, start, limit, step, body):
            optional_i64_value(step) + start + limit + body + stmt.name_index.i64()
```

Current rules:

- `fold value as Root into T:` dispatches over the chosen tree root or category and produces a `T`
- the first arm binder such as `expr`, `stmt`, or `block` is the exact current tree member
- a trailing binder like `children` receives the folded direct-child result sequence when requested
- named child-result bindings such as `args: arg_values` bind one child field's folded result under a chosen local name
- optional child fields produce optional folded results
- sequence child fields produce folded sequences whose element type is the fold result type
- non-sequence child fields produce one folded result value each

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

def simplify_binary(node: Lua.Expr) -> Lua.Expr:
    in perm:
        return rewrite node as Lua.Expr default:
            Lua.Expr.Binary(expr, left, right):
                default{span = expr.span, left, right}
```

Current rules:

- `rewrite value as Root:` is fold-backed, but it specializes child-result bindings to the original child edge types instead of forcing every rewritten child to have one uniform result type
- `rewrite value as Root default:` installs an implicit pass-through default for omitted exact variants of the chosen rewrite root
- arm heads, guards, exact tree targets, variant targets, wildcard arms, and named child-result bindings follow the existing `fold` arm rules
- named child bindings such as `left` and `right` are the already-rewritten child results
- inside an exact rewrite arm, bare `default` rebuilds the current exact member using the already rewritten child results
- `default{field = value, other}` rebuilds the current exact member while overriding selected fields or reusing same-named bindings
- use a family root such as `Lua.Node` or `ATPLSyntax.Node` when a category has heterogeneous structural children such as expressions, statements, and blocks
- `default` and `default{...}` are only allowed inside exact tree rewrite arms
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

- value-level `as` casts such as `expr as T` are no longer accepted; `as` is reserved for binding, aliasing, and related declaration surfaces
- `.cast[T]` is the explicit cast surface in expression position
- `.ref[T&]` is the explicit lvalue/reference reinterpretation surface
- postfix shorthand like `op.i64()` dispatches to a visible exact `__cast__(value: Source) -> Target` hook when one exists
- prefix type-constructor shorthand like `i64(op)` uses the same cast path and the same exact `__cast__` hook lookup as `op.i64()`
- optional postfix shorthand like `text.int?()` dispatches to a visible exact `__cast__(value: Source) -> int?` hook when one exists
- ordinary explicit `.cast[T]` conversions continue to use normal cast rules rather than hook dispatch
- the postfix hook surface is intentionally exact-source/exact-target rather than a broad overload search
- legacy postfix reference-cast shorthand such as `bits.u8&()` is no longer supported; use `.cast[T&]` when retargeting an existing pointer/reference-like value explicitly, or `.ref[T&]` when reinterpreting an lvalue slot
- legacy expression-arrow casts such as `value -> T` are no longer supported; use `.cast[T]`, hook-backed `T(value)` / `value.T()`, or other current conversion surfaces as appropriate
- legacy `.cast[T]()` call-style syntax is no longer supported; use `.cast[T]`

That distinction matters when reading code: `value.cast[T]` is the explicit cast surface, `slot.ref[T&]` is a reference reinterpretation, `value.T()` and `T(value)` are hook-backed conversion shorthands, and `value.T?()` is the same hook mechanism returning an optional.

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
values: darray[int] @owner = [1, 2, 3]
mapped: darray[int] @owner = [value + 1 for value in values if value > 0]
prepended: darray[int] @owner = [first, ...rest]
combined: darray[int] @owner = [...left, ...right]
```

The direct owner form is only for arena-backed dynamic array literals and
comprehensions. Fixed array literals remain pure values:

```elisa
values: int[3] = [1, 2, 3]
```

Region-owned structs declare their region in the bracket list —
`struct Expr[@owner]:` (several with `[@a, @b]`). Use sites carry
the region with the `@r` suffix — `Expr @scratch` / `Expr @owner` — naming a region,
region parameter, or visible `Arena` value (see
[68-region-memory-model.md §5](68-region-memory-model.md)).

When the struct also has type parameters, the brackets hold the type arguments and
the `@r` suffix carries the region.

```elisa
struct Box[T, @owner]:
    value: T
    next: Box[T]&? @owner

box: Box[i64] @scratch = Box{
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
