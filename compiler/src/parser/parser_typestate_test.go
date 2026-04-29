package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
	"llcontext/src/unparse"
)

func parseSourceFile(t *testing.T, src string) (*ast.File, []string) {
	t.Helper()
	l := lexer.New("test.llcontext", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := New(tokens)
	file := p.ParseFile("test.llcontext")
	return file, p.Errors()
}

func TestParseCharLiteralInConstDecl(t *testing.T) {
	file, errs := parseSourceFile(t, "const VALUE: char = '\\n'\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.ConstDecl)
	if !ok {
		t.Fatalf("expected const decl, got %T", file.Decls[0])
	}
	lit, ok := decl.Value.(*ast.CharLit)
	if !ok {
		t.Fatalf("expected char literal, got %T", decl.Value)
	}
	if lit.Value != "\n" {
		t.Fatalf("expected decoded newline char literal, got %q", lit.Value)
	}
	if named, ok := decl.Type.(*ast.NamedType); !ok || named.Name != "char" {
		t.Fatalf("expected const type char, got %T %#v", decl.Type, decl.Type)
	}
}

func TestParseStructDeclWithAggregateStateParam(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[?]:\n    value: i32&\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if !decl.HasStateParam {
		t.Fatal("expected struct declaration to record aggregate state parameter")
	}
	if decl.StateParamCount != 1 {
		t.Fatalf("expected one aggregate state parameter, got %d", decl.StateParamCount)
	}
	if len(decl.TypeParams) != 0 {
		t.Fatalf("expected no type params, got %v", decl.TypeParams)
	}
}

func TestParseStructDeclWithMultipleAggregateStateParams(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[?, ?]:\n    value: i32&\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if decl.StateParamCount != 2 {
		t.Fatalf("expected two aggregate state parameters, got %d", decl.StateParamCount)
	}
}

func TestParseStructDeclWithTypeAndAggregateStateParams(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[T][?]:\n    value: T\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.TypeParams) != 1 || decl.TypeParams[0] != "T" {
		t.Fatalf("expected one type param T, got %v", decl.TypeParams)
	}
	if !decl.HasStateParam {
		t.Fatal("expected struct declaration to record aggregate state parameter")
	}
	if decl.StateParamCount != 1 {
		t.Fatalf("expected one aggregate state parameter, got %d", decl.StateParamCount)
	}
}

func TestParseStructDeclWithTypeAndMultipleAggregateStateParams(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[T][?, ?]:\n    value: T\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.TypeParams) != 1 || decl.TypeParams[0] != "T" {
		t.Fatalf("expected one type param T, got %v", decl.TypeParams)
	}
	if decl.StateParamCount != 2 {
		t.Fatalf("expected two aggregate state parameters, got %d", decl.StateParamCount)
	}
}

func TestParseAggregateStateInstantiationTypeExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: Foo[&]) -> Foo[!]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	paramType, ok := decl.Params[0].Type.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state param type, got %T", decl.Params[0].Type)
	}
	if paramType.State != ast.RefStateNonNull {
		t.Fatalf("expected non-null aggregate state, got %v", paramType.State)
	}
	base, ok := paramType.Base.(*ast.NamedType)
	if !ok || base.Name != "Foo" {
		t.Fatalf("expected base named type Foo, got %T %v", paramType.Base, paramType.Base)
	}
	retType, ok := decl.ReturnType.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state return type, got %T", decl.ReturnType)
	}
	if retType.State != ast.RefStateNull {
		t.Fatalf("expected null aggregate state, got %v", retType.State)
	}
}

func TestParseAggregateStateInstantiationAfterGenericArgs(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep[T](value: Foo[T][&]) -> Foo[T][?]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	paramType, ok := decl.Params[0].Type.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state param type, got %T", decl.Params[0].Type)
	}
	base, ok := paramType.Base.(*ast.GenericType)
	if !ok {
		t.Fatalf("expected generic base type, got %T", paramType.Base)
	}
	if base.Name != "Foo" || len(base.Args) != 1 {
		t.Fatalf("expected Foo[T], got %#v", base)
	}
	arg, ok := base.Args[0].(*ast.NamedType)
	if !ok || arg.Name != "T" {
		t.Fatalf("expected generic arg T, got %T %#v", base.Args[0], base.Args[0])
	}
	if paramType.State != ast.RefStateNonNull {
		t.Fatalf("expected non-null aggregate state, got %v", paramType.State)
	}
	retType, ok := decl.ReturnType.(*ast.AggregateStateTypeExpr)
	if !ok || retType.State != ast.RefStateNullable {
		t.Fatalf("expected maybe aggregate state return type, got %T %#v", decl.ReturnType, decl.ReturnType)
	}
}

func TestParseAggregateStateInstantiationWithMultipleStates(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: Foo[!, &]) -> Foo[?, !]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	paramType, ok := decl.Params[0].Type.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state param type, got %T", decl.Params[0].Type)
	}
	if len(paramType.States) != 2 || paramType.States[0] != ast.RefStateNull || paramType.States[1] != ast.RefStateNonNull {
		t.Fatalf("expected [!, &] aggregate states, got %#v", paramType.States)
	}
	retType, ok := decl.ReturnType.(*ast.AggregateStateTypeExpr)
	if !ok {
		t.Fatalf("expected aggregate state return type, got %T", decl.ReturnType)
	}
	if len(retType.States) != 2 || retType.States[0] != ast.RefStateNullable || retType.States[1] != ast.RefStateNull {
		t.Fatalf("expected [?, !] aggregate states, got %#v", retType.States)
	}
}

func TestParseStructDeclRejectsNonPlaceholderStateMarker(t *testing.T) {
	_, errs := parseSourceFile(t, "struct Holder[?, &]:\n    value: i32\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for non-placeholder struct state declaration")
	}
	if !strings.Contains(errs[0], "struct state parameter declaration must use only [?] placeholders") {
		t.Fatalf("expected struct state parameter diagnostic, got %v", errs)
	}
}

func TestParseStructDeclWithNamedRefQualifiers(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[refstorage store, refstate state]:\n    value: store i32&[state]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.RefStorageParams) != 1 || decl.RefStorageParams[0] != "store" {
		t.Fatalf("expected refstorage param [store], got %v", decl.RefStorageParams)
	}
	if len(decl.RefStateParams) != 1 || decl.RefStateParams[0] != "state" {
		t.Fatalf("expected refstate param [state], got %v", decl.RefStateParams)
	}
	if len(decl.GenericParams) != 2 || decl.GenericParams[0].Kind != ast.GenericParamRefStorage || decl.GenericParams[1].Kind != ast.GenericParamRefState {
		t.Fatalf("expected ordered mixed generic params, got %#v", decl.GenericParams)
	}
	refType, ok := decl.Fields[0].Type.(*ast.RefType)
	if !ok {
		t.Fatalf("expected ref field type, got %T", decl.Fields[0].Type)
	}
	if refType.StorageParam != "store" {
		t.Fatalf("expected storage param store, got %q", refType.StorageParam)
	}
	if refType.StateParam != "state" {
		t.Fatalf("expected state param state, got %q", refType.StateParam)
	}
}

func TestParseNamedRefStateAttachesToNearestRef(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Holder[refstate s]:\n    value: i32&&[s]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.StructDecl)
	outer, ok := decl.Fields[0].Type.(*ast.RefType)
	if !ok {
		t.Fatalf("expected outer ref type, got %T", decl.Fields[0].Type)
	}
	inner, ok := outer.Elem.(*ast.RefType)
	if !ok {
		t.Fatalf("expected nested inner ref type, got %T", outer.Elem)
	}
	if outer.StateParam != "s" {
		t.Fatalf("expected outer ref to carry state param s, got %q", outer.StateParam)
	}
	if inner.StateParam != "" {
		t.Fatalf("expected inner ref to have no named state param, got %q", inner.StateParam)
	}
}

func TestParseLegacyNullableRefArraySuffixStillWorks(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: heap i32&?[COUNT]) -> void:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	arrayType, ok := decl.Params[0].Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("expected array type, got %T", decl.Params[0].Type)
	}
	refType, ok := arrayType.Elem.(*ast.RefType)
	if !ok {
		t.Fatalf("expected array element ref type, got %T", arrayType.Elem)
	}
	if refType.State != ast.RefStateNullable {
		t.Fatalf("expected nullable ref state, got %v", refType.State)
	}
	if refType.StateParam != "" {
		t.Fatalf("expected no named refstate param, got %q", refType.StateParam)
	}
}

func TestParsePackedOpenAndViewStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n\ndef fold(node: Expr, store: Expr.Store[Local]) -> int:\n    open node in store as Expr.Lit(value: value):\n        view node in store as Expr.Lit(lit):\n            return value + lit.span\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	openStmt, ok := decl.Body[0].(*ast.OpenStmt)
	if !ok {
		t.Fatalf("expected open stmt, got %T", decl.Body[0])
	}
	if openStmt.Pattern == nil {
		t.Fatal("expected open stmt to record a variant pattern")
	}
	if openStmt.Pattern.EnumName != "Expr" || openStmt.Pattern.Variant != "Lit" {
		t.Fatalf("expected Expr.Lit open pattern, got %#v", openStmt.Pattern)
	}
	if len(openStmt.Pattern.Args) != 1 {
		t.Fatalf("expected one open binding arg, got %d", len(openStmt.Pattern.Args))
	}
	bindPattern, ok := openStmt.Pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || bindPattern.Name != "value" {
		t.Fatalf("expected value payload binding, got %T %#v", openStmt.Pattern.Args[0].Pattern, openStmt.Pattern.Args[0].Pattern)
	}
	viewStmt, ok := openStmt.Body[0].(*ast.ViewStmt)
	if !ok {
		t.Fatalf("expected view stmt, got %T", openStmt.Body[0])
	}
	if viewStmt.Pattern == nil {
		t.Fatal("expected view stmt to record a view pattern")
	}
	if viewStmt.Pattern.EnumName != "Expr" || viewStmt.Pattern.Variant != "Lit" || viewStmt.Pattern.Name != "lit" {
		t.Fatalf("expected Expr.Lit(lit) view pattern, got %#v", viewStmt.Pattern)
	}
	if len(viewStmt.Pattern.Args) != 0 {
		t.Fatalf("expected alias-form view pattern to have no payload args, got %#v", viewStmt.Pattern.Args)
	}
}

func TestParsePackedOpenNestedPayloadPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Int(value: int)\n    Add(left: Expr, right: Expr)\n\ndef left_value(node: Expr, store: Expr.Store[Local]) -> int:\n    open node in store as Expr.Add(Expr.Int(value), rhs):\n        return value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	openStmt, ok := decl.Body[0].(*ast.OpenStmt)
	if !ok {
		t.Fatalf("expected open stmt, got %T", decl.Body[0])
	}
	if openStmt.Pattern == nil || len(openStmt.Pattern.Args) != 2 {
		t.Fatalf("expected two open payload patterns, got %#v", openStmt.Pattern)
	}
	leftPattern, ok := openStmt.Pattern.Args[0].Pattern.(*ast.MatchVariantPattern)
	if !ok || leftPattern.EnumName != "Expr" || leftPattern.Variant != "Int" || len(leftPattern.Args) != 1 {
		t.Fatalf("expected nested Expr.Int(value) pattern, got %#v", openStmt.Pattern.Args[0].Pattern)
	}
	leftBind, ok := leftPattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || leftBind.Name != "value" {
		t.Fatalf("expected nested bind pattern value, got %T %#v", leftPattern.Args[0].Pattern, leftPattern.Args[0].Pattern)
	}
	rightBind, ok := openStmt.Pattern.Args[1].Pattern.(*ast.MatchBindPattern)
	if !ok || rightBind.Name != "rhs" {
		t.Fatalf("expected rhs bind pattern, got %T %#v", openStmt.Pattern.Args[1].Pattern, openStmt.Pattern.Args[1].Pattern)
	}
}

func TestParseEnumVariantIsCondition(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    Add(left: int, right: int)\n\ndef is_int(node: Expr) -> bool:\n    if node is Expr.Int:\n        return true\n    return false\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if stmt, got %T", decl.Body[0])
	}
	cond, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary condition, got %T", ifStmt.Cond)
	}
	if cond.Op != lexer.TOKEN_IS {
		t.Fatalf("expected is operator, got %s", lexer.TokenName(cond.Op))
	}
	typeExpr, ok := cond.Right.(*ast.TypeExprExpr)
	if !ok {
		t.Fatalf("expected typed is RHS, got %T", cond.Right)
	}
	named, ok := typeExpr.Type.(*ast.NamedType)
	if !ok || named.Name != "Expr.Int" {
		t.Fatalf("expected Expr.Int typed RHS, got %#v", typeExpr.Type)
	}
}

func TestParseEnumVariantIsConditionWithPayloadPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Float(PI: f64)\n\ndef is_pi(node: Expr) -> bool:\n    return node is Expr.Float(3.14)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cond, ok := ret.Value.(*ast.BinaryExpr)
	if !ok {
		t.Fatalf("expected binary condition, got %T", ret.Value)
	}
	variantTarget, ok := cond.Right.(*ast.VariantTestExpr)
	if !ok {
		t.Fatalf("expected variant is target, got %T", cond.Right)
	}
	if variantTarget.Pattern == nil || variantTarget.Pattern.EnumName != "Expr" || variantTarget.Pattern.Variant != "Float" {
		t.Fatalf("expected Expr.Float payload test, got %#v", variantTarget.Pattern)
	}
	if len(variantTarget.Pattern.Args) != 1 {
		t.Fatalf("expected one payload pattern, got %#v", variantTarget.Pattern.Args)
	}
	if _, ok := variantTarget.Pattern.Args[0].Pattern.(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected positional literal payload pattern, got %T", variantTarget.Pattern.Args[0].Pattern)
	}
}

func TestParseIsConditionWithAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    LT = 1\n    LTEQ = 2\n    GT = 3\n\ndef is_rel(kind: Tok) -> bool:\n    return kind is .LT | .LTEQ | .GT\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cond, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || cond.Op != lexer.TOKEN_IS {
		t.Fatalf("expected is-expression return, got %T %#v", ret.Value, ret.Value)
	}
	alts, ok := cond.Right.(*ast.IsPatternExpr)
	if !ok {
		t.Fatalf("expected multi-target is-pattern RHS, got %T", cond.Right)
	}
	if len(alts.Targets) != 3 {
		t.Fatalf("expected three is-pattern targets, got %#v", alts.Targets)
	}
	for i, target := range alts.Targets {
		if _, ok := target.(*ast.ShorthandMemberExpr); !ok {
			t.Fatalf("expected shorthand member target at %d, got %T", i, target)
		}
	}
}

func TestParseStructFieldMatchPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    INTEGER = 1\n\nstruct Span:\n    start: int\n    finish: int\n\nstruct Token:\n    kind: Tok\n    span: Span\n    value: int\n\ndef score(tok: Token) -> int:\n    match tok:\n        Token(kind: .INTEGER, span: Span(start: start), value: value):\n            return start + value\n        _:\n            return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[3].(*ast.FuncDecl)
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected match stmt, got %T", decl.Body[0])
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected struct match pattern, got %T", matchStmt.Arms[0].Pattern)
	}
	if pattern.TypeName != "Token" || len(pattern.Args) != 3 {
		t.Fatalf("unexpected top-level struct pattern %#v", pattern)
	}
	kindPattern, ok := pattern.Args[0].Pattern.(*ast.MatchLiteralPattern)
	if !ok {
		t.Fatalf("expected literal kind pattern, got %T", pattern.Args[0].Pattern)
	}
	if _, ok := kindPattern.Value.(*ast.ShorthandMemberExpr); !ok {
		t.Fatalf("expected shorthand member kind pattern, got %T", kindPattern.Value)
	}
	spanPattern, ok := pattern.Args[1].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected nested span struct pattern, got %T", pattern.Args[1].Pattern)
	}
	if spanPattern.TypeName != "Span" || len(spanPattern.Args) != 1 || spanPattern.Args[0].Name != "start" {
		t.Fatalf("unexpected nested span pattern %#v", spanPattern)
	}
	startBind, ok := spanPattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || startBind.Name != "start" {
		t.Fatalf("expected start bind pattern, got %T %#v", spanPattern.Args[0].Pattern, spanPattern.Args[0].Pattern)
	}
	valueBind, ok := pattern.Args[2].Pattern.(*ast.MatchBindPattern)
	if !ok || valueBind.Name != "value" {
		t.Fatalf("expected value bind pattern, got %T %#v", pattern.Args[2].Pattern, pattern.Args[2].Pattern)
	}
}

func TestParseStructPatternIsCondition(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    INTEGER = 1\n\nstruct Span:\n    start: int\n    finish: int\n\nstruct Token:\n    kind: Tok\n    span: Span\n\ndef is_integer(tok: Token) -> bool:\n    return tok is Token(kind: .INTEGER, span: Span(start: 1))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[3].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	cond, ok := ret.Value.(*ast.BinaryExpr)
	if !ok || cond.Op != lexer.TOKEN_IS {
		t.Fatalf("expected is-expression return, got %T %#v", ret.Value, ret.Value)
	}
	target, ok := cond.Right.(*ast.StructTestExpr)
	if !ok {
		t.Fatalf("expected struct test target, got %T", cond.Right)
	}
	if target.Pattern == nil || target.Pattern.TypeName != "Token" || len(target.Pattern.Args) != 2 {
		t.Fatalf("unexpected struct is target %#v", target.Pattern)
	}
	if _, ok := target.Pattern.Args[0].Pattern.(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected literal kind field pattern, got %T", target.Pattern.Args[0].Pattern)
	}
	spanPattern, ok := target.Pattern.Args[1].Pattern.(*ast.MatchStructPattern)
	if !ok || spanPattern.TypeName != "Span" {
		t.Fatalf("expected nested span struct is-pattern, got %T %#v", target.Pattern.Args[1].Pattern, target.Pattern.Args[1].Pattern)
	}
	if len(spanPattern.Args) != 1 || spanPattern.Args[0].Name != "start" {
		t.Fatalf("unexpected span field args %#v", spanPattern.Args)
	}
	if _, ok := spanPattern.Args[0].Pattern.(*ast.MatchLiteralPattern); !ok {
		t.Fatalf("expected literal nested field pattern, got %T", spanPattern.Args[0].Pattern)
	}
}

func TestParseStructPatternIsConditionWithBindings(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    INTEGER = 1\n\nstruct Span:\n    start: int\n    finish: int\n\nstruct Token:\n    kind: Tok\n    span: Span\n    value: int\n\ndef score(tok: Token) -> int:\n    if tok is Token(kind: .INTEGER, span: Span(start: start), value: value):\n        return start + value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[3].(*ast.FuncDecl)
	ifStmt := decl.Body[0].(*ast.IfStmt)
	cond := ifStmt.Cond.(*ast.BinaryExpr)
	target, ok := cond.Right.(*ast.StructTestExpr)
	if !ok || target.Pattern == nil {
		t.Fatalf("expected struct test condition, got %T %#v", cond.Right, cond.Right)
	}
	spanPattern, ok := target.Pattern.Args[1].Pattern.(*ast.MatchStructPattern)
	if !ok {
		t.Fatalf("expected nested span pattern, got %T", target.Pattern.Args[1].Pattern)
	}
	startBind, ok := spanPattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || startBind.Name != "start" {
		t.Fatalf("expected nested start bind, got %T %#v", spanPattern.Args[0].Pattern, spanPattern.Args[0].Pattern)
	}
	valueBind, ok := target.Pattern.Args[2].Pattern.(*ast.MatchBindPattern)
	if !ok || valueBind.Name != "value" {
		t.Fatalf("expected value bind, got %T %#v", target.Pattern.Args[2].Pattern, target.Pattern.Args[2].Pattern)
	}
}

