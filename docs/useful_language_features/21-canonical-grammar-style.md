# Canonical grammar style

This note is the house style for llcontext grammar/parser implementations.

The goal is not to hide the low-level language. The goal is to make the common parser/tree/compiler cases read like a compact grammar DSL, while keeping every escape hatch available when allocation, recovery, or control flow needs to be explicit.

## Shape

Prefer splitting grammars by reusable concern:

```context
grammar PascalListGrammar with PascalParserEnv:
    grammar type separated_by[T](item: grammar -> T, stop: tokenset, sep: grammar = .COMMA) -> grammar -> darray[T]:
        separated item by sep until(stop)

grammar PascalRecoveryGrammar with PascalTreeGrammarEnv:
    grammar type recovered[T](item: grammar -> T, message: expr, stop: tokenset, fallback: expr) -> grammar -> T:
        item recover(message, until(stop), fallback)

grammar PascalExprGrammar with PascalTreeGrammarEnv uses PascalListGrammar, PascalRecoveryGrammar:
    ...
```

Use `grammar type` for reusable parser constructors. Keep `grammarfn` for compatibility tests and old examples only.

Prefer direct named calls for grammar constructors:

```context
args = separated_by(item: expression(), stop: RParenSync)
condition = recovered(item: condition(), message: expr(ExpectedCondition), stop: ConditionSync, fallback: expr(invalid_expr()))
```

Avoid the older explicit spelling unless you intentionally want to emphasize expansion or positional experiments:

```context
args = apply separated_by(item: expression(), stop: RParenSync)
```

Use `grammar alias` when a grammar fragment has a domain name that should appear at the use site:

```context
grammar PascalStmtGrammar with PascalTreeGrammarEnv uses PascalListGrammar:
    tokenset BlockEndSync:
        END
        token(TokenKind.EOF)

    grammar alias block_statement_items = separated_by(item: statement(), stop: BlockEndSync, sep: .SEMICOLON)

    compound_statement() -> Pascal.Stmt:
        begin_token = .BEGIN
        statements = block_statement_items
        end_token = required(.END, ParseMessageKey.ExpectedBlockEnd)
        return make_compound_stmt(begin_token, statements, end_token)
```

Use the block form when the alias names a larger fragment:

```context
grammar ATPLPostfix:
    grammar alias member_postfix_step:
        attempt(try self.try_parse_postfix_member(owner, base))

    postfix_step(self: mutable ATPLParser&, owner: mutable Arena&, base: ATPLExpr.Expr) -> ATPLExpr.Expr error[ATPLFrontendError]:
        step = member_postfix_step
        return step
```

When a block alias contains several terms, the body itself is the sequence:

```context
grammar PascalParenGrammar:
    grammar alias parenthesized_atom:
        .LPAREN
        atom()
        .RPAREN
```

Aliases are compile-time grammar terms. They are imported through `uses`, may compose with `grammar type` constructors, and expand before lowering. Use them for named parser concepts such as `call_args`, `program_decls`, `tuple_tail_items`, and `block_statement_items`; do not use them just to hide a one-off expression that is clearer inline.

Aliases also compose inside `infix table` declarations, which keeps expression tables from turning into dense one-line nests:

```context
grammar PascalExprGrammar:
    grammar alias atom_choice:
        choice:
            prefix(.PLUS, .MINUS) atom() -> make_unary_expr(op, operand)
            delimited(.LPAREN, expression(), .RPAREN, ExpectedRightParen)
            name_atom()
            integer_atom()

    infix table ExprTable(additive):
        atom = atom_choice
        left additive(left = atom()):
            op = .PLUS | .MINUS -> make_binary_expr(left, op, right)
```

## Tokens And Sets

Use grouped token aliases:

```context
token:
    IDENT
    INTEGER
    LPAREN "("
    RPAREN ")"
    COMMA ","
```

Use `tokenset` for sync boundaries and repeated list stops:

```context
tokenset RParenSync:
    RPAREN
    token(TokenKind.EOF)

tokenset StatementSync:
    SEMICOLON
    END
    ELSE
    token(TokenKind.EOF)
```

Prefer semantic names such as `StatementSync`, `BlockEndSync`, `RParenSync`, and `DeclarationSync` over repeating raw token lists at call sites.

## Channels

Use grammar-wide channels for fields that are genuinely shared by most productions in the grammar:

```context
grammar PascalExprGrammar with PascalTreeGrammarEnv:
    channel span: Span
    channel node
```

Use production-local channels for tuple/struct helper results:

```context
assignment_spec() -> (name_id: PascalNameId, value: Pascal.Expr, span: Span):
    channel name_id
    channel value
    channel span
    .IDENT(name_token)
    required(.ASSIGN, ParseMessageKey.ExpectedAssignmentOperator)
    name_id <- expr(name_token.lexeme_key)
    value <- expression()
    span <- expr(name_token.span + value.span)
    pass
```

Let channel synthesis build the final tuple/struct when the result shape is just the named channels.

## Lists

Use the list-family terms instead of hand-written allocation helpers:

```context
decls = const_decls + type_decls + var_decls
empty_decls = empty[Pascal.Decl]
one_decl = singleton[Pascal.Decl](decl)
ids = maplist[PascalNameId](names, token, token.lexeme_key)
decls = flatmaplist[Pascal.Decl](names, token, build_decls(token))
```

Use `empty[T]` for no items, `singleton[T](value)` for one item, `maplist[T]` for one result per item, and `flatmaplist[T]` for zero-or-more results per item.

Prefer grammar composition over helper functions whose only job is list plumbing:

```context
declarations() -> darray[Pascal.Decl]:
    kind = expr(state.current_token().kind)
    const_decls = when(kind == TokenKind.CONST, const_prefixed_decl_sections(), empty[Pascal.Decl])
    type_decls = when(kind == TokenKind.TYPE, type_prefixed_decl_sections(), empty[Pascal.Decl])
    var_decls = when(kind == TokenKind.VAR, variable_decl_section(), empty[Pascal.Decl])
    node <- const_decls + type_decls + var_decls
    return node
```

## Tree Construction

Use tree constructor sugar for ordinary AST construction:

```context
node <- expr(node[span = left.span + right.span] Pascal.Expr.Binary(left: left, op: op, right: right))
```

Use span algebra:

```context
span <- expr(start_token.span + end_token.span)
```

Avoid new parser code that writes:

```context
span <- expr(combine_span(start_token.span, end_token.span))
node <- expr(node[span = combine_span(left.span, right.span)] Pascal.Expr.Binary(left: left, right: right))
```

Keep low-level constructors available for places where allocation, storage, or field order must be explicit.

## Sequences

Use block `seq:` when a sequence owns multiple meaningful steps:

```context
choice:
    seq:
        .IDENT(token)
        expr(make_name_expr(token))
    seq:
        .INTEGER(token)
        expr(make_integer_expr(token))
```

For reusable transformed atoms, prefer a small helper production over nesting compact `seq(...)` inside a large expression:

```context
name_atom() -> Pascal.Expr:
    .IDENT(token)
    return make_name_expr(token)

infix table ExprTable(additive):
    atom = choice(prefix(.MINUS) atom() -> make_unary_expr(op, operand), name_atom(), integer_atom(), string_atom())
```

Inline `seq(...)` is still valid for compact nested positions, but it should not become the default style for real productions.

## Optional And Recovery

Use postfix `?` for optional grammar terms:

```context
else_stmt = else_clause()?
args = delimited(.LPAREN, separated_by(item: expression(), stop: RParenSync), .RPAREN, ExpectedRightParen)?
```

Use direct recovery constructors for repeated recovery shapes:

```context
grammar type recovered_expr(stop: tokenset) -> grammar -> SML.Expr:
    recovered(item: expression(), message: expr(SMLParseMessageKey.ExpectedExpression), stop: stop, fallback: expr(invalid_sml_expr_at(state.current_token().span)))

if_expr() -> SML.Expr:
    if_token = .IF
    condition = recovered_expr(stop: IfConditionSync)
    required(.THEN, SMLParseMessageKey.ExpectedThen)
    then_expr = recovered_expr(stop: ThenExprSync)
    required(.ELSE, SMLParseMessageKey.ExpectedElse)
    else_expr = recovered_expr(stop: EndSync)
    return node[span = if_token.span + else_expr.span] SML.Expr.IfExpr(condition: condition, then_expr: then_expr, else_expr: else_expr)
```

## Protocols

Use `protocol` for compile-time capability contracts:

```context
protocol SpanLike:
    type Range
    def combine(left: Range, right: Range) -> Range
```

Use `static interface` only when deliberately showing the low-level spelling or compatibility behavior.

## When To Drop Down

Drop to ordinary llcontext code when:

- Allocation strategy matters more than grammar readability.
- A parser helper needs loops, mutation, or several non-grammar checks.
- Error handling needs explicit `try` or custom error mapping.
- A tiny helper production would obscure rather than clarify the grammar.

The pyramid rule is: write the common grammar at the DSL layer, then descend freely when the implementation needs fine-grained control.
