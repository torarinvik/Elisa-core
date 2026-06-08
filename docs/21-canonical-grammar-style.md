# Canonical grammar style

This note is the house style for Elisa core grammar/parser implementations.

The goal is not to hide the low-level language. The goal is to make the common parser/tree/compiler cases read like a compact grammar DSL, while keeping every escape hatch available when allocation, recovery, or control flow needs to be explicit.

## Shape

Prefer splitting grammars by reusable concern:

```elisa
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

```elisa
args = separated_by(item: expression(), stop: RParenSync)
condition = recovered(item: condition(), message: expr(ExpectedCondition), stop: ConditionSync, fallback: expr(invalid_expr()))
```

When the first argument is the grammar fragment being transformed, prefer the pipeline form:

```elisa
args = expression() |> separated_by(stop: RParenSync)
condition = condition() |> recovered(message: expr(ExpectedCondition), stop: ConditionSync, fallback: expr(invalid_expr()))
```

Pipelines are compile-time grammar application sugar. They pass the left-hand term as the first positional argument to the grammar constructor and keep the remaining arguments named.

Avoid the older explicit spelling unless you intentionally want to emphasize expansion or positional experiments:

```elisa
args = apply separated_by(item: expression(), stop: RParenSync)
```

Use `grammar alias` when a grammar fragment has a domain name that should appear at the use site:

```elisa
grammar PascalStmtGrammar with PascalTreeGrammarEnv uses PascalListGrammar:
    tokenset BlockEndSync:
        END
        token(TokenKind.EOF)

    grammar alias block_statement_items = statement() |> separated_by(stop: BlockEndSync, sep: .SEMICOLON)

    compound_statement() -> Pascal.Stmt:
        begin_token = .BEGIN
        statements = block_statement_items
        end_token = required(.END, ParseMessageKey.ExpectedBlockEnd)
        return make_compound_stmt(begin_token, statements, end_token)
```

Use a parameterized alias when the named parser concept needs local stop sets, separators, or recovery expressions, but should still read as a domain construct at the call site:

```elisa
grammar PascalExprGrammar with PascalTreeGrammarEnv uses PascalListGrammar:
    grammar alias expr_items(stop: tokenset, sep: grammar = .COMMA):
        expression() |> separated_by(stop: stop, sep: sep)

    call_args() -> darray[Pascal.Expr]:
        args = expr_items(stop: RParenSync)
        return args
```

Prefer `grammar type` when the abstraction is a reusable constructor such as `separated_by` or `recovered`. Prefer a parameterized `grammar alias` when the abstraction is a partial specialization with parser-domain meaning such as `expr_items`, `pattern_items`, or `statement_list_until`.

Use the block form when the alias names a larger fragment:

```elisa
grammar ATPLPostfix:
    grammar alias member_postfix_step:
        attempt(try self.try_parse_postfix_member(owner, base))

    postfix_step(self: mutable ATPLParser&, owner: mutable Arena&, base: ATPLExpr.Expr) -> ATPLExpr.Expr error[ATPLFrontendError]:
        step = member_postfix_step
        return step
```

When a block alias contains several terms, the body itself is the sequence:

```elisa
grammar PascalParenGrammar:
    grammar alias parenthesized_atom:
        .LPAREN
        atom()
        .RPAREN
```

Aliases are compile-time grammar terms. They are imported through `uses`, may compose with `grammar type` constructors, and expand before lowering. Use them for named parser concepts such as `call_args`, `program_decls`, `tuple_tail_items`, and `block_statement_items`; do not use them just to hide a one-off expression that is clearer inline.

Aliases also compose inside `infix table` declarations, which keeps expression tables from turning into dense one-line nests:

```elisa
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

```elisa
token:
    IDENT
    INTEGER
    LPAREN "("
    RPAREN ")"
    COMMA ","
```

Use `tokenset` for sync boundaries and repeated list stops:

```elisa
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

Use `token family` when the same token union names a domain atom rather than a sync boundary:

```elisa
token family OperatorName:
    IDENT
    CONSTR_IDENT
    SYMBOL_IDENT
    PLUS
    MINUS
    EQ

operator_name() -> Token:
    token = required(OperatorName, ParseMessageKey.ExpectedOperatorName)
    return token
```

Prefer names that describe the parser concept. `StatementSync` and `BlockEndSync` should stay `tokenset`; `OperatorName` and `TypeNameStart` should be token families.

## Channels

Use grammar-wide channels for fields that are genuinely shared by most productions in the grammar:

```elisa
grammar PascalExprGrammar with PascalTreeGrammarEnv:
    channel span: Span
    channel node