func TestParseVariantPatternIsConditionWithBindings(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Pair(left: i64, right: i64)\n\ndef score(node: Expr) -> i64:\n    if node is Expr.Pair(left, right):\n        return left + right\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ifStmt := decl.Body[0].(*ast.IfStmt)
	cond := ifStmt.Cond.(*ast.BinaryExpr)
	target, ok := cond.Right.(*ast.VariantTestExpr)
	if !ok || target.Pattern == nil {
		t.Fatalf("expected variant test condition, got %T %#v", cond.Right, cond.Right)
	}
	leftBind, ok := target.Pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || leftBind.Name != "left" {
		t.Fatalf("expected left bind, got %T %#v", target.Pattern.Args[0].Pattern, target.Pattern.Args[0].Pattern)
	}
	rightBind, ok := target.Pattern.Args[1].Pattern.(*ast.MatchBindPattern)
	if !ok || rightBind.Name != "right" {
		t.Fatalf("expected right bind, got %T %#v", target.Pattern.Args[1].Pattern, target.Pattern.Args[1].Pattern)
	}
}

func TestParseIfLetCondition(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(maybe: i64?) -> i64:\n    if let value = maybe and value > 0:\n        return value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt := decl.Body[0].(*ast.IfStmt)
	cond, ok := ifStmt.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != lexer.TOKEN_AND {
		t.Fatalf("expected and-condition, got %T %#v", ifStmt.Cond, ifStmt.Cond)
	}
	letExpr, ok := cond.Left.(*ast.OptionalBindExpr)
	if !ok || letExpr.Name != "value" {
		t.Fatalf("expected let-bind condition on left, got %T %#v", cond.Left, cond.Left)
	}
}

func TestParseGuardElseStatement(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(maybe: i64?) -> i64:\n    guard maybe != null else return 0\n    return 1\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected lowered if stmt, got %T", decl.Body[0])
	}
	cond, ok := ifStmt.Cond.(*ast.UnaryExpr)
	if !ok || cond.Op != lexer.TOKEN_NOT {
		t.Fatalf("expected inverted guard condition, got %T %#v", ifStmt.Cond, ifStmt.Cond)
	}
	if _, ok := ifStmt.Then[0].(*ast.ReturnStmt); !ok {
		t.Fatalf("expected guard else branch to lower to return, got %T", ifStmt.Then[0])
	}
}

func TestParseWithBundleSpread(t *testing.T) {
	file, errs := parseSourceFile(t, "bundle ParseCtx implicit:\n    offset: i64\n\ndef inner() with ParseCtx -> i64:\n    return offset\n\ndef keep() -> i64:\n    offset: i64 = 7\n    return inner() with ParseCtx(.., offset = offset)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	ret := decl.Body[1].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok || len(call.WithBundles) != 1 {
		t.Fatalf("expected call with one bundle, got %T %#v", ret.Value, ret.Value)
	}
	bundle := call.WithBundles[0]
	if !bundle.Spread {
		t.Fatalf("expected ParseCtx bundle to record spread marker, got %#v", bundle)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "ParseCtx(.., offset = offset)") {
		t.Fatalf("expected unparse output to preserve bundle spread, got:\n%s", formatted)
	}
}

func TestParseVisitArmAlternativesAndGuard(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(expr)\n    node Expr:\n        Int(value: i64)\n        Float(value: f64)\n\ndef score(node: Lua.Expr) -> i64:\n    return visit node:\n        Lua.Expr.Int(expr) | Lua.Expr.Float(expr) when expr.span > 0:\n            expr.span\n        _:\n            0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	visitExpr, ok := ret.Value.(*ast.VisitExpr)
	if !ok {
		t.Fatalf("expected visit expr, got %T", ret.Value)
	}
	if len(visitExpr.Arms) != 3 {
		t.Fatalf("expected visit arm alternatives to expand into three arms, got %d", len(visitExpr.Arms))
	}
	for i := 0; i < 2; i++ {
		if visitExpr.Arms[i].Guard == nil {
			t.Fatalf("expected expanded visit arm %d to keep guard", i)
		}
		if visitExpr.Arms[i].BindName != "expr" {
			t.Fatalf("expected expanded visit arm %d bind name expr, got %#v", i, visitExpr.Arms[i])
		}
	}
	if !visitExpr.Arms[2].Wildcard {
		t.Fatalf("expected final wildcard arm, got %#v", visitExpr.Arms[2])
	}
}

func TestParseStructDeclWithNamedStateCasesAndDeriveBlock(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Player[state Alive | Dead]:\n    health: int\n\n    derive state:\n        Alive when self.health > 0\n        Dead when self.health <= 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.NamedStateCases) != 2 || decl.NamedStateCases[0] != "Alive" || decl.NamedStateCases[1] != "Dead" {
		t.Fatalf("expected named state cases [Alive Dead], got %#v", decl.NamedStateCases)
	}
	if len(decl.GenericParams) != 1 || decl.GenericParams[0].Kind != ast.GenericParamState {
		t.Fatalf("expected a single struct state generic param, got %#v", decl.GenericParams)
	}
	if len(decl.DerivedStates) != 2 {
		t.Fatalf("expected two derived-state clauses, got %d", len(decl.DerivedStates))
	}
	if decl.DerivedStates[0].StateName != "Alive" || decl.DerivedStates[1].StateName != "Dead" {
		t.Fatalf("unexpected derived-state clause names: %#v", decl.DerivedStates)
	}
	if _, ok := decl.DerivedStates[0].Condition.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected first derived-state condition to be binary, got %T", decl.DerivedStates[0].Condition)
	}
}

func TestParseNamedStateUnionTypeExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: Player[Alive | Dead]) -> Player[Alive | Dead]:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	paramType, ok := decl.Params[0].Type.(*ast.GenericType)
	if !ok {
		t.Fatalf("expected generic type, got %T", decl.Params[0].Type)
	}
	if len(paramType.Args) != 1 {
		t.Fatalf("expected one state arg, got %d", len(paramType.Args))
	}
	stateSet, ok := paramType.Args[0].(*ast.StateSetTypeExpr)
	if !ok {
		t.Fatalf("expected state-set type arg, got %T", paramType.Args[0])
	}
	if len(stateSet.Cases) != 2 || stateSet.Cases[0] != "Alive" || stateSet.Cases[1] != "Dead" {
		t.Fatalf("expected Alive | Dead state-set, got %#v", stateSet.Cases)
	}
}

func TestParseTypedStateIsConditionAndExplicitStatefulStructLiteral(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(node: Player[Alive | Dead]) -> Player[Alive]:\n    if node is Player[Alive]:\n        return Player[Alive](1)\n    return Player[Alive](2)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if stmt, got %T", decl.Body[0])
	}
	cond := ifStmt.Cond.(*ast.BinaryExpr)
	typed, ok := cond.Right.(*ast.TypeExprExpr)
	if !ok {
		t.Fatalf("expected typed is target, got %T", cond.Right)
	}
	target, ok := typed.Type.(*ast.GenericType)
	if !ok || target.Name != "Player" || len(target.Args) != 1 {
		t.Fatalf("expected Player[Alive] is target, got %#v", typed.Type)
	}
	ret := ifStmt.Then[0].(*ast.ReturnStmt)
	lit, ok := ret.Value.(*ast.StructLitExpr)
	if !ok {
		t.Fatalf("expected explicit stateful struct literal, got %T", ret.Value)
	}
	if lit.Name != "Player" || len(lit.TypeArgs) != 1 {
		t.Fatalf("expected Player[Alive](...) literal, got %#v", lit)
	}
	if _, ok := lit.TypeArgs[0].(*ast.NamedType); !ok {
		t.Fatalf("expected named state type arg, got %T", lit.TypeArgs[0])
	}
}

func TestParsePackedViewPayloadDestructureStatement(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    common:\n        span: int\n    Add(left: int, right: int)\n\ndef fold(node: Expr, store: Expr.Store[Local]) -> int:\n    view node in store as Expr.Add(left: lhs, right: rhs):\n        return lhs + rhs + node.span\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	viewStmt, ok := decl.Body[0].(*ast.ViewStmt)
	if !ok {
		t.Fatalf("expected view stmt, got %T", decl.Body[0])
	}
	if viewStmt.Pattern == nil {
		t.Fatal("expected view stmt to record a view pattern")
	}
	if viewStmt.Pattern.Name != "" {
		t.Fatalf("expected destructuring-form view pattern to have no alias name, got %q", viewStmt.Pattern.Name)
	}
	if len(viewStmt.Pattern.Args) != 2 {
		t.Fatalf("expected two destructured payload args, got %d", len(viewStmt.Pattern.Args))
	}
	if viewStmt.Pattern.Args[0].Name != "left" || viewStmt.Pattern.Args[1].Name != "right" {
		t.Fatalf("expected named payload bindings left/right, got %#v", viewStmt.Pattern.Args)
	}
	leftBind, ok := viewStmt.Pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || leftBind.Name != "lhs" {
		t.Fatalf("expected lhs bind pattern, got %T %#v", viewStmt.Pattern.Args[0].Pattern, viewStmt.Pattern.Args[0].Pattern)
	}
	rightBind, ok := viewStmt.Pattern.Args[1].Pattern.(*ast.MatchBindPattern)
	if !ok || rightBind.Name != "rhs" {
		t.Fatalf("expected rhs bind pattern, got %T %#v", viewStmt.Pattern.Args[1].Pattern, viewStmt.Pattern.Args[1].Pattern)
	}
}

func TestParseAlignedStructAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@align(32)\nstruct Vec4:\n    x: f32\n    y: f32\n    z: f32\n    w: f32\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one struct annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "align" {
		t.Fatalf("expected align annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "32" {
		t.Fatalf("expected align(32), got %#v", decl.Annotations[0].Args)
	}
}

func TestParseCachelineAlignedStructAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@cacheline_aligned\nstruct Counter:\n    value: i64\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one struct annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "cacheline_aligned" {
		t.Fatalf("expected cacheline_aligned annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected cacheline_aligned to take no args, got %#v", decl.Annotations[0].Args)
	}
}

func TestParsePackedEnumProfileAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@packed_profile(build_heavy)\npacked enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.EnumDecl)
	if !ok {
		t.Fatalf("expected enum decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one enum annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "packed_profile" {
		t.Fatalf("expected packed_profile annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "build_heavy" {
		t.Fatalf("expected packed_profile(build_heavy), got %#v", decl.Annotations[0].Args)
	}
}

func TestParseInlineFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@inline(always)\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "inline" {
		t.Fatalf("expected inline annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "always" {
		t.Fatalf("expected inline(always), got %#v", decl.Annotations[0].Args)
	}
}

func TestParseNoRecurseFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@norecurse\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "norecurse" {
		t.Fatalf("expected norecurse annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected norecurse to take no args, got %#v", decl.Annotations[0].Args)
	}
}

func TestParseHotFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@hot\ndef fold(value: int) -> int:\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "hot" {
		t.Fatalf("expected hot annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 0 {
		t.Fatalf("expected hot to take no args, got %#v", decl.Annotations[0].Args)
	}
}

func TestParseGuardNonNullFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@guard_nonnull(box)\ndef has_box(box: Box&?) -> bool:\n    return box != null\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "guard_nonnull" {
		t.Fatalf("expected guard_nonnull annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "box" {
		t.Fatalf("expected guard_nonnull(box), got %#v", decl.Annotations[0].Args)
	}
}

func TestParseGuardVariantFunctionAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@guard_variant(node, Expr.Int)\ndef is_int(node: Expr) -> bool:\n    return node is Expr.Int\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 1 {
		t.Fatalf("expected one function annotation, got %d", len(decl.Annotations))
	}
	if decl.Annotations[0].Name != "guard_variant" {
		t.Fatalf("expected guard_variant annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 2 || decl.Annotations[0].Args[0] != "node" || decl.Annotations[0].Args[1] != "Expr.Int" {
		t.Fatalf("expected guard_variant(node, Expr.Int), got %#v", decl.Annotations[0].Args)
	}
}

func TestParseLikelyIfHint(t *testing.T) {
	file, errs := parseSourceFile(t, "def fold(value: bool) -> int:\n    if likely value:\n        return 1\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	stmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected first stmt to be if, got %T", decl.Body[0])
	}
	if stmt.Hint != ast.BranchHintLikely {
		t.Fatalf("expected if stmt to record likely hint, got %v", stmt.Hint)
	}
	if ident, ok := stmt.Cond.(*ast.Ident); !ok || ident.Name != "value" {
		t.Fatalf("expected raw condition ident value, got %T %#v", stmt.Cond, stmt.Cond)
	}
}

func TestParseUnlikelyWhileHint(t *testing.T) {
	file, errs := parseSourceFile(t, "def fold(value: bool) -> int:\n    while unlikely value:\n        return 1\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	stmt, ok := decl.Body[0].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected first stmt to be while, got %T", decl.Body[0])
	}
	if stmt.Hint != ast.BranchHintUnlikely {
		t.Fatalf("expected while stmt to record unlikely hint, got %v", stmt.Hint)
	}
	if ident, ok := stmt.Cond.(*ast.Ident); !ok || ident.Name != "value" {
		t.Fatalf("expected raw condition ident value, got %T %#v", stmt.Cond, stmt.Cond)
	}
}

func TestParsePackedViewSurfaceType(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n\ndef keep(view_value: packedview[Expr.Lit]) -> packedview[Expr.Lit]:\n    return view_value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	paramType, ok := decl.Params[0].Type.(*ast.BuiltinTypeExpr)
	if !ok {
		t.Fatalf("expected builtin packedview type, got %T", decl.Params[0].Type)
	}
	if paramType.Name != "packedview" {
		t.Fatalf("expected packedview builtin type name, got %q", paramType.Name)
	}
	if len(paramType.TypeArgs) != 1 {
		t.Fatalf("expected one packedview type arg, got %d", len(paramType.TypeArgs))
	}
	variantType, ok := paramType.TypeArgs[0].(*ast.NamedType)
	if !ok {
		t.Fatalf("expected packedview variant named type, got %T", paramType.TypeArgs[0])
	}
	if variantType.Name != "Expr.Lit" {
		t.Fatalf("expected packedview variant Expr.Lit, got %q", variantType.Name)
	}
	retType, ok := decl.ReturnType.(*ast.BuiltinTypeExpr)
	if !ok {
		t.Fatalf("expected builtin packedview return type, got %T", decl.ReturnType)
	}
	if retType.Name != "packedview" {
		t.Fatalf("expected packedview return type name, got %q", retType.Name)
	}
}

func TestParseTreeViewSurfaceType(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Nil\n        Binary(left: Expr, right: Expr)\n\ndef keep(view_value: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:\n    return view_value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	paramType, ok := decl.Params[0].Type.(*ast.BuiltinTypeExpr)
	if !ok {
		t.Fatalf("expected builtin treeview type, got %T", decl.Params[0].Type)
	}
	if paramType.Name != "treeview" {
		t.Fatalf("expected treeview builtin type name, got %q", paramType.Name)
	}
	if len(paramType.TypeArgs) != 1 {
		t.Fatalf("expected one treeview type arg, got %d", len(paramType.TypeArgs))
	}
	variantType, ok := paramType.TypeArgs[0].(*ast.NamedType)
	if !ok {
		t.Fatalf("expected treeview variant named type, got %T", paramType.TypeArgs[0])
	}
	if variantType.Name != "Lua.Expr.Binary" {
		t.Fatalf("expected treeview variant Lua.Expr.Binary, got %q", variantType.Name)
	}
	retType, ok := decl.ReturnType.(*ast.BuiltinTypeExpr)
	if !ok {
		t.Fatalf("expected builtin treeview return type, got %T", decl.ReturnType)
	}
	if retType.Name != "treeview" {
		t.Fatalf("expected treeview return type name, got %q", retType.Name)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "def keep(view_value: Lua.Expr.Binary) -> Lua.Expr.Binary:") {
		t.Fatalf("expected formatter to canonicalize treeview surface types, got:\n%s", formatted)
	}
}

func TestParseOpenAndViewRemainContextualIdentifiers(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> int:\n    open: int = 1\n    view: int = open\n    open(view)\n    return view\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if _, ok := decl.Body[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("expected first stmt to stay a var decl, got %T", decl.Body[0])
	}
	if _, ok := decl.Body[1].(*ast.VarDeclStmt); !ok {
		t.Fatalf("expected second stmt to stay a var decl, got %T", decl.Body[1])
	}
	exprStmt, ok := decl.Body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected third stmt to stay an expr stmt, got %T", decl.Body[2])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected open(view) to parse as a call, got %T", exprStmt.Expr)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "open" {
		t.Fatalf("expected call callee open, got %T %#v", call.Func, call.Func)
	}
}

func TestParseTreeVisitAndFoldExprs(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Nil\n        Unary(child expr: Expr)\n        Binary(child left: Expr, child right: Expr)\n        Call(child callee: Expr, children args: darray[Expr])\n\ndef kind(node: Lua.Expr) -> i64:\n    return visit node:\n        Lua.Expr.Nil(expr):\n            0\n        Lua.Expr.Binary(expr):\n            expr.left.span\n\ndef score(node: Lua.Expr) -> i64:\n    return fold node as Lua.Node into i64:\n        Lua.Expr.Nil(expr, children):\n            expr.span + children.len.i64()\n        Lua.Expr.Unary(expr, expr: inner):\n            try inner + expr.span\n        Lua.Expr.Binary(expr, left, right):\n            try left + try right + expr.span\n        Lua.Expr.Call(expr, callee, args: arg_values):\n            try callee + arg_values.len.i64() + expr.span\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	kindDecl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected first func decl, got %T", file.Decls[1])
	}
	retKind, ok := kindDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", kindDecl.Body[0])
	}
	visitExpr, ok := retKind.Value.(*ast.VisitExpr)
	if !ok {
		t.Fatalf("expected visit expr, got %T", retKind.Value)
	}
	if visitExpr.Root != nil {
		t.Fatalf("expected implicit visit root, got %#v", visitExpr.Root)
	}
	if len(visitExpr.Arms) != 2 || visitExpr.Arms[1].BindName != "expr" {
		t.Fatalf("unexpected visit arms: %#v", visitExpr.Arms)
	}
	foldDecl, ok := file.Decls[2].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected second func decl, got %T", file.Decls[2])
	}
	retFold, ok := foldDecl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", foldDecl.Body[0])
	}
	foldExpr, ok := retFold.Value.(*ast.FoldExpr)
	if !ok {
		t.Fatalf("expected fold expr, got %T", retFold.Value)
	}
	rootType, ok := foldExpr.Root.(*ast.NamedType)
	if !ok || rootType.Name != "Lua.Node" {
		t.Fatalf("expected fold root Lua.Node, got %#v", foldExpr.Root)
	}
	resultType, ok := foldExpr.ResultType.(*ast.NamedType)
	if !ok || resultType.Name != "i64" {
		t.Fatalf("expected fold result i64, got %#v", foldExpr.ResultType)
	}
	if len(foldExpr.Arms) != 4 || foldExpr.Arms[0].ChildResultsName != "children" {
		t.Fatalf("unexpected fold arms: %#v", foldExpr.Arms)
	}
	if len(foldExpr.Arms[1].ChildBindings) != 1 || foldExpr.Arms[1].ChildBindings[0].FieldName != "expr" || foldExpr.Arms[1].ChildBindings[0].BindName != "inner" {
		t.Fatalf("unexpected unary fold child bindings: %#v", foldExpr.Arms[1].ChildBindings)
	}
	if len(foldExpr.Arms[2].ChildBindings) != 2 || foldExpr.Arms[2].ChildBindings[0].FieldName != "left" || foldExpr.Arms[2].ChildBindings[0].BindName != "left" || foldExpr.Arms[2].ChildBindings[1].FieldName != "right" || foldExpr.Arms[2].ChildBindings[1].BindName != "right" {
		t.Fatalf("unexpected binary fold child bindings: %#v", foldExpr.Arms[2].ChildBindings)
	}
	if len(foldExpr.Arms[3].ChildBindings) != 2 || foldExpr.Arms[3].ChildBindings[0].FieldName != "callee" || foldExpr.Arms[3].ChildBindings[0].BindName != "callee" || foldExpr.Arms[3].ChildBindings[1].FieldName != "args" || foldExpr.Arms[3].ChildBindings[1].BindName != "arg_values" {
		t.Fatalf("unexpected call fold child bindings: %#v", foldExpr.Arms[3].ChildBindings)
	}
}

func TestParseTreeRewriteExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Int(value: i64)\n        Binary(child left: Expr, child right: Expr)\n\ndef simplify(node: Lua.Expr) -> Lua.Expr:\n    in perm:\n        return rewrite node as Lua.Expr default:\n            Lua.Expr.Binary(expr, left, right):\n                default{span = expr.span, left, right}\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	inStmt, ok := decl.Body[0].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in store stmt, got %T", decl.Body[0])
	}
	if len(inStmt.Body) != 1 {
		t.Fatalf("expected in store body with one stmt, got %#v", inStmt.Body)
	}
	ret, ok := inStmt.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt inside in store, got %T", inStmt.Body[0])
	}
	rewriteExpr, ok := ret.Value.(*ast.FoldExpr)
	if !ok {
		t.Fatalf("expected rewrite to parse as fold-backed expr, got %T", ret.Value)
	}
	if rewriteExpr.Keyword != "rewrite" {
		t.Fatalf("expected rewrite keyword marker, got %q", rewriteExpr.Keyword)
	}
	rootType, ok := rewriteExpr.Root.(*ast.NamedType)
	if !ok || rootType.Name != "Lua.Expr" {
		t.Fatalf("expected rewrite root Lua.Expr, got %#v", rewriteExpr.Root)
	}
	resultType, ok := rewriteExpr.ResultType.(*ast.NamedType)
	if !ok || resultType.Name != "Lua.Expr" {
		t.Fatalf("expected rewrite result Lua.Expr, got %#v", rewriteExpr.ResultType)
	}
	if !rewriteExpr.RewriteDefault {
		t.Fatalf("expected rewrite default flag, got %#v", rewriteExpr)
	}
	if len(rewriteExpr.Arms) != 1 || len(rewriteExpr.Arms[0].ChildBindings) != 2 {
		t.Fatalf("unexpected rewrite arms: %#v", rewriteExpr.Arms)
	}
	stmt, ok := rewriteExpr.Arms[0].Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt arm body, got %T", rewriteExpr.Arms[0].Body[0])
	}
	update, ok := stmt.Expr.(*ast.RecordUpdateExpr)
	if !ok {
		t.Fatalf("expected rewrite default update, got %T", stmt.Expr)
	}
	baseIdent, ok := update.Base.(*ast.Ident)
	if !ok || baseIdent.Name != "default" {
		t.Fatalf("expected default base for rewrite update, got %#v", update.Base)
	}
	if got := update.ArgName(0); got != "span" {
		t.Fatalf("expected first record update field to be span, got %q", got)
	}
	if got := update.ArgName(1); got != "left" {
		t.Fatalf("expected second record update field to be left, got %q", got)
	}
	if got := update.ArgName(2); got != "right" {
		t.Fatalf("expected third record update field to be right, got %q", got)
	}
	if got := unparse.FormatExpr(rewriteExpr); !strings.HasPrefix(got, "rewrite node as Lua.Expr default:") {
		t.Fatalf("expected unparse to preserve rewrite spelling, got:\n%s", got)
	}
}

func TestParseTreeAttributeDecl(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Nil\n        Binary(child left: Expr, child right: Expr)\n\nattribute Lua.Node.checksum -> i64 error[LuaFrontendError]:\n    Lua.Expr.Binary(node, left, right):\n        lua_binary_checksum(node.span, left.checksum, right.checksum)\n    _:\n        0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.AttributeDecl)
	if !ok {
		t.Fatalf("expected attribute decl, got %T", file.Decls[1])
	}
	receiver, ok := decl.Receiver.(*ast.NamedType)
	if !ok || receiver.Name != "Lua.Node" {
		t.Fatalf("expected receiver Lua.Node, got %#v", decl.Receiver)
	}
	if decl.Name != "checksum" {
		t.Fatalf("expected attribute name checksum, got %q", decl.Name)
	}
	retType, ok := decl.ReturnType.(*ast.ErrorUnionTypeExpr)
	if !ok {
		t.Fatalf("expected fallible return type, got %#v", decl.ReturnType)
	}
	valueType, ok := retType.Value.(*ast.NamedType)
	if !ok || valueType.Name != "i64" {
		t.Fatalf("expected return value type i64, got %#v", retType.Value)
	}
	errorType, ok := retType.Errors.(*ast.ErrorSetExpr)
	if !ok || len(errorType.Tags) != 1 || errorType.Tags[0].SetName != "LuaFrontendError" || errorType.Tags[0].Tag != "" {
		t.Fatalf("expected error set LuaFrontendError, got %#v", retType.Errors)
	}
	if len(decl.Arms) != 2 {
		t.Fatalf("expected two attribute arms, got %#v", decl.Arms)
	}
	if decl.Arms[0].TargetName != "Lua.Expr.Binary" || decl.Arms[0].BindName != "node" {
		t.Fatalf("unexpected first attribute arm: %#v", decl.Arms[0])
	}
	if len(decl.Arms[0].ChildBindings) != 2 || decl.Arms[0].ChildBindings[0].FieldName != "left" || decl.Arms[0].ChildBindings[1].FieldName != "right" {
		t.Fatalf("unexpected child bindings: %#v", decl.Arms[0].ChildBindings)
	}
	if !decl.Arms[1].Wildcard {
		t.Fatalf("expected wildcard fallback arm, got %#v", decl.Arms[1])
	}
}

func TestParseSequenceRewriteExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep_non_zero(owner: mutable Arena&, items: dview[u32]) -> darray[u32]:\n    can Abort.Panic, Memory.Allocate:\n        in owner:\n            return rewrite items as sequence[u32]:\n                item when item != 0u32:\n                    emit item\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	canStmt, ok := decl.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected can stmt, got %T", decl.Body[0])
	}
	inStmt, ok := canStmt.Body[0].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in stmt, got %T", canStmt.Body[0])
	}
	ret, ok := inStmt.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", inStmt.Body[0])
	}
	rewriteExpr, ok := ret.Value.(*ast.FoldExpr)
	if !ok {
		t.Fatalf("expected fold-backed rewrite expr, got %T", ret.Value)
	}
	rootType, ok := rewriteExpr.Root.(*ast.GenericType)
	if !ok || rootType.Name != "sequence" || len(rootType.Args) != 1 {
		t.Fatalf("expected sequence[T] root, got %#v", rewriteExpr.Root)
	}
	if len(rewriteExpr.Arms) != 1 || rewriteExpr.Arms[0].TargetName != "item" {
		t.Fatalf("unexpected sequence rewrite arms: %#v", rewriteExpr.Arms)
	}
	stmt, ok := rewriteExpr.Arms[0].Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt arm body, got %T", rewriteExpr.Arms[0].Body[0])
	}
	emitExpr, ok := stmt.Expr.(*ast.EmitExpr)
	if !ok {
		t.Fatalf("expected emit expr, got %T", stmt.Expr)
	}
	if emitExpr.Value == nil || emitExpr.Nothing {
		t.Fatalf("expected emit value form, got %#v", emitExpr)
	}
	got := unparse.FormatExpr(rewriteExpr)
	if !strings.HasPrefix(got, "rewrite items as sequence[u32]:") {
		t.Fatalf("expected unparse to preserve sequence rewrite spelling, got:\n%s", got)
	}
	if !strings.Contains(got, "emit item") {
		t.Fatalf("expected unparse to preserve emit, got:\n%s", got)
	}
}

func TestParseTreeTargetSequenceRewriteExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Int(value: i64)\n        Name(name: u32)\n\ndef keep_int_values(owner: mutable Arena&, items: dview[Lua.Expr]) -> darray[i64]:\n    can Abort.Panic, Memory.Allocate:\n        in owner:\n            return rewrite items as sequence[i64]:\n                Lua.Expr.Int(expr) when expr.value > 0:\n                    emit expr.value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	canStmt, ok := decl.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected can stmt, got %T", decl.Body[0])
	}
	inStmt, ok := canStmt.Body[0].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in stmt, got %T", canStmt.Body[0])
	}
	ret, ok := inStmt.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", inStmt.Body[0])
	}
	rewriteExpr, ok := ret.Value.(*ast.FoldExpr)
	if !ok {
		t.Fatalf("expected fold-backed rewrite expr, got %T", ret.Value)
	}
	if len(rewriteExpr.Arms) != 1 {
		t.Fatalf("unexpected sequence rewrite arms: %#v", rewriteExpr.Arms)
	}
	arm := rewriteExpr.Arms[0]
	if arm.TargetName != "Lua.Expr.Int" || arm.BindName != "expr" {
		t.Fatalf("expected tree-target arm with explicit bind name, got %#v", arm)
	}
	got := unparse.FormatExpr(rewriteExpr)
	if !strings.Contains(got, "Lua.Expr.Int(expr) when (expr.value > 0)") {
		t.Fatalf("expected unparse to preserve tree-target sequence arm, got:\n%s", got)
	}
}

func TestParseSequenceRewriteEmitAllExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def concat(owner: mutable Arena&, left: dview[u32], right: dview[u32]) -> darray[u32]:\n    can Abort.Panic, Memory.Allocate:\n        in owner:\n            segments: darray[dview[u32]] = [left, right]\n            return rewrite segments as sequence[u32]:\n                segment:\n                    emit all segment\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	canStmt, ok := decl.Body[0].(*ast.CanStmt)
	if !ok {
		t.Fatalf("expected can stmt, got %T", decl.Body[0])
	}
	inStmt, ok := canStmt.Body[0].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in stmt, got %T", canStmt.Body[0])
	}
	ret, ok := inStmt.Body[1].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", inStmt.Body[1])
	}
	rewriteExpr, ok := ret.Value.(*ast.FoldExpr)
	if !ok {
		t.Fatalf("expected fold-backed rewrite expr, got %T", ret.Value)
	}
	stmt, ok := rewriteExpr.Arms[0].Body[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt arm body, got %T", rewriteExpr.Arms[0].Body[0])
	}
	emitExpr, ok := stmt.Expr.(*ast.EmitExpr)
	if !ok || !emitExpr.All || emitExpr.Value == nil {
		t.Fatalf("expected emit-all expr, got %#v", stmt.Expr)
	}
	if got := unparse.FormatExpr(rewriteExpr); !strings.Contains(got, "emit all segment") {
		t.Fatalf("expected unparse to preserve emit all, got:\n%s", got)
	}
}

func TestParseDeferStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> int:\n    defer block:\n        pass\n    defer function:\n        pass\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	blockDefer, ok := decl.Body[0].(*ast.DeferStmt)
	if !ok {
		t.Fatalf("expected first stmt to be defer, got %T", decl.Body[0])
	}
	if blockDefer.Mode != ast.DeferModeBlock {
		t.Fatalf("expected first defer mode block, got %v", blockDefer.Mode)
	}
	functionDefer, ok := decl.Body[1].(*ast.DeferStmt)
	if !ok {
		t.Fatalf("expected second stmt to be defer, got %T", decl.Body[1])
	}
	if functionDefer.Mode != ast.DeferModeFunction {
		t.Fatalf("expected second defer mode function, got %v", functionDefer.Mode)
	}
	if len(blockDefer.Body) != 1 || len(functionDefer.Body) != 1 {
		t.Fatalf("expected one stmt in each defer body, got %d and %d", len(blockDefer.Body), len(functionDefer.Body))
	}
}

func TestParseDeferRemainsContextualIdentifier(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> int:\n    defer_value: int = 1\n    defer(defer_value)\n    return defer_value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	if _, ok := decl.Body[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("expected first stmt to stay a var decl, got %T", decl.Body[0])
	}
	exprStmt, ok := decl.Body[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected second stmt to stay an expr stmt, got %T", decl.Body[1])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected defer(defer_value) to parse as a call, got %T", exprStmt.Expr)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "defer" {
		t.Fatalf("expected call callee defer, got %T %#v", call.Func, call.Func)
	}
}

func TestParseFunctionEnsuresClauses(t *testing.T) {
	file, errs := parseSourceFile(t, "def finish(team: Team&, node: heap HeapPairNode&, maybe: heap HeapPairNode&?, player: Player&) -> void can[Memory.Release] ensures team.player => Dead, node => !, maybe => &?, player => preserve:\n    pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Ensures) != 4 {
		t.Fatalf("expected four ensures clauses, got %d", len(decl.Ensures))
	}
	if decl.Ensures[0].Target.Root != "team" || len(decl.Ensures[0].Target.Fields) != 1 || decl.Ensures[0].Target.Fields[0] != "player" {
		t.Fatalf("expected first ensures target team.player, got %#v", decl.Ensures[0].Target)
	}
	if decl.Ensures[0].Kind != ast.EnsuresKindNamedState || len(decl.Ensures[0].StateCases) != 1 || decl.Ensures[0].StateCases[0] != "Dead" {
		t.Fatalf("expected first ensures clause to be named-state Dead, got %#v", decl.Ensures[0])
	}
	if decl.Ensures[1].Kind != ast.EnsuresKindRefState || decl.Ensures[1].RefState != ast.RefStateNull {
		t.Fatalf("expected second ensures clause to be refstate null, got %#v", decl.Ensures[1])
	}
	if decl.Ensures[2].Kind != ast.EnsuresKindRefState || decl.Ensures[2].RefState != ast.RefStateNullable {
		t.Fatalf("expected third ensures clause to be refstate nullable, got %#v", decl.Ensures[2])
	}
	if decl.Ensures[3].Kind != ast.EnsuresKindPreserve {
		t.Fatalf("expected fourth ensures clause to preserve state, got %#v", decl.Ensures[3])
	}
}

func TestParseExternEnsuresClause(t *testing.T) {
	file, errs := parseSourceFile(t, "extern sfree_heap_pair_node(node: heap HeapPairNode&) -> void can[Memory.Release] ensures node => !\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.ExternFuncDecl)
	if !ok {
		t.Fatalf("expected extern func decl, got %T", file.Decls[0])
	}
	if len(decl.Ensures) != 1 {
		t.Fatalf("expected one ensures clause, got %d", len(decl.Ensures))
	}
	if decl.Ensures[0].Target.Root != "node" {
		t.Fatalf("expected ensures target node, got %#v", decl.Ensures[0].Target)
	}
	if decl.Ensures[0].Kind != ast.EnsuresKindRefState || decl.Ensures[0].RefState != ast.RefStateNull {
		t.Fatalf("expected extern ensures clause to set node => !, got %#v", decl.Ensures[0])
	}
}

func TestParseConditionalEnsuresClauses(t *testing.T) {
	file, errs := parseSourceFile(t, "def finish(job: ParseJob&, player: Player&) -> bool ensures return true => job => Ready, return false => job => Failed, player => preserve:\n    return true\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if len(decl.Ensures) != 3 {
		t.Fatalf("expected three ensures clauses, got %d", len(decl.Ensures))
	}
	if decl.Ensures[0].Condition.Kind != ast.EnsuresConditionReturnBool || !decl.Ensures[0].Condition.ReturnBool {
		t.Fatalf("expected first ensures clause to be return-true conditioned, got %#v", decl.Ensures[0].Condition)
	}
	if decl.Ensures[1].Condition.Kind != ast.EnsuresConditionReturnBool || decl.Ensures[1].Condition.ReturnBool {
		t.Fatalf("expected second ensures clause to be return-false conditioned, got %#v", decl.Ensures[1].Condition)
	}
	if decl.Ensures[2].Condition.Kind != ast.EnsuresConditionAlways {
		t.Fatalf("expected third ensures clause to stay unconditional, got %#v", decl.Ensures[2].Condition)
	}
	if decl.Ensures[2].Kind != ast.EnsuresKindPreserve {
		t.Fatalf("expected unconditional clause to preserve state, got %#v", decl.Ensures[2])
	}
}

func TestParseConditionalEnsuresRequiresBoolLiteralBranch(t *testing.T) {
	_, errs := parseSourceFile(t, "def finish(job: ParseJob&) -> bool ensures return maybe => job => Ready:\n    return true\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for non-bool ensures return condition")
	}
	if !strings.Contains(errs[0], "ensures return condition expects true or false") {
		t.Fatalf("expected conditional ensures diagnostic, got %v", errs)
	}
}

func TestParseParallelForStatement(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Int(value: int)\n\ndef walk(frozen: Expr.Store[Frozen]) -> void:\n    pool workers(4):\n        parallel for node in frozen:\n            pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	poolStmt, ok := decl.Body[0].(*ast.PoolStmt)
	if !ok {
		t.Fatalf("expected pool stmt, got %T", decl.Body[0])
	}
	parallelStmt, ok := poolStmt.Body[0].(*ast.ParallelForStmt)
	if !ok {
		t.Fatalf("expected parallel-for stmt, got %T", poolStmt.Body[0])
	}
	if parallelStmt.Name != "node" {
		t.Fatalf("expected loop binder node, got %q", parallelStmt.Name)
	}
	if parallelStmt.IndexName != "" {
		t.Fatalf("expected no index binder, got %q", parallelStmt.IndexName)
	}
	source, ok := parallelStmt.Source.(*ast.Ident)
	if !ok || source.Name != "frozen" {
		t.Fatalf("expected loop source frozen, got %T %#v", parallelStmt.Source, parallelStmt.Source)
	}
}

func TestParseParallelForStatementWithIndexBinder(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Int(value: int)\n\ndef walk(frozen: Expr.Store[Frozen]) -> void:\n    pool workers(4):\n        parallel for tag at i in frozen.tags:\n            pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	poolStmt := decl.Body[0].(*ast.PoolStmt)
	parallelStmt, ok := poolStmt.Body[0].(*ast.ParallelForStmt)
	if !ok {
		t.Fatalf("expected parallel-for stmt, got %T", poolStmt.Body[0])
	}
	if parallelStmt.Name != "tag" {
		t.Fatalf("expected loop binder tag, got %q", parallelStmt.Name)
	}
	if parallelStmt.IndexName != "i" {
		t.Fatalf("expected index binder i, got %q", parallelStmt.IndexName)
	}
}

func TestParseParallelRemainsContextualIdentifier(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> int:\n    parallel: int = 1\n    for_value: int = parallel\n    parallel(for_value)\n    return for_value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	if _, ok := decl.Body[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("expected first stmt to stay a var decl, got %T", decl.Body[0])
	}
	exprStmt, ok := decl.Body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected third stmt to stay an expr stmt, got %T", decl.Body[2])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected parallel(for_value) to parse as a call, got %T", exprStmt.Expr)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "parallel" {
		t.Fatalf("expected call callee parallel, got %T %#v", call.Func, call.Func)
	}
}

func TestParseForStatementRangeForms(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk() -> void:\n    for i in 0..<10..2:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	forStmt, ok := decl.Body[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected for stmt, got %T", decl.Body[0])
	}
	if forStmt.Name != "i" {
		t.Fatalf("expected loop binder i, got %q", forStmt.Name)
	}
	if forStmt.Op != lexer.TOKEN_RANGE_LT {
		t.Fatalf("expected exclusive ascending range op, got %v", forStmt.Op)
	}
	if forStmt.Step == nil {
		t.Fatal("expected explicit range step")
	}
}

