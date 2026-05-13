package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
)

func TestParseQueryExprFamily(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(items: darray[i64]) -> bool:\n    return any item in items where item > 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.Kind != ast.QueryExprAny || query.Name != "item" {
		t.Fatalf("unexpected query shape: %#v", query)
	}
	if _, ok := query.Filter.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected binary filter, got %T", query.Filter)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return any item in items where (item > 0)") {
		t.Fatalf("expected formatted query expression, got:\n%s", formatted)
	}
}

func TestParseFirstProjectionQueryExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Entry:\n    name: i64\n    enabled: bool\n\ndef keep(items: darray[Entry]) -> i64?:\n    return item.name for first item in items where item.enabled\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.Kind != ast.QueryExprFirst || query.Name != "item" || query.Projection == nil {
		t.Fatalf("unexpected projection query shape: %#v", query)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return item.name for first item in items where item.enabled") {
		t.Fatalf("expected formatted first projection query expression, got:\n%s", formatted)
	}
}

func TestParseEachProjectionQueryExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Entry:\n    name: i64\n    enabled: bool\n\ndef keep(items: darray[Entry]) -> darray[i64]:\n    return item.name for each item in items\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret, ok := decl.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", decl.Body[0])
	}
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.Kind != ast.QueryExprEach || query.Name != "item" || query.Projection == nil {
		t.Fatalf("unexpected projection query shape: %#v", query)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return item.name for each item in items") {
		t.Fatalf("expected formatted each projection query expression, got:\n%s", formatted)
	}
}

func TestParseProjectionQueryPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Missing\n\ndef keep(items: darray[Expr]) -> darray[i64]:\n    return value for each item in items where Expr.Int(value)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.PatternFilter == nil || query.Filter != nil {
		t.Fatalf("expected pattern filter query, got filter=%T pattern=%T", query.Filter, query.PatternFilter)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return value for each item in items where Expr.Int(value)") {
		t.Fatalf("expected formatted pattern-filter query expression, got:\n%s", formatted)
	}
}

func TestParseQuerySubjectIsPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Missing\n\ndef keep(items: darray[Expr]) -> bool:\n    return any item in items where item is Expr.Int(value): value > 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.PatternFilter == nil || query.Filter == nil {
		t.Fatalf("expected subject-is guarded pattern filter query, got filter=%T pattern=%T", query.Filter, query.PatternFilter)
	}
	pattern, ok := query.PatternFilter.(*ast.MatchVariantPattern)
	if !ok || pattern.EnumName != "Expr" || pattern.Variant != "Int" {
		t.Fatalf("unexpected pattern filter: %T %#v", query.PatternFilter, query.PatternFilter)
	}
}

func TestParseQueryTupleSubjectIsPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Missing\n\ndef keep(items: darray[Expr]) -> bool:\n    return any index, item in items.enumerate() where item is Expr.Int(value): value > index\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	pattern, ok := query.Pattern.(*ast.MoveBindTuplePattern)
	if !ok || len(pattern.Args) != 2 || pattern.Args[0].Name != "index" || pattern.Args[1].Name != "item" {
		t.Fatalf("expected tuple query binder, got %T %#v", query.Pattern, query.Pattern)
	}
	if query.PatternFilterSubject != "item" {
		t.Fatalf("expected pattern filter subject item, got %q", query.PatternFilterSubject)
	}
	if query.PatternFilter == nil || query.Filter == nil {
		t.Fatalf("expected guarded pattern filter, got filter=%T pattern=%T", query.Filter, query.PatternFilter)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return any index, item in items.enumerate() where item is Expr.Int(value): (value > index)") {
		t.Fatalf("expected formatter to preserve tuple subject query, got:\n%s", formatted)
	}
}

func TestParseWhereViewSubjectIsPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Missing\n\ndef keep(items: darray[Expr]) -> bool:\n    return any((items where item is Expr.Int(value): value > 0))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected aggregate call expr, got %T", ret.Value)
	}
	paren, ok := call.Args[0].(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected parenthesized where view arg, got %T", call.Args[0])
	}
	whereCall, ok := paren.Inner.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected where view call source, got %T", paren.Inner)
	}
	if fn, ok := whereCall.Func.(*ast.Ident); !ok || fn.Name != "where" {
		t.Fatalf("expected where call, got %T %#v", whereCall.Func, whereCall.Func)
	}
}

func TestParseProjectionQueryPatternFilterGuard(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    Missing\n\ndef keep(items: darray[Expr]) -> darray[i64]:\n    return value for each item in items where Expr.Int(value): value > 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.PatternFilter == nil || query.Filter == nil {
		t.Fatalf("expected guarded pattern filter query, got filter=%T pattern=%T", query.Filter, query.PatternFilter)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return value for each item in items where Expr.Int(value): (value > 0)") {
		t.Fatalf("expected formatted guarded pattern filter query, got:\n%s", formatted)
	}
}

func TestParseEachProjectionQueryWithExplicitOwner(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Entry:\n    name: i64\n\ndef keep(owner: Arena, items: darray[Entry]) -> darray[i64]:\n    alloc: mutable Arena& = (&owner).cast[mutable Arena&]\n    return item.name for each item in items with alloc\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[1].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok {
		t.Fatalf("expected query expr, got %T", ret.Value)
	}
	if query.Owner == nil {
		t.Fatal("expected explicit query owner")
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return item.name for each item in items with alloc") {
		t.Fatalf("expected formatted owner query expression, got:\n%s", formatted)
	}
}

