# Tree capabilities and interface cleanup

This note records the canonical direction for keeping Elisa core low-level at the bottom and DSL-like at the top without splitting the language into unrelated feature islands.

The guiding rule is the fact-core rule from `22-value-fact-core.md`: tree and
grammar sugar may hide constructor boilerplate, but it must not hide fact
transitions. `node[...]` is a producing operation tied to an allocator/store;
`freeze(move store)` is consume + rebase + produce; parser recovery and region
rollback can invalidate facts derived from speculative allocations.

## Interface spelling

Compile-time interfaces use `protocol` for capability-style contracts. The older `static interface` spelling is deprecated but remains accepted as a compatibility alias.

```elisacore
protocol ParseBuilder:
    type ExprNode
    type StmtNode

    def make_name(span: Span, name_id: u32) -> ExprNode
    def make_binary(span: Span, left: ExprNode, right: ExprNode) -> ExprNode
```

`protocol` is compile-time only; it formats as `protocol` and uses the same implementation machinery as the deprecated `static interface` spelling. The old `interface Name:` spelling remains accepted as a compatibility alias.

Runtime/dynamic interfaces are intentionally left unclaimed by this spelling. If Elisa core grows vtable-like runtime interfaces later, they should use a separate explicit feature instead of overloading the current static system.

## Ambient inputs

Parser and tree code should prefer implicit bundles for ambient dependencies such as the active parser, allocator, intern table, diagnostics sink, or concrete builder.

```elisacore
bundle ParseCtx implicit:
    parser: mutable Parser&
    alloc: mutable Arena&

def parse_atom[B: ParseBuilder]() with ParseCtx -> B.ExprNode:
    token: Token = parser.current_token()
    return B.make_name(token.span, token.lexeme_key)
```

This keeps low-level control available: callers can still pass or override the bundle explicitly.

```elisacore
return parse_atom[AstBuilder]() with ParseCtx(parser:, alloc:)
return parse_atom[AstBuilder]() with ParseCtx(.., alloc = scratch.ref[mutable Arena&])
```

Explicit bundles remain part of the same model, but they are call-shaping bundles rather than ambient ones. Use `args Name:` for local packs that are only meaningful inside a narrow parser/helper region.

```elisacore
bundle Pair explicit:
    left: i64
    right: i64 = 7

def add(use Pair) -> i64:
    return left + right

def build(left: i64) -> i64:
    args LocalPair:
        left: i64 = left
        right: i64 = 9
    return add(use LocalPair)
```

When a call needs implicit parameters and no active implicit scope supplies them, the compiler may use same-named in-scope values as the ambient arguments. This is especially useful for generated parser functions that already have a low-level `alloc` parameter but should be able to call higher-level helpers declared `with AllocCtx`.

```elisacore
bundle AllocCtx implicit:
    alloc: mutable Arena&

def make_name_expr(token: Token) with AllocCtx -> Pascal.Expr:
    return node[span = token.span] Pascal.Expr.Name(name_id: token.lexeme_key)

def generated_parser_step(alloc: mutable Arena&, token: Token) -> Pascal.Expr:
    return make_name_expr(token)
```

## Tree capability shape

Tree frontends should compose small protocols instead of growing one monolithic builder contract when possible.

```elisacore
protocol SpanLike:
    type Range
    def combine(left: Range, right: Range) -> Range

protocol TreeBuilder:
    type Span
    type ExprNode

    def make_invalid(span: Span) -> ExprNode
    def make_binary(span: Span, left: ExprNode, right: ExprNode) -> ExprNode
```

The language can layer compact DSL syntax over those contracts:

```elisacore
span: Span = left.span + right.span
return node[span = span] Pascal.Expr.Binary(left: left, right: right)
```

With a visible `SpanLike` impl for `Span`, that lowers to static-interface dispatch. The low-level equivalent remains available:

```elisacore
span: Span = combine_span(left.span, right.span)
return new[alloc] Pascal.Expr.Binary(span: span, left: left, right: right)
```

## Recommended parser style

Use the pyramid deliberately:

- low-level functions keep explicit parameters when allocation ownership, lifetime, or mutation must be obvious
- helper functions use `with ParseCtx` or a smaller `with AllocCtx` when allocator threading is mechanical
- grammar actions use canonical tree syntax such as `node[span = ...] Tree.Member(...)`
- grammar channels are a good fit for parser result shapes; prefer named tuple returns for local helper results, and use structs when the shape is shared more broadly. Stateful grammar lowering can synthesize either tuple-shaped or struct-shaped success values from channel names.
- production-local channels are preferred for helper tuple/struct productions so grammar-wide `node`/`span` channels stay focused on tree productions
- reusable parser combinators that construct grammar fragments should use `grammar type` instead of plain `grammarfn`
- grammar sequence results use `left + right` for list-producing parser values instead of one-off merge helpers
- grammar list wrappers use `singleton[T](value)` instead of one-off helpers that allocate a `darray`, push one value, and return it
- parser branches snapshot cursor-dependent values before trying alternatives that can consume input
- helper productions that return plain structs can now live inside tree grammars more comfortably: struct synthesis requires channel names to match struct fields, so unrelated grammar-wide channels such as `node` do not accidentally shape non-tree results
- when parser code already has a list value in hand, prefer a list comprehension over a one-off helper function that only loops, filters, and returns a `darray`
- `protocol` expresses parser-builder variability, not runtime dispatch