func TestParseIterableForStatementWithRefDestructuring(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Pair:\n    left: int\n    right: int\n\ndef walk(items: array[Pair, 2]) -> void:\n    for ref Pair(left, right) in items:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindRef {
		t.Fatalf("expected readonly ref bind mode, got %v", iterStmt.Mode)
	}
	pattern, ok := iterStmt.Pattern.(*ast.MoveBindStructPattern)
	if !ok {
		t.Fatalf("expected struct bind pattern, got %T", iterStmt.Pattern)
	}
	if pattern.TypeName != "Pair" {
		t.Fatalf("expected Pair pattern, got %q", pattern.TypeName)
	}
	if len(pattern.Args) != 2 || pattern.Args[0].Name != "left" || pattern.Args[1].Name != "right" {
		t.Fatalf("unexpected iterable pattern args: %#v", pattern.Args)
	}
	if _, ok := iterStmt.Source.(*ast.Ident); !ok {
		t.Fatalf("expected iterable source ident, got %T", iterStmt.Source)
	}
}

func TestParseIterableForStatementWithMutableRefBinder(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for mutable ref item in items:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindMutableRef {
		t.Fatalf("expected mutable ref bind mode, got %v", iterStmt.Mode)
	}
	pattern, ok := iterStmt.Pattern.(*ast.MoveBindNamePattern)
	if !ok || pattern.Name != "item" {
		t.Fatalf("expected mutable ref name pattern item, got %T %#v", iterStmt.Pattern, iterStmt.Pattern)
	}
}

func TestParseIterableForStatementWithEnumerateTuplePattern(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for index, value in enumerate(items):\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindValue {
		t.Fatalf("expected value bind mode, got %v", iterStmt.Mode)
	}
	pattern, ok := iterStmt.Pattern.(*ast.MoveBindTuplePattern)
	if !ok {
		t.Fatalf("expected tuple bind pattern, got %T", iterStmt.Pattern)
	}
	if len(pattern.Args) != 2 || pattern.Args[0].Name != "index" || pattern.Args[1].Name != "value" {
		t.Fatalf("unexpected tuple bind args: %#v", pattern.Args)
	}
	call, ok := iterStmt.Source.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected enumerate call source, got %T", iterStmt.Source)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "enumerate" {
		t.Fatalf("expected enumerate callee, got %T %#v", call.Func, call.Func)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for index, value in enumerate(items):") {
		t.Fatalf("expected formatter to preserve enumerate tuple loop syntax, got:\n%s", formatted)
	}
}

func TestParseReverseIterableForScopeAndCheckpointStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]\n\ndef walk(items: darray[int]) -> void:\n    for value in rev(items):\n        pass\n    scope pool_new(2):\n        pass\n    checkpoint mark = items:\n        pass\n    restore mark\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if !iterStmt.Reverse {
		t.Fatal("expected iterable loop to preserve reverse flag")
	}
	if _, ok := decl.Body[1].(*ast.ScopeStmt); !ok {
		t.Fatalf("expected scope statement, got %T", decl.Body[1])
	}
	checkpointStmt, ok := decl.Body[2].(*ast.CheckpointStmt)
	if !ok {
		t.Fatalf("expected checkpoint statement, got %T", decl.Body[2])
	}
	if checkpointStmt.Name != "mark" {
		t.Fatalf("expected checkpoint name mark, got %q", checkpointStmt.Name)
	}
	if _, ok := decl.Body[3].(*ast.RestoreCheckpointStmt); !ok {
		t.Fatalf("expected restore checkpoint statement, got %T", decl.Body[3])
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for value in rev(items):") || !strings.Contains(formatted, "scope pool_new(2):") || !strings.Contains(formatted, "checkpoint mark = items:") || !strings.Contains(formatted, "restore mark") {
		t.Fatalf("expected formatter to preserve reverse/scope/checkpoint syntax, got:\n%s", formatted)
	}
}

func TestParseGroupedCheckpointStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int], others: darray[int], more: darray[int]) -> void:\n    checkpoint items, others, more:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	checkpointStmt, ok := decl.Body[0].(*ast.GroupedCheckpointStmt)
	if !ok {
		t.Fatalf("expected grouped checkpoint statement, got %T", decl.Body[0])
	}
	if len(checkpointStmt.Targets) != 3 {
		t.Fatalf("expected 3 grouped checkpoint targets, got %d", len(checkpointStmt.Targets))
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "checkpoint items, others, more:") {
		t.Fatalf("expected formatter to preserve grouped checkpoint syntax, got:\n%s", formatted)
	}
}

func TestParseRejectsSingleTargetAnonymousCheckpoint(t *testing.T) {
	_, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    checkpoint items:\n        pass\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for single-target anonymous checkpoint")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "grouped checkpoint requires at least 2 targets") {
		t.Fatalf("expected grouped checkpoint arity diagnostic, got: %v", errs)
	}
}

func TestParseRevLoopVariableNameWithoutReverseCompatCollision(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for rev in items:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Reverse {
		t.Fatal("expected `rev` to be parsed as the loop variable name, not the reverse compat prefix")
	}
	namePattern, ok := iterStmt.Pattern.(*ast.MoveBindNamePattern)
	if !ok {
		t.Fatalf("expected simple loop pattern, got %T", iterStmt.Pattern)
	}
	if namePattern.Name != "rev" {
		t.Fatalf("expected loop variable name `rev`, got %q", namePattern.Name)
	}
}

func TestParseRejectsLegacyReverseIterableLoopSyntax(t *testing.T) {
	_, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for rev value in items:\n        pass\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "legacy reverse iterable loop syntax `for rev ... in ...:` is no longer supported") {
		t.Fatalf("expected legacy reverse iterable loop parser diagnostic, got: %v", errs)
	}
}

func TestParseStoreDecl(t *testing.T) {
	file, errs := parseSourceFile(t, "store PendingGotoStore:\n    name_key: u32\n    depth: u32\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	storeDecl, ok := file.Decls[0].(*ast.StoreDecl)
	if !ok {
		t.Fatalf("expected store decl, got %T", file.Decls[0])
	}
	if storeDecl.Name != "PendingGotoStore" {
		t.Fatalf("expected store name PendingGotoStore, got %q", storeDecl.Name)
	}
	if len(storeDecl.Fields) != 2 || storeDecl.Fields[0].Name != "name_key" || storeDecl.Fields[1].Name != "depth" {
		t.Fatalf("unexpected store fields: %#v", storeDecl.Fields)
	}
	if formatted := unparse.FormatDecl(storeDecl); !strings.Contains(formatted, "store PendingGotoStore:") {
		t.Fatalf("expected formatter to preserve store syntax, got:\n%s", formatted)
	}
}

func TestParseGetOrInsertBlockSugar(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(values: dict[dstr[key_shape], i64], key: dstr[key_shape]):\n    slot = values.get_or_insert(key):\n        42\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected rewritten call value, got %T", decl.Value)
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok || callee.Field != "get_or_insert" {
		t.Fatalf("expected get_or_insert field call, got %#v", call.Func)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected rewritten get_or_insert call to have key and default args, got %d", len(call.Args))
	}
	if _, ok := call.Args[1].(*ast.IntLit); !ok {
		t.Fatalf("expected block expression to become second call arg, got %T", call.Args[1])
	}
}

func TestParseGetOrInsertBlockSugarWithSetupStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(values: dict[dstr[key_shape], i64], key: dstr[key_shape]):\n    slot = values.get_or_insert(key):\n        base = 40\n        base + 2\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok || len(call.Args) != 2 {
		t.Fatalf("expected rewritten get_or_insert call, got %#v", decl.Value)
	}
	block, ok := call.Args[1].(*ast.ExprBlock)
	if !ok {
		t.Fatalf("expected second arg to be expr block, got %T", call.Args[1])
	}
	if len(block.Stmts) != 1 {
		t.Fatalf("expected one setup stmt in expr block, got %d", len(block.Stmts))
	}
}

func TestParseDictEntryGetOrInsertBlockSugar(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(values: dict[dstr[key_shape], i64], key: dstr[key_shape]):\n    slot = values.entry(key).get_or_insert():\n        42\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected rewritten call value, got %T", decl.Value)
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok || callee.Field != "get_or_insert" {
		t.Fatalf("expected get_or_insert field call, got %#v", call.Func)
	}
	if len(call.Args) != 1 {
		t.Fatalf("expected rewritten entry get_or_insert call to have one default arg, got %d", len(call.Args))
	}
	entryCall, ok := callee.Object.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected get_or_insert receiver to stay an entry call, got %T", callee.Object)
	}
	entryField, ok := entryCall.Func.(*ast.FieldExpr)
	if !ok || entryField.Field != "entry" {
		t.Fatalf("expected entry receiver field call, got %#v", entryCall.Func)
	}
	if len(entryCall.Args) != 1 {
		t.Fatalf("expected entry call to keep one key arg, got %d", len(entryCall.Args))
	}
	if _, ok := call.Args[0].(*ast.IntLit); !ok {
		t.Fatalf("expected block expression to become get_or_insert default arg, got %T", call.Args[0])
	}
}

func TestParseGetOrInsertBlockSugarWithGenericDictKey(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(values: dict[u32, i64], key: u32):\n    slot = values.get_or_insert(key):\n        42\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected rewritten call value, got %T", decl.Value)
	}
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok || callee.Field != "get_or_insert" {
		t.Fatalf("expected get_or_insert field call, got %#v", call.Func)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected rewritten get_or_insert call to have key and default args, got %d", len(call.Args))
	}
	if _, ok := call.Args[0].(*ast.Ident); !ok {
		t.Fatalf("expected first arg to stay the key ident, got %T", call.Args[0])
	}
	if _, ok := call.Args[1].(*ast.IntLit); !ok {
		t.Fatalf("expected block expression to become second call arg, got %T", call.Args[1])
	}
}