func TestParseExpressionWhereViewWithExplicitBinder(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(items: darray[i64]) -> bool:\n    return all((items where item: item > 0))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected all call, got %T", ret.Value)
	}
	paren, ok := call.Args[0].(*ast.ParenExpr)
	if !ok {
		t.Fatalf("expected parenthesized where view arg, got %T", call.Args[0])
	}
	whereCall, ok := paren.Inner.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected where call, got %T", paren.Inner)
	}
	callee, ok := whereCall.Func.(*ast.Ident)
	if !ok || callee.Name != "where" {
		t.Fatalf("expected where callee, got %T %#v", whereCall.Func, whereCall.Func)
	}
	if len(whereCall.Args) != 2 {
		t.Fatalf("expected where source and predicate args, got %d", len(whereCall.Args))
	}
	lambda, ok := whereCall.Args[1].(*ast.LambdaExpr)
	if !ok || len(lambda.Params) != 1 || lambda.Params[0].Name != "item" {
		t.Fatalf("expected shorthand item lambda, got %T %#v", whereCall.Args[1], whereCall.Args[1])
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return all((items where item: (item > 0)))") {
		t.Fatalf("expected formatted expression where view, got:\n%s", formatted)
	}
}

func TestParseExpressionWhereViewWithEnumerateBinders(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(items: darray[i64]) -> bool:\n    return all((items.enumerate() where index, value: index > 0 and value > 2))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	paren := call.Args[0].(*ast.ParenExpr)
	whereCall := paren.Inner.(*ast.CallExpr)
	lambda, ok := whereCall.Args[1].(*ast.LambdaExpr)
	if !ok || len(lambda.Params) != 1 || lambda.Params[0].Name != "__where_item" {
		t.Fatalf("expected rewritten enumerate tuple lambda, got %T %#v", whereCall.Args[1], whereCall.Args[1])
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "items.enumerate() where index, value: ((index > 0) and (value > 2))") {
		t.Fatalf("expected formatted enumerate where predicate, got:\n%s", formatted)
	}
}

func TestParseExpressionWhereViewWithEnumerateSubjectPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    None\n\ndef keep(items: darray[Expr]) -> bool:\n    return any((items.enumerate() where index, item is Expr.Int(value): value > index))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	paren := call.Args[0].(*ast.ParenExpr)
	whereCall := paren.Inner.(*ast.CallExpr)
	lambda, ok := whereCall.Args[1].(*ast.LambdaExpr)
	if !ok || len(lambda.Params) != 1 || lambda.Params[0].Name != "__where_item" {
		t.Fatalf("expected rewritten enumerate tuple lambda, got %T %#v", whereCall.Args[1], whereCall.Args[1])
	}
	if _, ok := lambda.BodyExpr.(*ast.MatchExpr); !ok {
		t.Fatalf("expected guarded tuple subject pattern to lower to match expr, got %T", lambda.BodyExpr)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "items.enumerate() where index, value is Expr.Int(value): (value > index)") {
		t.Fatalf("expected formatted enumerate subject pattern, got:\n%s", formatted)
	}
}

func TestParseExpressionWhereViewWithVariantPattern(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    None\n\ndef keep(items: darray[Expr]) -> bool:\n    return any((items where Expr.Int(value)))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	paren := call.Args[0].(*ast.ParenExpr)
	whereCall := paren.Inner.(*ast.CallExpr)
	lambda, ok := whereCall.Args[1].(*ast.LambdaExpr)
	if !ok || len(lambda.Params) != 1 || lambda.Params[0].Name != "__where_item" {
		t.Fatalf("expected pattern where lambda, got %T %#v", whereCall.Args[1], whereCall.Args[1])
	}
	condition, ok := lambda.BodyExpr.(*ast.BinaryExpr)
	if !ok || condition.Op != lexer.TOKEN_IS {
		t.Fatalf("expected pattern where is condition, got %T %#v", lambda.BodyExpr, lambda.BodyExpr)
	}
	target, ok := condition.Right.(*ast.VariantTestExpr)
	if !ok || target.Pattern.EnumName != "Expr" || target.Pattern.Variant != "Int" {
		t.Fatalf("expected Expr.Int pattern target, got %T %#v", condition.Right, condition.Right)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return any((items where Expr.Int(value)))") {
		t.Fatalf("expected formatted expression where pattern, got:\n%s", formatted)
	}
}

func TestParseExpressionWhereViewWithVariantPatternPredicate(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: i64)\n    None\n\ndef keep(items: darray[Expr]) -> bool:\n    return any((items where Expr.Int(value): value > 0))\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ret := decl.Body[0].(*ast.ReturnStmt)
	call := ret.Value.(*ast.CallExpr)
	paren := call.Args[0].(*ast.ParenExpr)
	whereCall := paren.Inner.(*ast.CallExpr)
	lambda := whereCall.Args[1].(*ast.LambdaExpr)
	matchExpr, ok := lambda.BodyExpr.(*ast.MatchExpr)
	if !ok || len(matchExpr.Arms) != 2 {
		t.Fatalf("expected pattern predicate match expression, got %T %#v", lambda.BodyExpr, lambda.BodyExpr)
	}
	if _, ok := matchExpr.Arms[0].Pattern.(*ast.MatchVariantPattern); !ok {
		t.Fatalf("expected first arm to be variant pattern, got %T", matchExpr.Arms[0].Pattern)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "return any((items where Expr.Int(value): (value > 0)))") {
		t.Fatalf("expected formatted expression where pattern predicate, got:\n%s", formatted)
	}
}
