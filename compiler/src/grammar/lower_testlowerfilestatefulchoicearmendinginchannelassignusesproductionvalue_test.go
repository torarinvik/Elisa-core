package grammar

import (
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestLowerFileStatefulChoiceArmEndingInChannelAssignUsesProductionValue(t *testing.T) {
	file := parseGrammarTestFile(t, `struct Tail:
	name_token: Token
	close_token: Token

grammar PerlFrontend over Token using ParserState:
	cursor parser
	member_tail() -> Tail:
		channel name_token: Token
		channel close_token: Token
		choice:
			seq:
				name_token <- token(TokenKind.IDENT)
				close_token <- expr(name_token)
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_seq_value_",
		"zeroed as Tail",
		"Tail(name_token:, close_token:)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected channel-ending seq lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "<- name_token") && strings.Contains(formatted, "mutable Tail") {
		t.Fatalf("expected channel-ending seq to avoid assigning token channel as Tail value, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulSynthesizesNamedTupleReturnFromChannels(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel item: i64 = 7
	channel count: usize = 1
	summary() -> (item: i64, count: usize):
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "def summary(parser: mutable ParserState&) -> (item: i64, count: usize):") {
		t.Fatalf("expected public production to preserve named tuple return type, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "(item, count)") {
		t.Fatalf("expected synthesized named tuple return to assemble channel fields, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulInfersUntypedChannelsFromNamedTupleReturnFields(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel item
	channel count
	summary() -> (item: i64, count: usize):
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"item: mutable i64 = zeroed as i64",
		"count: mutable usize = zeroed as usize",
		"return (true, __grammar_committed_",
		"(item, count)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected named tuple channel inference lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "item: mutable (item: i64, count: usize)") || strings.Contains(formatted, "count: mutable (item: i64, count: usize)") {
		t.Fatalf("expected tuple-field inference to avoid seeding channels with the whole tuple type, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulTypedExprTermInSeparatedListInfersElementType(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel param_name_ids
	param_name_ids_core() -> darray[NameId]:
		param_name_ids <- separated required(seq(.IDENT(param_token), expr[NameId](param_token.lexeme_key)), ParseMessageKey.ExpectedProgramParamName) by .COMMA until(.RPAREN, token(TokenKind.EOF))
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"param_name_ids: mutable darray[NameId] = zeroed as darray[NameId]",
		"__grammar_list_value_",
		"mutable NameId = zeroed as NameId",
		".push(__grammar_seq_value_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected typed expr list lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected typed expr term to prevent void list inference, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulTypedExprTermCarriesDeclaredTypeForEmptyList(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	empty_decls() -> darray[Pascal.Decl]:
		decls <- expr[darray[Pascal.Decl]]([])
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_value_empty_decls_",
		": darray[Pascal.Decl] = []",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected typed expr lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulListComprehensionPreservesComprehensionExpr(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	variable_decl_group(names: darray[Token], type_token: Token) -> darray[Pascal.Decl]:
		decls <- [build_var_decl(name_token, type_token) for name_token in names if name_token.kind == TokenKind.IDENT]
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"[build_var_decl(name_token, type_token) for name_token in names if (name_token.kind == TokenKind.IDENT)]",
		"decls <- [build_var_decl(name_token, type_token) for name_token in names if (name_token.kind == TokenKind.IDENT)]",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected list comprehension lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "__grammar_maplist_") {
		t.Fatalf("expected lowering to avoid legacy maplist scaffolding, got:\n%s", formatted)
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected list comprehension lowering to preserve element typing, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulInterleavedSupportDeclsResolve(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	advance advance_token
	expect expect
	expect_kind expect_kind
	current current_token
	record_error record_parse_error
	first_arg() -> Token:
		token = .IDENT
		return token
	token:
		IDENT
		COMMA ","
		RPAREN ")"
	tokenset RParenSync:
		RPAREN
		token(TokenKind.EOF)
	grammar alias arg_items = separated required(.IDENT, ParseMessageKey.ExpectedDeclName) by .COMMA until(RParenSync)
	recovery ArgRecovery:
		message ParseMessageKey.ExpectedDeclName
		until RParenSync
		fallback []
	args() -> darray[Token]:
		values = arg_items recover ArgRecovery
		return values
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def first_arg(parser: mutable ParserState&) -> Token:",
		"parser.expect_kind(TokenKind.IDENT)",
		"parser.current_token().kind == TokenKind.RPAREN",
		"parser.record_parse_error(ParseMessageKey.ExpectedDeclName)",
		"def args(parser: mutable ParserState&) -> darray[Token]:",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered interleaved grammar support declarations to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulSingletonBuildsSingleItemList(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	alloc arena
	channel decls
	const_decl_group(decl: Pascal.Decl) -> darray[Pascal.Decl]:
		decls <- singleton[Pascal.Decl](decl)
		return decls
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Pascal.Decl] = zeroed as darray[Pascal.Decl]",
		"__grammar_singleton_value_",
		"in arena:",
		".push(decl)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected singleton lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "single_decl_list") || strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected singleton lowering to avoid helper calls and infer element type, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulEmptyBuildsTypedEmptyList(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	empty_decls() -> darray[Pascal.Decl]:
		decls <- empty[Pascal.Decl]
		return decls
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Pascal.Decl] = zeroed as darray[Pascal.Decl]",
		"__grammar_empty_value_",
		": darray[Pascal.Decl] = []",
		"decls <- __grammar_empty_value_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected empty lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected empty lowering to infer element type, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulConcatTermFlattensListOperands(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	declarations() -> darray[Token]:
		decls <- expr[darray[Token]]([]) + list(token(TokenKind.IDENT)) + expr[darray[Token]]([])
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Token] = zeroed as darray[Token]",
		"__grammar_concat_value_",
		"__grammar_concat_group_",
		"parser.expect_kind(TokenKind.IDENT)",
		".push(__grammar_concat_group_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected concat lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected concat lowering to preserve list element type, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulConcatMergesListElementTypePastUntypedEmpty(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
	cursor parser
	channel decls
	declarations(decl: Pascal.Decl) -> darray[Pascal.Decl]:
		decls <- empty + singleton[Pascal.Decl](decl)
		pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"decls: mutable darray[Pascal.Decl] = zeroed as darray[Pascal.Decl]",
		"__grammar_concat_value_",
		"darray[Pascal.Decl] = []",
		".push(decl)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected concat lowering to merge element type past untyped empty and contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "darray[void]") {
		t.Fatalf("expected concat lowering to avoid darray[void] for untyped empty plus typed singleton, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulSynthesizesStructReturnFromNamespacedChannels(t *testing.T) {
	file := parseGrammarTestFile(t, `namespace PascalFrontend:
	struct BuiltSummary:
		items: darray[i64]
		checksum_total: i64
		arg_count: usize
		close_span: Span

	grammar Parser over Token using ParserState:
		cursor parser
		channel items: darray[i64] = []
		channel checksum_total: i64 = 0
		channel arg_count: usize = 0
		channel close_span: Span = $end.span
		summary() -> BuiltSummary:
			pass
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "BuiltSummary(items:, checksum_total:, arg_count:, close_span:)") {
		t.Fatalf("expected namespaced struct return synthesis to use the local struct scope, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulTryWrapperCarriesProductionErrorUnion(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar ATPLFrontend:
    postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:
        base = expr(try self.parse_primary(owner))
        return base
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:",
		"def grammar_try_ATPLFrontend_postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, value: ATPLExpr.Expr) error[ATPLFrontendError]:",
		"def __grammar_try__ATPLFrontend__postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, committed: bool, value: ATPLExpr.Expr) error[ATPLFrontendError]:",
		"try __grammar_try__ATPLFrontend__postfix_expr(self, owner)",
		"try self.parse_primary(owner)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected error-union stateful grammar lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulAutoTriesNestedErrorUnionProductionCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar ATPLFrontend:
    atom(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:
        base = expr(try self.parse_primary(owner))
        return base
    postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> ATPLExpr.Expr error[ATPLFrontendError]:
        value = atom(self, owner)
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "try __grammar_try__ATPLFrontend__atom(self, owner)") {
		t.Fatalf("expected nested error-union production calls to lower through try-wrapped internal helpers, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulSupportsPosBasedReceiverForAttemptDrivenPostfix(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar ATPLFrontend:
    postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> Token:
        result = postfix(left = attempt(self.try_parse_primary(owner))):
			step = attempt(self.try_parse_suffix(owner, left)) -> step
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> Token:",
		"def grammar_try_ATPLFrontend_postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, value: Token):",
		"def __grammar_try__ATPLFrontend__postfix_expr(self: mutable ATPLParser&, owner: mutable Arena&) -> (matched: bool, committed: bool, value: Token):",
		"self.pos",
		"= self.try_parse_primary(owner)",
		"= self.try_parse_suffix(owner, left)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected pos-based stateful postfix lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	for _, unwanted := range []string{
		"self.current_token()",
		"self.advance_token()",
		"self.record_parse_error(",
	} {
		if strings.Contains(formatted, unwanted) {
			t.Fatalf("expected pos-based receiver lowering to avoid %q, got:\n%s", unwanted, formatted)
		}
	}
}
func TestLowerFileStatefulCutPropagatesCommittedFailureThroughChoice(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Token:
        value = choice(if_statement(), ident_statement())
        return value
    if_statement(state: mutable ParserState&) -> Token:
        "if"
        cut
        "then"
        return zeroed as Token
    ident_statement(state: mutable ParserState&) -> Token:
        .IDENT(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def __grammar_try__PascalFrontend__if_statement(state: mutable ParserState&) -> (matched: bool, committed: bool, value: Token):",
		"choice_committed_statement_PascalFrontend",
		"return (false, __grammar_committed_if_statement_PascalFrontend",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected cut-aware lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulPrecedenceTermBindsOperatorValue(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        precedence(left = token(TokenKind.IDENT)):
			op = "+" right = token(TokenKind.IDENT) -> right
        return left
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"state.expect(\"+\")",
		"op = __grammar_token_expression_PascalFrontend_token_",
		"right = __grammar_token_expression_PascalFrontend_token_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered precedence production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulPostfixTermBuildsLoopAndBindings(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar LuaFrontend:
    suffix_expr(state: mutable ParserState&) -> Token:
        result = postfix(left = token(TokenKind.IDENT)):
			"." field = token(TokenKind.IDENT) -> field
			"(" arg = token(TokenKind.IDENT) close = ")" -> arg
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"left: mutable Token = __grammar_token_suffix_expr_LuaFrontend_token_",
		"while (not __grammar_postfix_stop_suffix_expr_LuaFrontend_postfix_stop_",
		"state.expect(\".\")",
		"field = __grammar_token_suffix_expr_LuaFrontend_token_",
		"state.expect(\"(\")",
		"arg = __grammar_token_suffix_expr_LuaFrontend_token_",
		"close = __grammar_token_suffix_expr_LuaFrontend_token_",
		"state.expect(\")\")",
		"result = left",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered postfix production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulSuffixTermRunsArmsOnce(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    condition(state: mutable ParserState&) -> Token:
        result = suffix(left = token(TokenKind.IDENT)):
			op = choice("=", ">") right = token(TokenKind.IDENT) -> right
        return result
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"left: mutable Token = __grammar_token_condition_PascalFrontend_token_",
		"__grammar_suffix_cursor_condition_PascalFrontend_suffix_cursor_",
		"state.expect(\"=\")",
		"state.expect(\">\")",
		"op = __grammar_choice_value_condition_PascalFrontend_choice_value_",
		"right = __grammar_token_condition_PascalFrontend_token_",
		"result = left",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered suffix production to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "while (not __grammar_suffix_stop_condition_PascalFrontend_suffix_stop_") {
		t.Fatalf("expected suffix term to avoid postfix loop, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulExprTermEvaluatesHelperExpression(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        current = expr(state.current_token())
        parsed = expr(try state.expect_name_token(ParseMessageKey.ExpectedProgramName))
        return current
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_value_expression_PascalFrontend_value_",
		"current = __grammar_value_expression_PascalFrontend_value_",
		"parsed = __grammar_value_expression_PascalFrontend_value_",
		"try state.expect_name_token(ParseMessageKey.ExpectedProgramName)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered expr-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulTokenKindShorthandUsesExpectKind(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    ident_expr(state: mutable ParserState&) -> Token:
        .IDENT(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def ident_expr(state: mutable ParserState&) -> Token:",
		"state.expect_kind(TokenKind.IDENT)",
		"tok = __grammar_token_ident_expr_PascalFrontend_token_",
		"return tok",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered token-kind shorthand production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulAttemptTermBuildsTupleBind(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        suffix = attempt(state.try_parse_suffix())
        return suffix
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_matched_expression_PascalFrontend_matched_",
		"__grammar_value_expression_PascalFrontend_value_",
		"= state.try_parse_suffix()",
		"if (not __grammar_matched_expression_PascalFrontend_matched_",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered attempt-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulGuardTermBuildsFailurePredicate(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    statement(state: mutable ParserState&) -> Token:
        .IDENT(name_token)
        guard(state.lookahead_token(1).kind == TokenKind.ASSIGN)
        return name_token
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"__grammar_guard_statement_PascalFrontend_guard_",
		"state.lookahead_token(1).kind == TokenKind.ASSIGN",
		"if (not __grammar_guard_statement_PascalFrontend_guard_",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered guard-term production to contain %q, got:\n%s", want, formatted)
		}
	}
}