func TestParseDoExprBlock(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> i64:\n    value = do:\n        base = 40\n        base + 2\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	block, ok := decl.Value.(*ast.ExprBlock)
	if !ok {
		t.Fatalf("expected expr block value, got %T", decl.Value)
	}
	if len(block.Stmts) != 1 {
		t.Fatalf("expected one setup stmt in expr block, got %d", len(block.Stmts))
	}
	formatted := unparse.FormatStmt(fn.Body[0])
	if !strings.Contains(formatted, "do:") {
		t.Fatalf("expected formatter to preserve do expression block syntax, got:\n%s", formatted)
	}
}

func TestParseDoExprBlockFinalMatchStatementLowersToMatchExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Op of i32:\n    ADD = 1\n    SUB = 2\n\ndef keep(op: Op) -> i64:\n    return do:\n        match op:\n            Op.ADD:\n                10\n            Op.SUB:\n                20\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[1])
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", fn.Body[0])
	}
	block, ok := ret.Value.(*ast.ExprBlock)
	if !ok {
		t.Fatalf("expected do expr block, got %T", ret.Value)
	}
	if len(block.Stmts) != 0 {
		t.Fatalf("expected final match statement to be lowered into expr block value, got %d setup stmt(s)", len(block.Stmts))
	}
	matchExpr, ok := block.Value.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("expected expr block value to be lowered match expr, got %T", block.Value)
	}
	if len(matchExpr.Arms) != 2 {
		t.Fatalf("expected two match arms, got %d", len(matchExpr.Arms))
	}
}

func TestParseRejectsDirectMatchExprSyntax(t *testing.T) {
	_, errs := parseSourceFile(t, "const enum Op of i32:\n    ADD = 1\n\ndef keep(op: Op) -> i64:\n    return match op:\n        Op.ADD:\n            10\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for direct match expression syntax, got none")
	}
}

func TestFormatCallWithDoExprBlockArg(t *testing.T) {
	stmt := &ast.VarDeclStmt{
		Name: "value",
		Value: &ast.CallExpr{
			Func: &ast.Ident{Name: "consume"},
			Args: []ast.Expr{
				&ast.ExprBlock{
					Stmts: []ast.Stmt{
						&ast.VarDeclStmt{
							Name:  "base",
							Value: &ast.IntLit{Value: "40"},
						},
					},
					Value: &ast.BinaryExpr{
						Op:    lexer.TOKEN_PLUS,
						Left:  &ast.Ident{Name: "base"},
						Right: &ast.IntLit{Value: "2"},
					},
				},
			},
		},
	}
	formatted := unparse.FormatStmt(stmt)
	if !strings.Contains(formatted, "consume(\n") {
		t.Fatalf("expected multiline call formatting, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "do:\n") {
		t.Fatalf("expected nested do expression to be preserved, got:\n%s", formatted)
	}
}

func TestParseCallWithDoExprBlockArg(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> i64:\n    value = consume(do:\n        base = 40\n        base + 2\n    )\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected call expr with one arg, got %#v", decl.Value)
	}
	if _, ok := call.Args[0].(*ast.ExprBlock); !ok {
		t.Fatalf("expected do block arg, got %T", call.Args[0])
	}
}

func TestParseCallNamedArgDoStillWorks(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> i64:\n    value = consume(do: 3)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected call expr with one arg, got %#v", decl.Value)
	}
	if call.ArgName(0) != "do" {
		t.Fatalf("expected named arg 'do', got %q", call.ArgName(0))
	}
	if _, ok := call.Args[0].(*ast.IntLit); !ok {
		t.Fatalf("expected int literal named arg value, got %T", call.Args[0])
	}
}

func TestParseCallNamedArgWithDoExprBlock(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> i64:\n    value = consume(x: do:\n        base = 40\n        base + 2\n    )\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected call expr with one arg, got %#v", decl.Value)
	}
	if call.ArgName(0) != "x" {
		t.Fatalf("expected named arg 'x', got %q", call.ArgName(0))
	}
	if _, ok := call.Args[0].(*ast.ExprBlock); !ok {
		t.Fatalf("expected do block named arg value, got %T", call.Args[0])
	}
}

func TestParseListWithDoExprBlockElem(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> void:\n    values: i64[2] = [do:\n        base = 40\n        base + 2\n    , 7]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || len(fn.Body) != 1 {
		t.Fatalf("expected single function body stmt, got %#v", file.Decls[0])
	}
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	list, ok := decl.Value.(*ast.ListLitExpr)
	if !ok || len(list.Elems) != 2 {
		t.Fatalf("expected list literal with two elems, got %#v", decl.Value)
	}
	if _, ok := list.Elems[0].(*ast.ExprBlock); !ok {
		t.Fatalf("expected first list elem to be do block, got %T", list.Elems[0])
	}
}

func TestParseChildrenToOverrideExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(stmt)\n    node Stmt:\n        BreakStmt\n\ndef keep(stmt: Lua.Stmt) -> Lua.Node:\n    return children(stmt to Lua.Node).node\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	field, ok := ret.Value.(*ast.FieldExpr)
	if !ok {
		t.Fatalf("expected field expr, got %T", ret.Value)
	}
	call, ok := field.Object.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected children call, got %T %#v", field.Object, field.Object)
	}
	cast, ok := call.Args[0].(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected to-override arg, got %T", call.Args[0])
	}
	if cast.Origin != ast.CastExprOriginToSyntax {
		t.Fatalf("expected to-syntax cast origin, got %v", cast.Origin)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "children(stmt as Lua.Node)") {
		t.Fatalf("expected unparse to canonicalize to-override syntax to as-cast syntax, got:\n%s", formatted)
	}
}

func TestParsePostfixShorthandCastFormatsAsCast(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: i64) -> u32:\n    return value.u32()\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cast, ok := ret.Value.(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected cast expr, got %T", ret.Value)
	}
	if cast.Origin != ast.CastExprOriginPostfixShorthand {
		t.Fatalf("expected postfix shorthand cast origin, got %v", cast.Origin)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "value as u32") {
		t.Fatalf("expected unparse to canonicalize postfix cast shorthand to as-cast syntax, got:\n%s", formatted)
	}
}

func TestParsePostfixCastWithAnyRefTarget(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> u8&:\n    return \"hello\".cast[u8&]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cast, ok := ret.Value.(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected cast expr, got %T", ret.Value)
	}
	if cast.Origin != ast.CastExprOriginExplicitCast {
		t.Fatalf("expected explicit cast origin, got %v", cast.Origin)
	}
	target, ok := cast.Target.(*ast.RefType)
	if !ok {
		t.Fatalf("expected ref target type, got %T", cast.Target)
	}
	if target.Storage != ast.RefStorageAny || target.State != ast.RefStateNonNull {
		t.Fatalf("unexpected cast target %#v", target)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, ".cast[u8&]") {
		t.Fatalf("expected unparse to preserve postfix cast target, got:\n%s", formatted)
	}
}

func TestParsePostfixCastWithMutableAnyRefTarget(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Cursor:\n    pos: i64\n\ndef keep(cursor: Cursor&) -> mutable Cursor&:\n    return cursor.cast[mutable Cursor&]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cast, ok := ret.Value.(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected cast expr, got %T", ret.Value)
	}
	if _, ok := cast.Target.(*ast.MutableType); !ok {
		t.Fatalf("expected mutable cast target, got %T", cast.Target)
	}
}

func TestParseAsCastExprPreservesSyntax(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Arena:\n    value: i64\n\ndef keep(owner: Arena) -> mutable Arena&:\n    return &owner as mutable Arena&\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	cast, ok := ret.Value.(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected cast expr, got %T", ret.Value)
	}
	if cast.Origin != ast.CastExprOriginAsSyntax {
		t.Fatalf("expected as-syntax cast origin, got %v", cast.Origin)
	}
	if _, ok := cast.Operand.(*ast.AddrOfExpr); !ok {
		t.Fatalf("expected address-of operand, got %T", cast.Operand)
	}
	if _, ok := cast.Target.(*ast.MutableType); !ok {
		t.Fatalf("expected mutable cast target, got %T", cast.Target)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "&owner as mutable Arena&") {
		t.Fatalf("expected unparse to preserve as cast syntax, got:\n%s", formatted)
	}
}

func TestParseAsRefAssignmentRemainsStatementSyntax(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Arena:\n    end: Arena!\n\ndef keep(a: mutable Arena&) -> void:\n    a.end as & <- zeroed\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	assign, ok := decl.Body[0].(*ast.AsRefAssignStmt)
	if !ok {
		t.Fatalf("expected as-ref assignment stmt, got %T", decl.Body[0])
	}
	if assign.AsKind != "&" {
		t.Fatalf("expected as-ref assignment kind &, got %q", assign.AsKind)
	}
	if got := unparse.FormatStmt(assign); !strings.Contains(got, "a.end as & <- zeroed") {
		t.Fatalf("expected unparse to preserve as-ref assignment, got:\n%s", got)
	}
}

func TestParseMoveAsStructPatternRemainsStatementSyntax(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Worker:\n    value: i64\n\ndef keep(worker: Worker) -> i64:\n    move worker as Worker(value)\n    return value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	stmt, ok := decl.Body[0].(*ast.MoveBindStmt)
	if !ok {
		t.Fatalf("expected move bind stmt, got %T", decl.Body[0])
	}
	pattern, ok := stmt.Pattern.(*ast.MoveBindStructPattern)
	if !ok {
		t.Fatalf("expected struct move bind pattern, got %T", stmt.Pattern)
	}
	if pattern.TypeName != "Worker" || len(pattern.Args) != 1 || pattern.Args[0].Name != "value" {
		t.Fatalf("unexpected move bind pattern %#v", pattern)
	}
}

func TestParseForRemainsContextualIdentifier(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep() -> int:\n    for_value: int = 1\n    for(for_value)\n    return for_value\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	exprStmt, ok := decl.Body[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected expr stmt, got %T", decl.Body[1])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call, got %T", exprStmt.Expr)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "for" {
		t.Fatalf("expected call callee for, got %T %#v", call.Func, call.Func)
	}
}
