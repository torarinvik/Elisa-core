# Tree capabilities and interface cleanup

This note records the canonical direction for keeping llcontext low-level at the bottom and DSL-like at the top without splitting the language into unrelated feature islands.

## Interface spelling

Compile-time interfaces are canonicalized as `static interface`.

```llcontext
static interface ParseBuilder:
    type ExprNode
    type StmtNode

    def make_name(span: Span, name_id: u32) -> ExprNode
    def make_binary(span: Span, left: ExprNode, right: ExprNode) -> ExprNode
```

The old `interface Name:` spelling remains accepted as a compatibility alias, but formatter output and new examples should use `static interface Name:`.

Runtime/dynamic interfaces are intentionally left unclaimed by this spelling. If llcontext grows vtable-like runtime interfaces later, they should use a separate explicit feature instead of overloading the current static system.

## Ambient inputs

Parser and tree code should prefer implicit bundles for ambient dependencies such as the active parser, allocator, intern table, diagnostics sink, or concrete builder.

```llcontext
bundle ParseCtx implicit:
    parser: mutable Parser&
    alloc: mutable Arena&

def parse_atom[B: ParseBuilder]() with ParseCtx -> B.ExprNode:
    token: Token = parser.current_token()
    return B.make_name(token.span, token.lexeme_key)
```

This keeps low-level control available: callers can still pass or override the bundle explicitly.

```llcontext
return parse_atom[AstBuilder]() with ParseCtx(parser:, alloc:)
return parse_atom[AstBuilder]() with ParseCtx(.., alloc = scratch.ref[mutable Arena&])
```

When a call needs implicit parameters and no active implicit scope supplies them, the compiler may use same-named in-scope values as the ambient arguments. This is especially useful for generated parser functions that already have a low-level `alloc` parameter but should be able to call higher-level helpers declared `with AllocCtx`.

```llcontext
bundle AllocCtx implicit:
    alloc: mutable Arena&

def make_name_expr(token: Token) with AllocCtx -> Pascal.Expr:
    return node[span = token.span] Pascal.Expr.Name(name_id: token.lexeme_key)

def generated_parser_step(alloc: mutable Arena&, token: Token) -> Pascal.Expr:
    return make_name_expr(token)
```

## Tree capability shape

Tree frontends should compose small static interfaces instead of growing one monolithic builder contract when possible.

```llcontext
static interface SpanLike:
    type Span
    def combine(left: Span, right: Span) -> Span

static interface TreeBuilder:
    type Span
    type ExprNode

    def make_invalid(span: Span) -> ExprNode
    def make_binary(span: Span, left: ExprNode, right: ExprNode) -> ExprNode
```

The language can layer compact DSL syntax over those contracts:

```llcontext
span: Span = left.span + right.span
return node[span = span] Pascal.Expr.Binary(left: left, right: right)
```

The low-level equivalent remains available:

```llcontext
span: Span = combine_span(left.span, right.span)
return new[alloc] Pascal.Expr.Binary(span: span, left: left, right: right)
```

## Recommended parser style

Use the pyramid deliberately:

- low-level functions keep explicit parameters when allocation ownership, lifetime, or mutation must be obvious
- helper functions use `with ParseCtx` or a smaller `with AllocCtx` when allocator threading is mechanical
- grammar actions use canonical tree syntax such as `node[span = ...] Tree.Member(...)`
- static interfaces express parser-builder variability, not runtime dispatch
