package grammar

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestLowerFileLowersLexerHelperDeclToFunctions(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum DemoTokenKind of i16:
    EOF = 0
    IDENT = 1
    IF = 2
    EQ = 3
    EQEQ = 4

lexer DemoLex:
    token_kind DemoTokenKind
    mode_enum DemoLexMode
    mode NORMAL
    mode STRING
    charclass digit = '0'..'9'
    charclass ident = '_' | digit
    keywords fallback IDENT:
        "if" -> IF
    literals longest fallback EOF:
        "=" -> EQ
        "==" -> EQEQ
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"enum DemoLexMode:",
		"NORMAL",
		"STRING",
		"def demo_lex_is_digit(ch: char) -> bool:",
		"def demo_lex_is_ident(ch: char) -> bool:",
		"demo_lex_is_digit(ch)",
		"def demo_lex_keyword_kind(text: sview) -> DemoTokenKind:",
		"match text:",
		`"if":`,
		"DemoTokenKind.IF",
		"DemoTokenKind.IDENT",
		"def demo_lex_match_literal(source: cstr, offset: usize) -> (kind: DemoTokenKind, len: usize):",
		`source[offset:(offset + 2)] == "=="`,
		"return (DemoTokenKind.EQEQ, 2)",
		"return (DemoTokenKind.EOF, 0)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lowered lexer helper output to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "lexer DemoLex:") {
		t.Fatalf("expected lexer decl to be lowered away, got:\n%s", formatted)
	}
	longIndex := strings.Index(formatted, `source[offset:(offset + 2)] == "=="`)
	shortIndex := strings.Index(formatted, `source[offset:(offset + 1)] == "="`)
	if longIndex < 0 || shortIndex < 0 || longIndex > shortIndex {
		t.Fatalf("expected longest literal match to be checked before shorter prefix, got:\n%s", formatted)
	}
}

func TestLowerFileLowersKeywordMapDeclToFunction(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum LuaTokenKind of i16:
    NAME = 0
    AND = 1
    BREAK = 2

keywordmap lua_keyword: sview -> LuaTokenKind:
    "and" => .AND
    "break" => .BREAK
    _ => .NAME
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def lua_keyword(text: sview) -> LuaTokenKind:",
		"match text:",
		`"and":`,
		"return .AND",
		`"break":`,
		"return .BREAK",
		"_:",
		"return .NAME",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected keywordmap lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "keywordmap lua_keyword") {
		t.Fatalf("expected keywordmap decl to be lowered away, got:\n%s", formatted)
	}
}

func TestLowerFileLexerImportsGrammarTokenAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum DemoTokenKind of i16:
    EOF = 0
    IDENT = 1
    BEGIN = 2
    END = 3
    COLON = 4
    ASSIGN = 5

grammar DemoGrammar:
    token_kind DemoTokenKind
    token:
        IDENT
        BEGIN "begin"
        END "end"
        COLON ":"
        ASSIGN ":="

