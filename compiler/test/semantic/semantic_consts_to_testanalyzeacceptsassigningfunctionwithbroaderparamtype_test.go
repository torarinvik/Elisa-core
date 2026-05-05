package semantic_test

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const parallelForConcurrencyPrelude = `extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]
extern pool_shutdown(pool: ThreadPool&) -> void can[Pool.Shutdown]

def pool_submit1[A, R](pool: ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending] can[Pool.Submit, Memory.Allocate, Abort.Panic]:
	task: Task[R, Pending] = zeroed
	return move task

def task_group_new() -> TaskGroup:
	group: TaskGroup = zeroed
	return move group

def task_group_add[R](group: TaskGroup&, task: Task[R, Pending]) -> void can[Memory.Allocate, Abort.Panic]:
	_ = move task

def task_group_wait_all(group: TaskGroup&) -> void can[Pool.WaitAll]:
	pass
`

func parseAndAnalyze(t *testing.T, filename string, src string) (*semantic.Result, []string) {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		return nil, errs
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		return nil, errs
	}
	result := semantic.Analyze(file)
	return result, result.Errors()
}
func requireNoErrors(t *testing.T, errs []string) {
	t.Helper()
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got:\n%s", strings.Join(errs, "\n"))
	}
}
func requireNoWarnings(t *testing.T, result *semantic.Result) {
	t.Helper()
	if warns := result.Warnings(); len(warns) != 0 {
		t.Fatalf("expected no warnings, got:\n%s", strings.Join(warns, "\n"))
	}
}
func TestAnalyzeContextuallyCoercesStringLiteralsToCStrAndSView(t *testing.T) {
	src := `extern take_cstr(text: cstr) -> void
extern take_sview(text: sview) -> void

def use() -> void:
    text: cstr = "hello"
    view: sview = "world"
    take_cstr("hello")
    take_sview("world")
    _ = text
    _ = view
`
	_, errs := parseAndAnalyze(t, "string_literal_contextual_cstr_sview.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeReportsStatefulGrammarWhenTermMismatchedBranchTypes(t *testing.T) {
	src := `struct Token:
    kind: TokenKind

struct ParserState:
    cursor: mutable usize

const enum TokenKind of i16:
    EOF = 0
    IDENT = 1

def current_token(state: ParserState&) -> Token:
    return zeroed as Token

def expect_kind(state: mutable ParserState&, kind: TokenKind) -> Token:
    return zeroed as Token

grammar DemoGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    eof TokenKind.EOF
    token_field kind
    current current_token
    expect_kind expect_kind
    token:
        IDENT
    atom() -> Token:
        value = when(state.current_token().kind == TokenKind.IDENT, .IDENT, expr(1))
        return value
`
	_, errs := parseAndAnalyze(t, "grammar_when_mismatched_branch_types.elisa", src)
	if len(errs) == 0 {
		t.Fatalf("expected mismatched when-term branch values to fail semantic analysis")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot assign") || !strings.Contains(all, "Token") {
		t.Fatalf("expected lowered when-term type mismatch diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeReportsStatefulGrammarChoiceMismatchedBranchTypes(t *testing.T) {
	src := `struct Token:
    kind: TokenKind

struct ParserState:
    cursor: mutable usize

const enum TokenKind of i16:
    EOF = 0
    IDENT = 1

def current_token(state: ParserState&) -> Token:
    return zeroed as Token

def expect_kind(state: mutable ParserState&, kind: TokenKind) -> Token:
    return zeroed as Token

grammar DemoGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    eof TokenKind.EOF
    token_field kind
    current current_token
    expect_kind expect_kind
    token:
        IDENT
    atom() -> Token:
        value = choice(.IDENT, expr(1))
        return value
`
	_, errs := parseAndAnalyze(t, "grammar_choice_mismatched_branch_types.elisa", src)
	if len(errs) == 0 {
		t.Fatalf("expected mismatched choice branch values to fail semantic analysis")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot assign") || !strings.Contains(all, "Token") {
		t.Fatalf("expected lowered choice type mismatch diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsStatefulGrammarChoicePromotingValueBranchToOptional(t *testing.T) {
	src := `struct Token:
    kind: TokenKind

struct ParserState:
    cursor: mutable usize

const enum TokenKind of i16:
    EOF = 0
    IDENT = 1

def current_token(state: ParserState&) -> Token:
    return zeroed as Token

def expect_kind(state: mutable ParserState&, kind: TokenKind) -> Token:
    return zeroed as Token

grammar DemoGrammar over Token using ParserState:
    cursor state
    token_kind TokenKind
    eof TokenKind.EOF
    token_field kind
    current current_token
    expect_kind expect_kind
    token:
        IDENT
    atom() -> Token?:
        value = choice(.IDENT, expr[Token?](null))
        return value
`
	_, errs := parseAndAnalyze(t, "grammar_choice_promotes_value_to_optional.elisa", src)
	requireNoErrors(t, errs)
}
func requireDeclaredFunctionPermissionRefs(t *testing.T, result *semantic.Result, name string, expected ...string) {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected %s to be a function, got %T", name, sym.Type)
	}
	got := make([]string, 0, len(fn.DeclaredPermissionRefs))
	for _, ref := range fn.DeclaredPermissionRefs {
		got = append(got, semantic.PermissionRefString(ref))
	}
	want := append([]string(nil), expected...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %s declared permissions %v, got %v", name, expected, got)
	}
}
func requireFunctionPermissionRefs(t *testing.T, result *semantic.Result, name string, expected ...string) {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected %s to be a function, got %T", name, sym.Type)
	}
	got := make([]string, 0, len(fn.PermissionRefs))
	for _, ref := range fn.PermissionRefs {
		got = append(got, semantic.PermissionRefString(ref))
	}
	want := append([]string(nil), expected...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("expected %s permissions %v, got %v", name, expected, got)
	}
}
func requireFunctionReturnTypeString(t *testing.T, result *semantic.Result, name string, expected string) {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	fn, ok := sym.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected %s to be a function, got %T", name, sym.Type)
	}
	if got := fn.Return.String(); got != expected {
		t.Fatalf("expected %s return type %q, got %q", name, expected, got)
	}
}
func requireFuncDecl(t *testing.T, result *semantic.Result, name string) *ast.FuncDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected %s to resolve to a function declaration, got %T", name, sym.Node)
	}
	return decl
}
func requireConstDecl(t *testing.T, result *semantic.Result, name string) *ast.ConstDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	decl, ok := sym.Node.(*ast.ConstDecl)
	if !ok {
		t.Fatalf("expected %s to resolve to a const declaration, got %T", name, sym.Node)
	}
	return decl
}
func requireGlobalDecl(t *testing.T, result *semantic.Result, name string) *ast.GlobalDecl {
	t.Helper()
	sym, ok := result.GlobalScope.Lookup(name)
	if !ok {
		t.Fatalf("expected %s symbol", name)
	}
	decl, ok := sym.Node.(*ast.GlobalDecl)
	if !ok {
		t.Fatalf("expected %s to resolve to a global declaration, got %T", name, sym.Node)
	}
	return decl
}
func requireExprTypeString(t *testing.T, result *semantic.Result, expr ast.Expr, expected string) {
	t.Helper()
	typ, ok := result.ExprTypes[expr]
	if !ok || typ == nil {
		t.Fatalf("expected expression %T to have resolved type %q", expr, expected)
	}
	if got := typ.String(); got != expected {
		t.Fatalf("expected expression %T type %q, got %q", expr, expected, got)
	}
}
func TestAnalyzeAsCastUsesExistingCastSemantics(t *testing.T) {
	src := `struct Arena:
    value: i64

def keep(owner: Arena) -> mutable Arena&:
    return &owner as mutable Arena&
`

	result, errs := parseAndAnalyze(t, "as_cast.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	decl := requireFuncDecl(t, result, "keep")
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cast, ok := ret.Value.(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected cast expr, got %T", ret.Value)
	}
	if cast.Origin != ast.CastExprOriginAsSyntax {
		t.Fatalf("expected as-syntax origin, got %v", cast.Origin)
	}
	requireExprTypeString(t, result, cast, "mutable Arena&")
}

func TestAnalyzeAcceptsConstEnumCastToOptionalSameEnum(t *testing.T) {
	src := `const enum Mode of i8:
    READ = 0
    WRITE = 1

def make_mode() -> Mode?:
    return Mode.READ as Mode?
`

	result, errs := parseAndAnalyze(t, "const_enum_optional_cast.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	decl := requireFuncDecl(t, result, "make_mode")
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	requireExprTypeString(t, result, ret.Value, "Mode?")
}

func TestAnalyzeTernaryPropagatesExpectedDarrayTypeToEmptyListBranch(t *testing.T) {
	src := `def choose(flag: bool) -> darray[i64]:
    values: darray[i64] = [1] if flag else []
    return values
`

	result, errs := parseAndAnalyze(t, "ternary_darray_inference.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	decl := requireFuncDecl(t, result, "choose")
	varDecl, ok := decl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl, got %T", decl.Body[0])
	}
	ter, ok := varDecl.Value.(*ast.TernaryExpr)
	if !ok {
		t.Fatalf("expected ternary initializer, got %T", varDecl.Value)
	}
	requireExprTypeString(t, result, ter, "darray[i64]")
	if _, ok := ter.Alt.(*ast.ListLitExpr); !ok {
		t.Fatalf("expected empty list literal branch, got %T", ter.Alt)
	}
	requireExprTypeString(t, result, ter.Alt, "darray[i64]")
}
func TestAnalyzePatternTernaryBindsTrueBranchPayload(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def unwrap(node: Expr) -> i64:
    return value if node is Expr.Int(value) else 0
`

	result, errs := parseAndAnalyze(t, "pattern_ternary_bindings.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeReturnQuestionPatternGuard(t *testing.T) {
	src := `enum Expr:
    Int(value: i64)
    Missing

def unwrap(node: Expr) -> i64:
    return? value if node is Expr.Int(value)
    return 0
`

	result, errs := parseAndAnalyze(t, "return_question_pattern_guard.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeCatchExprOverErrorUnion(t *testing.T) {
	src := `error FileError:
	NotFound
	Busy

extern read_value(flag: bool) -> i64 error[FileError]

def load(flag: bool) -> i64:
	return catch read_value(flag):
		value:
			value
		NotFound:
			1
		Busy:
			2
`

	result, errs := parseAndAnalyze(t, "catch_expr.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)

	decl := requireFuncDecl(t, result, "load")
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	catchExpr, ok := ret.Value.(*ast.CatchExpr)
	if !ok {
		t.Fatalf("expected catch expr, got %T", ret.Value)
	}
	requireExprTypeString(t, result, catchExpr, "i64")
}
func TestAnalyzeCatchExprRequiresExhaustiveErrorArms(t *testing.T) {
	src := `error FileError:
	NotFound
	Busy

extern read_value(flag: bool) -> i64 error[FileError]

def load(flag: bool) -> i64:
	return catch read_value(flag):
		value:
			value
		NotFound:
			1
`

	_, errs := parseAndAnalyze(t, "catch_non_exhaustive.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error for non-exhaustive catch")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "non-exhaustive catch") || !strings.Contains(joined, "Busy") {
		t.Fatalf("expected non-exhaustive catch error mentioning Busy, got:\n%s", joined)
	}
}
func repoRootFromTestFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to determine test file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
}
func loadSourceWithIncludes(t *testing.T, filename string, seen map[string]bool) string {
	t.Helper()
	abs, err := filepath.Abs(filename)
	if err != nil {
		t.Fatalf("failed to resolve %s: %v", filename, err)
	}
	if seen[abs] {
		return ""
	}
	seen[abs] = true

	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("failed to read %s: %v", abs, err)
	}

	lines := strings.Split(string(raw), "\n")
	var out strings.Builder
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if includePath, ok := parseIncludeDirective(trimmed); ok {
			out.WriteString(loadSourceWithIncludes(t, filepath.Join(filepath.Dir(abs), includePath), seen))
			if out.Len() == 0 || !strings.HasSuffix(out.String(), "\n") {
				out.WriteByte('\n')
			}
			continue
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
func parseIncludeDirective(line string) (string, bool) {
	for _, prefix := range []string{"# include ", "include "} {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
			return "", false
		}
		return rest[1 : len(rest)-1], true
	}
	return "", false
}
func TestAnalyzeValidInlineProgram(t *testing.T) {
	src := `struct Box:
    value: mutable int

extern make_box() -> Box&?

def read_box() -> int:
	box: mutable Box&? = make_box()
    if box == null:
        return 0
    box.value <- 7
    return box.value
`
	_, errs := parseAndAnalyze(t, "inline_valid.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeUndefinedIdentifier(t *testing.T) {
	src := `def bad() -> int:
    return missing
`
	_, errs := parseAndAnalyze(t, "undefined_ident.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(errs[0], "undefined identifier") {
		t.Fatalf("expected undefined identifier error, got %q", errs[0])
	}
}
func TestAnalyzeWrongCallArity(t *testing.T) {
	src := `extern alloc(size: usize) -> int

def use_alloc() -> int:
    return alloc()
`
	_, errs := parseAndAnalyze(t, "wrong_arity.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "expects 1 arguments, got 0") {
		t.Fatalf("expected arity diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsFunctionTypeParametersAndHigherOrderCalls(t *testing.T) {
	src := `def apply_identity[T](fn: func(T) -> T, value: T) -> T:
    return fn(value)

def bump(value: int) -> int:
    return value + 1

def run() -> int:
	return apply_identity(bump, 41)
`
	result, errs := parseAndAnalyze(t, "higher_order_function_types.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "apply_identity", "T")
	requireFunctionReturnTypeString(t, result, "run", "int")
}
func TestAnalyzeRejectsAssigningFunctionWithNarrowerParamType(t *testing.T) {
	src := `struct Box:
	value: int

def only_nonnull(box: Box&) -> int:
	return box.value

def bad() -> int:
	wider: func(Box&?) -> int = only_nonnull
	return wider(null)
`
	_, errs := parseAndAnalyze(t, "function_param_contravariance_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, `variable "wider" expects`) || !strings.Contains(all, `func(Box&?) -> int`) || !strings.Contains(all, `func(Box&) -> int`) {
		t.Fatalf("expected contravariant function assignment diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsAssigningFunctionWithBroaderParamType(t *testing.T) {
	src := `struct Box:
	value: int

def allow_null(box: Box&?) -> int:
	if box == null:
		return 0
	return box.value

def ok() -> int:
	region scratch(256)
	box: Box& = new[scratch] Box(7)
	narrower: func(Box&) -> int = allow_null
	return narrower(box)
`
	result, errs := parseAndAnalyze(t, "function_param_contravariance_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "ok", "int")
}
