package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/lexer"
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
	file, errs := parseSourceFile(t, "struct Holder[?]:\n    value: any i32&\n")
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
	file, errs := parseSourceFile(t, "struct Holder[?, ?]:\n    value: any i32&\n")
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
	file, errs := parseSourceFile(t, "struct Holder[refstate s]:\n    value: any i32&&[s]\n")
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

func TestParsePackedEnumAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@packed_abi(dense_fixed)\npacked enum Expr:\n    Lit(value: int)\n")
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
	if decl.Annotations[0].Name != "packed_abi" {
		t.Fatalf("expected packed_abi annotation, got %q", decl.Annotations[0].Name)
	}
	if len(decl.Annotations[0].Args) != 1 || decl.Annotations[0].Args[0] != "dense_fixed" {
		t.Fatalf("expected packed_abi(dense_fixed), got %#v", decl.Annotations[0].Args)
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

func TestParsePackedEnumPrefixAnnotation(t *testing.T) {
	file, errs := parseSourceFile(t, "@packed_abi(dense_fixed)\n@packed_prefix(common_only)\npacked enum Expr:\n    common:\n        span: int\n    Lit(value: int)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.EnumDecl)
	if !ok {
		t.Fatalf("expected enum decl, got %T", file.Decls[0])
	}
	if len(decl.Annotations) != 2 {
		t.Fatalf("expected two enum annotations, got %d", len(decl.Annotations))
	}
	if decl.Annotations[1].Name != "packed_prefix" {
		t.Fatalf("expected packed_prefix annotation, got %q", decl.Annotations[1].Name)
	}
	if len(decl.Annotations[1].Args) != 1 || decl.Annotations[1].Args[0] != "common_only" {
		t.Fatalf("expected packed_prefix(common_only), got %#v", decl.Annotations[1].Args)
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
	file, errs := parseSourceFile(t, "@guard_nonnull(box)\ndef has_box(box: any Box&?) -> bool:\n    return box != null\n")
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
	file, errs := parseSourceFile(t, "def finish(team: any Team&, node: heap HeapPairNode&, maybe: heap HeapPairNode&?, player: any Player&) -> void can[Memory.Release] ensures team.player => Dead, node => !, maybe => &?, player => preserve:\n    pass\n")
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
	file, errs := parseSourceFile(t, "def finish(job: any ParseJob&, player: any Player&) -> bool ensures return true => job => Ready, return false => job => Failed, player => preserve:\n    return true\n")
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
	_, errs := parseSourceFile(t, "def finish(job: any ParseJob&) -> bool ensures return maybe => job => Ready:\n    return true\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for non-bool ensures return condition")
	}
	if !strings.Contains(errs[0], "ensures return condition expects true or false") {
		t.Fatalf("expected conditional ensures diagnostic, got %v", errs)
	}
}

func TestParseParallelForStatement(t *testing.T) {
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Int(value: int)\n\ndef walk(frozen: Expr.Store[Frozen]) -> void:\n    pool workers(4u):\n        parallel for node in frozen:\n            pass\n")
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
	file, errs := parseSourceFile(t, "packed enum Expr:\n    Int(value: int)\n\ndef walk(frozen: Expr.Store[Frozen]) -> void:\n    pool workers(4u):\n        parallel for tag at i in frozen.tags:\n            pass\n")
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