lexer DemoLex:
    tokens DemoGrammar
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def demo_lex_keyword_kind(text: sview) -> DemoTokenKind:",
		`"begin":`,
		"DemoTokenKind.BEGIN",
		`"end":`,
		"DemoTokenKind.END",
		"DemoTokenKind.IDENT",
		"def demo_lex_assert_keyword_table() -> void can[Abort.Panic]:",
		`assert_eq(demo_lex_keyword_kind("begin"), DemoTokenKind.BEGIN)`,
		`assert_eq(demo_lex_keyword_kind("end"), DemoTokenKind.END)`,
		"def demo_lex_match_literal(source: cstr, offset: usize) -> (kind: DemoTokenKind, len: usize):",
		`source[offset:(offset + 2)] == ":="`,
		"return (DemoTokenKind.ASSIGN, 2)",
		`source[offset:(offset + 1)] == ":"`,
		"return (DemoTokenKind.COLON, 1)",
		"return (DemoTokenKind.EOF, 0)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lexer imported token aliases to contain %q, got:\n%s", want, formatted)
		}
	}
	assignIndex := strings.Index(formatted, `source[offset:(offset + 2)] == ":="`)
	colonIndex := strings.Index(formatted, `source[offset:(offset + 1)] == ":"`)
	if assignIndex < 0 || colonIndex < 0 || assignIndex > colonIndex {
		t.Fatalf("expected imported literal aliases to use longest matching, got:\n%s", formatted)
	}
}
func TestLowerFileLexerKeywordCompareUsesConfiguredFunc(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum DemoTokenKind of i16:
    EOF = 0
    IDENT = 1
    BEGIN = 2

lexer DemoLex:
    token_kind DemoTokenKind
    keyword_compare demo_text_eq
    keywords fallback IDENT:
        "begin" -> BEGIN
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def demo_lex_keyword_kind(text: sview) -> DemoTokenKind:",
		`if demo_text_eq(text, "begin"):`,
		"return DemoTokenKind.BEGIN",
		"return DemoTokenKind.IDENT",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lexer keyword compare lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "match text:") {
		t.Fatalf("expected configured keyword compare lowering to avoid match expression, got:\n%s", formatted)
	}
}
func TestLowerFileGeneratesTokenEnumFromGrammarAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar DemoGrammar over Token using ParserState:
    cursor state
    token_enum DemoTokenKind of u16
    token_lookup demo_token_kind_for_text
    token:
        IDENT
        BEGIN "begin"
        END "end"
    program() -> Token:
        "begin"
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"const enum DemoTokenKind of u16:",
		"EOF = 0",
		"IDENT = 1",
		"BEGIN = 2",
		"END = 3",
		"def demo_token_kind_for_text(text: cstr) -> DemoTokenKind:",
		"return DemoTokenKind.BEGIN",
		"return DemoTokenKind.EOF",
		"state.expect_kind(DemoTokenKind.BEGIN)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected generated token enum lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileLexerImportsGeneratedGrammarTokenEnum(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar DemoGrammar:
    token_enum DemoTokenKind
    token:
        IDENT
        BEGIN "begin"

