package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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
func TestParseStructDeclWithValueGenericParam(t *testing.T) {
	file, errs := parseSourceFile(t, "struct InlineVec[T, N: usize]:\n    items: T[N]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected struct decl, got %T", file.Decls[0])
	}
	if len(decl.GenericParams) != 2 || decl.GenericParams[0].Kind != ast.GenericParamType || decl.GenericParams[1].Kind != ast.GenericParamValue {
		t.Fatalf("expected type param followed by value param, got %#v", decl.GenericParams)
	}
	if decl.GenericParams[1].Name != "N" || decl.GenericParams[1].InterfaceBound != "usize" {
		t.Fatalf("expected value param N: usize, got %#v", decl.GenericParams[1])
	}
	arrayType, ok := decl.Fields[0].Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("expected T[N] to parse as array type, got %T", decl.Fields[0].Type)
	}
	if _, ok := arrayType.Size.(*ast.Ident); !ok {
		t.Fatalf("expected array size to remain value param identifier, got %T", arrayType.Size)
	}
}

func TestParseNamedStructArrayWithConstantSize(t *testing.T) {
	file, errs := parseSourceFile(t, "const MAX_ITEMS: usize = 4\nstruct Item:\n    value: u32\nstruct Holder:\n    items: Item[MAX_ITEMS]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[2].(*ast.StructDecl)
	if !ok {
		t.Fatalf("expected holder struct decl, got %T", file.Decls[2])
	}
	arrayType, ok := decl.Fields[0].Type.(*ast.ArrayType)
	if !ok {
		t.Fatalf("expected Item[MAX_ITEMS] to parse as array type, got %T", decl.Fields[0].Type)
	}
	named, ok := arrayType.Elem.(*ast.NamedType)
	if !ok || named.Name != "Item" {
		t.Fatalf("expected array element type Item, got %#v", arrayType.Elem)
	}
	size, ok := arrayType.Size.(*ast.Ident)
	if !ok || size.Name != "MAX_ITEMS" {
		t.Fatalf("expected array size MAX_ITEMS, got %#v", arrayType.Size)
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
}
func TestParsePackedIfPatternWithViewAliasBinding(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n\ndef fold(node: Expr, store: Expr.Store[Local]) -> int:\n    if node in store is Expr.Lit(value: value):\n        lit: packedview[Expr.Lit] = node\n        return value + lit.span\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected lowered match stmt, got %T", decl.Body[0])
	}
	if len(matchStmt.Arms) != 2 {
		t.Fatalf("expected match arm plus wildcard, got %d", len(matchStmt.Arms))
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected Expr.Lit pattern, got %T", matchStmt.Arms[0].Pattern)
	}
	if pattern.EnumName != "Expr" || pattern.Variant != "Lit" {
		t.Fatalf("expected Expr.Lit pattern, got %#v", pattern)
	}
	if len(pattern.Args) != 1 {
		t.Fatalf("expected one binding arg, got %d", len(pattern.Args))
	}
	bindPattern, ok := pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || bindPattern.Name != "value" {
		t.Fatalf("expected value payload binding, got %T %#v", pattern.Args[0].Pattern, pattern.Args[0].Pattern)
	}
	if _, ok := matchStmt.Arms[0].Body[0].(*ast.VarDeclStmt); !ok {
		t.Fatalf("expected packedview alias binding in match body, got %T", matchStmt.Arms[0].Body[0])
	}
}
func TestParsePackedIfNestedPayloadPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Int(value: int)\n    Add(left: Expr, right: Expr)\n\ndef left_value(node: Expr, store: Expr.Store[Local]) -> int:\n    if node in store is Expr.Add(Expr.Int(value), rhs):\n        return value\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[1])
	}
	matchStmt, ok := decl.Body[0].(*ast.MatchStmt)
	if !ok {
		t.Fatalf("expected lowered match stmt, got %T", decl.Body[0])
	}
	pattern, ok := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if !ok || len(pattern.Args) != 2 {
		t.Fatalf("expected two payload patterns, got %#v", matchStmt.Arms[0].Pattern)
	}
	leftPattern, ok := pattern.Args[0].Pattern.(*ast.MatchVariantPattern)
	if !ok || leftPattern.EnumName != "Expr" || leftPattern.Variant != "Int" || len(leftPattern.Args) != 1 {
		t.Fatalf("expected nested Expr.Int(value) pattern, got %#v", pattern.Args[0].Pattern)
	}
	leftBind, ok := leftPattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || leftBind.Name != "value" {
		t.Fatalf("expected nested bind pattern value, got %T %#v", leftPattern.Args[0].Pattern, leftPattern.Args[0].Pattern)
	}
	rightBind, ok := pattern.Args[1].Pattern.(*ast.MatchBindPattern)
	if !ok || rightBind.Name != "rhs" {
		t.Fatalf("expected rhs bind pattern, got %T %#v", pattern.Args[1].Pattern, pattern.Args[1].Pattern)
	}
}

func TestParseNestedOrPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Token:\n    Ident\n    Keyword\n    Other\n\nenum Expr:\n    Leaf(kind: Token)\n\ndef score(expr: Expr) -> int:\n    match expr:\n        Expr.Leaf(Token.Ident | Token.Keyword):\n            return 1\n        _:\n            return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	matchStmt := decl.Body[0].(*ast.MatchStmt)
	pattern := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if len(pattern.Args) != 1 {
		t.Fatalf("expected one payload arg, got %#v", pattern.Args)
	}
	orPattern, ok := pattern.Args[0].Pattern.(*ast.MatchOrPattern)
	if !ok || len(orPattern.Options) != 2 {
		t.Fatalf("expected two-option nested or pattern, got %T %#v", pattern.Args[0].Pattern, pattern.Args[0].Pattern)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "Expr.Leaf(Token.Ident | Token.Keyword):") {
		t.Fatalf("expected nested or pattern to format, got:\n%s", formatted)
	}
}
func TestParseNestedOrPatternAllowsBindings(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Token:\n    Ident(value: int)\n    Keyword(value: int)\n\nenum Expr:\n    Leaf(kind: Token)\n\ndef score(expr: Expr) -> int:\n    match expr:\n        Expr.Leaf(Token.Ident(value) | Token.Keyword(value)):\n            return value\n        _:\n            return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	matchStmt := decl.Body[0].(*ast.MatchStmt)
	pattern := matchStmt.Arms[0].Pattern.(*ast.MatchVariantPattern)
	orPattern, ok := pattern.Args[0].Pattern.(*ast.MatchOrPattern)
	if !ok || len(orPattern.Options) != 2 {
		t.Fatalf("expected binding nested or pattern, got %T %#v", pattern.Args[0].Pattern, pattern.Args[0].Pattern)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "Token.Ident(value) | Token.Keyword(value)") {
		t.Fatalf("expected binding nested or pattern to format, got:\n%s", formatted)
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
func TestParseIsConditionWithBracketedAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Tok of i32:\n    LT = 1\n    LTEQ = 2\n    GT = 3\n\ndef is_rel(kind: Tok) -> bool:\n    return kind is [.LT | .LTEQ | .GT]\n")
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
		t.Fatalf("expected bracketed is-pattern RHS, got %T", cond.Right)
	}
	if !alts.Brackets {
		t.Fatalf("expected bracketed is-pattern form, got %#v", alts)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "kind is [.LT | .LTEQ | .GT]") {
		t.Fatalf("expected bracketed is-pattern to unparse, got:\n%s", formatted)
	}
}
func TestParseIsConditionWithQualifiedAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Bool(value: bool)\n    Char(value: i64)\n\ndef is_scalar(value: Expr) -> bool:\n    return value is Expr.Int | Expr.Bool | Expr.Char\n")
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
	if !ok || len(alts.Targets) != 3 {
		t.Fatalf("expected three qualified alternatives, got %T %#v", cond.Right, cond.Right)
	}
}
func TestParseIsConditionWithBracketedQualifiedAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Bool(value: bool)\n    Char(value: i64)\n\ndef is_scalar(value: Expr) -> bool:\n    return value is [Expr.Int | Expr.Bool | Expr.Char]\n")
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
	if !ok || len(alts.Targets) != 3 {
		t.Fatalf("expected three bracketed qualified alternatives, got %T %#v", cond.Right, cond.Right)
	}
	if !alts.Brackets {
		t.Fatalf("expected bracketed qualified alternatives, got %#v", alts)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "value is [Expr.Int | Expr.Bool | Expr.Char]") {
		t.Fatalf("expected bracketed qualified is-pattern to unparse, got:\n%s", formatted)
	}
}
func TestParseIsConditionWithGroupedMultilineAlternativeTargets(t *testing.T) {
	file, errs := parseSourceFile(t, `enum Decl:
    ProcedureDecl
    ProcedureQualifiedDecl
    ProcedureGenericDecl
    FunctionDecl

def has_body(decl: Decl) -> bool:
    return decl is (
        Decl.ProcedureDecl
        | Decl.ProcedureQualifiedDecl
        | Decl.ProcedureGenericDecl
        | Decl.FunctionDecl
    )
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"return decl is (",
		"    Decl.ProcedureDecl",
		"    | Decl.ProcedureQualifiedDecl",
		"    | Decl.ProcedureGenericDecl",
		"    | Decl.FunctionDecl",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted grouped is alternatives to contain %q, got:\n%s", want, formatted)
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