```

Use production-local channels for tuple/struct helper results:

```elisa
assignment_spec() -> (name_id: NameId, value: Pascal.Expr, span: Span):
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

Use comprehension syntax and list composition instead of hand-written allocation helpers:

```elisa
decls = const_decls + type_decls + var_decls
empty_decls = empty[Pascal.Decl]
one_decl = singleton[Pascal.Decl](decl)
ids = [token.lexeme_key for token in names]
decls = [build_decl(token) for token in names if token.kind == TokenKind.IDENT]
decls: darray[Pascal.Decl] @alloc = [build_decl(token) for token in names if token.kind == TokenKind.IDENT]
```

Use `empty[T]` for no items, `singleton[T](value)` for one item, `[value for item in items]` for mapped lists, and `[value for item in items if cond]` for filtered list construction.

Use iterable sources as the default traversal shape in grammar helper code too.
Prefer comprehensions and `for item in items:` over `for i in 0..<items.count:`
when walking parsed lists, token lists, AST child lists, or row views. Reach for
numeric ranges only for counter-driven grammar work, explicit lookahead windows,
manual cursor arithmetic, or places where the source does not yet expose an
iterator.

Prefer grammar composition over helper functions whose only job is list plumbing:

```elisa
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

```elisa
node <- expr(node[span = left.span + right.span] Pascal.Expr.Binary(left: left, op: op, right: right))
```

Use span algebra:

```elisa
span <- expr(start_token.span + end_token.span)
```

Avoid new parser code that writes:

```elisa
span <- expr(combine_span(start_token.span, end_token.span))
node <- expr(node[span = combine_span(left.span, right.span)] Pascal.Expr.Binary(left: left, right: right))
```

Keep low-level constructors available for places where allocation, storage, or field order must be explicit.

## Sequences

Use block `seq:` when a sequence owns multiple meaningful steps:

```elisa
choice:
    seq:
        .IDENT(token)
        expr(make_name_expr(token))
    seq:
        .INTEGER(token)
        expr(make_integer_expr(token))
```

For reusable transformed atoms, prefer a small helper production over nesting compact `seq(...)` inside a large expression:

```elisa
name_atom() -> Pascal.Expr:
    .IDENT(token)
    return make_name_expr(token)

infix table ExprTable(additive):
    atom = choice(prefix(.MINUS) atom() -> make_unary_expr(op, operand), name_atom(), integer_atom(), string_atom())
```

Inline `seq(...)` is still valid for compact nested positions, but it should not become the default style for real productions.

## Optional And Recovery

Use postfix `?` for optional grammar terms:

```elisa
else_stmt = else_clause()?
args = delimited(.LPAREN, expression() |> separated_by(stop: RParenSync), .RPAREN, ExpectedRightParen)?
```

When ordinary Elisa core support code needs to return an optional value only after several optional inputs are present, prefer multi-binding `if let` over a nested ladder:

```elisa
def integer_range_contains(lower: Expr, upper: Expr, value: Expr) -> bool?:
    if let lower_value = integer_literal(lower),
           upper_value = integer_literal(upper),
           actual_value = integer_literal(value):
        return actual_value >= lower_value and actual_value <= upper_value
    return null
```

Use the same multi-binding form when the present branch performs diagnostics, mutation, or multiple statements:

```elisa
if let lower_value = integer_literal(lower),
       upper_value = integer_literal(upper):
    lower_value > upper_value then:
        record_diagnostic()
    return
```

The `then:` block is just a compact statement-form `if` for short guards. Use the ordinary `if condition:` spelling when the condition or branch needs more room.

Use direct recovery constructors for repeated recovery shapes:

```elisa
grammar type recovered_expr(stop: tokenset) -> grammar -> SML.Expr:
    expression() |> recovered(message: expr(SMLParseMessageKey.ExpectedExpression), stop: stop, fallback: expr(invalid_sml_expr_at(state.current_token().span)))

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

```elisa
protocol SpanLike:
    type Range
    def combine(left: Range, right: Range) -> Range
```

The older `static interface` spelling has been removed; use `protocol`.

## When To Drop Down

Drop to ordinary Elisa core code when:

- Allocation strategy matters more than grammar readability.
- A parser helper needs loops, mutation, or several non-grammar checks.
- Error handling needs explicit `try` or custom error mapping.
- A tiny helper production would obscure rather than clarify the grammar.

The pyramid rule is: write the common grammar at the DSL layer, then descend freely when the implementation needs fine-grained control.