lexer DemoLex:
    tokens DemoGrammar
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"const enum DemoTokenKind of i16:",
		"def demo_lex_keyword_kind(text: sview) -> DemoTokenKind:",
		"DemoTokenKind.BEGIN",
		"DemoTokenKind.IDENT",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lexer to use generated grammar token enum %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileLexerTokenAliasImportRespectsExplicitEntries(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum DemoTokenKind of i16:
    EOF = 0
    IDENT = 1
    BEGIN = 2
    CUSTOM_BEGIN = 3
    COLON = 4
    CUSTOM_COLON = 5

grammar DemoGrammar:
    token_kind DemoTokenKind
    token:
        BEGIN "begin"
        COLON ":"

lexer DemoLex:
    tokens DemoGrammar
    keywords fallback IDENT:
        "begin" -> CUSTOM_BEGIN
    literals fallback EOF:
        ":" -> CUSTOM_COLON
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"DemoTokenKind.CUSTOM_BEGIN",
		"DemoTokenKind.CUSTOM_COLON",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected explicit lexer entries to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "DemoTokenKind.BEGIN") || strings.Contains(formatted, "DemoTokenKind.COLON") {
		t.Fatalf("expected explicit lexer entries to override imported aliases, got:\n%s", formatted)
	}
}
func TestLowerFileLexerTokenAliasImportIncludesUsedGrammars(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum DemoTokenKind of i16:
    EOF = 0
    IDENT = 1
    IF = 2
    BEGIN = 3

grammar SharedTokens:
    token_kind DemoTokenKind
    token:
        IF "if"

grammar DemoGrammar uses SharedTokens:
    token_kind DemoTokenKind
    token:
        BEGIN "begin"

lexer DemoLex:
    tokens DemoGrammar
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		`"if":`,
		"DemoTokenKind.IF",
		`"begin":`,
		"DemoTokenKind.BEGIN",
		"DemoTokenKind.IDENT",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lexer token import to include used grammar alias %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileLexerImportsQualifiedGrammarTokenAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `const enum DemoTokenKind of i16:
    EOF = 0
    IDENT = 1
    BEGIN = 2

module Pascal:
    grammar Frontend:
        token_kind DemoTokenKind
        token:
            IDENT
            BEGIN "begin"

lexer DemoLex:
    tokens Pascal.Frontend
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		`"begin":`,
		"DemoTokenKind.BEGIN",
		"DemoTokenKind.IDENT",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected lexer to import qualified grammar aliases %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulAddsStablePublicTryWrapper(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        token(TokenKind.IDENT)
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def expression(state: mutable ParserState&) -> Token:",
		"def grammar_try_PascalFrontend_expression(state: mutable ParserState&) -> (matched: bool, value: Token):",
		"__grammar_committed_expression_PascalFrontend_committed_",
		"def __grammar_try__PascalFrontend__expression(state: mutable ParserState&) -> (matched: bool, committed: bool, value: Token):",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected stable public try wrapper output to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulUsesGrantOnlyCanBlocksForGeneratedPermissions(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend:
    expression(state: mutable ParserState&) -> Token:
        token(TokenKind.IDENT)
`)
	lowered := LowerFile(file)
	fn, ok := lowered.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected first lowered decl after grammar to be func, got %T", lowered.Decls[1])
	}
	if len(fn.Body) != 1 {
		t.Fatalf("expected generated public production to be wrapped in one can stmt, got %d statements", len(fn.Body))
	}
	canStmt, ok := fn.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected generated production body to start with can stmt, got %T", fn.Body[0])
	}
	if !canStmt.SuppressPermissionInference {
		t.Fatal("expected generated grammar can block to suppress outward permission inference")
	}
}
func TestLowerFileStatefulUsesGrammarHeaderReceiverForParamlessProductions(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    ident_expr() -> Token:
        .IDENT(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def ident_expr(parser: mutable ParserState&) -> Token:",
		"def grammar_try_PascalFrontend_ident_expr(parser: mutable ParserState&) -> (matched: bool, value: Token):",
		"def __grammar_try__PascalFrontend__ident_expr(parser: mutable ParserState&) -> (matched: bool, committed: bool, value: Token):",
		"parser.cursor",
		"parser.expect_kind(TokenKind.IDENT)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected header-driven stateful lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulInjectsHeaderReceiverIntoBareProductionCalls(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    atom() -> Token:
        .IDENT(tok)
        return tok
    expr() -> Token:
        value = atom()
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "__grammar_try__PascalFrontend__atom(parser)") {
		t.Fatalf("expected bare production call to thread implicit header receiver, got:\n%s", formatted)
	}
}
func TestLowerFileMergesExtendGrammarProductionsAndHeader(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor parser
    atom() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalFrontend:
    expr() -> Token:
        value = atom()
        return value
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def atom(parser: mutable ParserState&) -> Token:",
		"def expr(parser: mutable ParserState&) -> Token:",
		"__grammar_try__PascalFrontend__atom(parser)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected merged extend-grammar lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Count(formatted, "def atom(parser: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected merged grammar lowering to emit atom exactly once, got:\n%s", formatted)
	}
	if strings.Count(formatted, "def expr(parser: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected merged grammar lowering to emit expr exactly once, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulProductionAugmentationBuildsSingleMergedProduction(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    cursor state
    statement() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalStmtGrammar:
    statement +=
        .INTEGER(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if strings.Count(formatted, "def statement(state: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected exactly one merged statement production, got:\n%s", formatted)
	}
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
		"__grammar_augment_statement_base_0",
		"__grammar_augment_statement_append_1",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected augmented lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulGroupedProductionAugmentationBuildsMergedProduction(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalStmtGrammar over Token using ParserState:
    cursor state
    statement() -> Token:
        .IDENT(tok)
        return tok

extend grammar PascalStmtGrammar:
    statement +=:
        .INTEGER(tok)
		pass
		|
        .STRING(tok)
        return tok
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if strings.Count(formatted, "def statement(state: mutable ParserState&) -> Token:") != 1 {
		t.Fatalf("expected exactly one merged statement production, got:\n%s", formatted)
	}
	for _, want := range []string{
		"state.expect_kind(TokenKind.IDENT)",
		"state.expect_kind(TokenKind.INTEGER)",
		"state.expect_kind(TokenKind.STRING)",
		"__grammar_augment_statement_base_0",
		"__grammar_augment_statement_append_1",
		"__grammar_augment_statement_append_2",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected grouped augmented lowering to contain %q, got:\n%s", want, formatted)
		}
	}
}
func TestLowerFileStatefulTokenAliasesRewriteLiteralTokensToKinds(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor state
    token .PROGRAM "program"
    program() -> Token:
        "program"
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, "state.expect_kind(TokenKind.PROGRAM)") {
		t.Fatalf("expected token alias lowering to prefer expect_kind for aliased literal, got:\n%s", formatted)
	}
	if strings.Contains(formatted, `state.expect("program")`) {
		t.Fatalf("expected token alias lowering to avoid raw string match for aliased literal, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulTokenLookupUsesTokenAliases(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor state
    token_lookup token_kind_for_text
    token:
        IDENT
        PROGRAM "program"
        PLUS "+"
    program() -> Token:
        "program"
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def token_kind_for_text(text: cstr) -> TokenKind:",
		`if (text == "program"):`,
		"return TokenKind.PROGRAM",
		`if (text == "+"):`,
		"return TokenKind.PLUS",
		"return TokenKind.EOF",
		"def token_kind_for_text_assert_table() -> void can[Abort.Panic]:",
		`assert_eq(token_kind_for_text("program"), TokenKind.PROGRAM)`,
		`assert_eq(token_kind_for_text("+"), TokenKind.PLUS)`,
		"state.expect_kind(TokenKind.PROGRAM)",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected token lookup lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "TokenKind.IDENT") && strings.Contains(formatted, `if (text == "IDENT")`) {
		t.Fatalf("expected bare token aliases not to become text lookup arms, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulTokenLookupUsesConfiguredCompareFunc(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar PascalFrontend over Token using ParserState:
    cursor state
    token_lookup token_kind_for_text
    token_lookup_compare pascal_text_eq_keyword
    token:
        PROGRAM "program"
        PLUS "+"
    program() -> Token:
        "program"
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	for _, want := range []string{
		"def token_kind_for_text(text: cstr) -> TokenKind:",
		`if pascal_text_eq_keyword(text, "program"):`,
		"return TokenKind.PROGRAM",
		`if pascal_text_eq_keyword(text, "+"):`,
		"return TokenKind.PLUS",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected token lookup compare lowering to contain %q, got:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, `if (text == "program"):`) {
		t.Fatalf("expected configured token lookup compare to avoid raw equality, got:\n%s", formatted)
	}
}
func TestLowerFileStatefulRawTokenLiteralUsesConfiguredTokenLookup(t *testing.T) {
	file := parseGrammarTestFile(t, `grammar DemoGrammar over Token using ParserState:
    cursor state
    token_lookup demo_token_kind_for_text
    program() -> Token:
        "custom"
`)
	lowered := LowerFile(file)
	formatted := unparse.FormatFile(lowered)
	if !strings.Contains(formatted, `demo_token_kind_for_text("custom")`) {
		t.Fatalf("expected raw token literal matching to use configured token lookup, got:\n%s", formatted)
	}
	if strings.Contains(formatted, `== token_kind_for_text("custom")`) {
		t.Fatalf("expected raw token literal matching not to use default token lookup, got:\n%s", formatted)
	}
}
