package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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
func TestParseIterableForStatementWithPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    None\n\ndef walk(items: darray[Expr]) -> void:\n    for item in items where Expr.Int(value):\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	pattern, ok := iterStmt.PatternFilter.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected variant pattern filter, got %T", iterStmt.PatternFilter)
	}
	if pattern.EnumName != "Expr" || pattern.Variant != "Int" {
		t.Fatalf("unexpected pattern filter target: %#v", pattern)
	}
	if len(pattern.Args) != 1 {
		t.Fatalf("expected one pattern argument, got %d", len(pattern.Args))
	}
	bind, ok := pattern.Args[0].Pattern.(*ast.MatchBindPattern)
	if !ok || bind.Name != "value" {
		t.Fatalf("expected payload binding `value`, got %#v", pattern.Args[0].Pattern)
	}
	formatted := unparse.FormatDecl(decl)
	if !strings.Contains(formatted, "for item in items where Expr.Int(value):") {
		t.Fatalf("expected formatter to preserve pattern filter, got:\n%s", formatted)
	}
}
func TestParseIterableForStatementWithBareVariantPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    None\n\ndef walk(items: darray[Expr]) -> void:\n    for item in items where Expr.Int:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	pattern, ok := iterStmt.PatternFilter.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected variant pattern filter, got %T", iterStmt.PatternFilter)
	}
	if pattern.EnumName != "Expr" || pattern.Variant != "Int" {
		t.Fatalf("unexpected pattern filter target: %#v", pattern)
	}
	if len(pattern.Args) != 0 {
		t.Fatalf("expected bare variant pattern to bind no arguments, got %d", len(pattern.Args))
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where Expr.Int:") {
		t.Fatalf("expected formatter to preserve bare pattern filter, got:\n%s", formatted)
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

func TestParseIterableForStatementWithWherePredicateClause(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: int) -> bool:\n    return value > 0\n\ndef walk(items: darray[int]) -> void:\n    for item in items where keep:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	call, ok := iterStmt.Source.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected where call source, got %T", iterStmt.Source)
	}
	callee, ok := call.Func.(*ast.Ident)
	if !ok || callee.Name != "where" {
		t.Fatalf("expected where callee, got %T %#v", call.Func, call.Func)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected where source and predicate args, got %d", len(call.Args))
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where keep:") {
		t.Fatalf("expected formatter to preserve where predicate loop syntax, got:\n%s", formatted)
	}
}

func TestParseIterableForStatementWithInlineWhereFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for item in items where item > 0:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if _, ok := iterStmt.WhereFilter.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected binary where filter, got %T", iterStmt.WhereFilter)
	}
	if _, ok := iterStmt.Source.(*ast.Ident); !ok {
		t.Fatalf("expected plain source expression, got %T", iterStmt.Source)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where (item > 0):") {
		t.Fatalf("expected formatter to preserve inline where filter, got:\n%s", formatted)
	}
}

func TestParseIterableForStatementWithInlineFieldWhereFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Item:\n    kind: int\n\ndef walk(items: darray[Item]) -> void:\n    for item in items where item.kind == 1:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if _, ok := iterStmt.WhereFilter.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected binary where filter, got %T", iterStmt.WhereFilter)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where (item.kind == 1):") {
		t.Fatalf("expected formatter to preserve inline field where filter, got:\n%s", formatted)
	}
}

func TestParseIterableForStatementWithInlineQualifiedFieldWhereFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum TokenKind:\n    IDENT\n    EOF\n\nstruct Token:\n    kind: TokenKind\n\ndef walk(items: darray[Token]) -> void:\n    for item in items where item.kind == TokenKind.IDENT:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if _, ok := iterStmt.WhereFilter.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected binary where filter, got %T", iterStmt.WhereFilter)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where (item.kind == TokenKind.IDENT):") {
		t.Fatalf("expected formatter to preserve inline qualified field where filter, got:\n%s", formatted)
	}
}

func TestParseIterableForStatementWithInlineCallWhereFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "def is_selected(item: int) -> bool:\n    return item > 0\n\ndef walk(items: darray[int]) -> void:\n    for item in items where is_selected(item):\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if _, ok := iterStmt.WhereFilter.(*ast.CallExpr); !ok {
		t.Fatalf("expected call where filter, got %T", iterStmt.WhereFilter)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where is_selected(item):") {
		t.Fatalf("expected formatter to preserve inline call where filter, got:\n%s", formatted)
	}
}

func TestParseIterableForStatementKeepsQualifiedVariantWherePattern(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(decls: darray[Pascal.Decl]) -> void:\n    for decl in decls where Pascal.Decl.VarDecl(var_name_id, type_expr, _):\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	pattern, ok := iterStmt.PatternFilter.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected qualified variant pattern filter, got %T", iterStmt.PatternFilter)
	}
	if pattern.EnumName != "Pascal.Decl" || pattern.Variant != "VarDecl" {
		t.Fatalf("unexpected variant pattern: %#v", pattern)
	}
}

func TestParseChainedIterableForStatementLowersToNestedLoops(t *testing.T) {
	file, errs := parseSourceFile(t, "def collect(groups: darray[darray[int]]) -> void:\n    for group in groups for value in group:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	outer, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected outer iterable for stmt, got %T", decl.Body[0])
	}
	if pattern, ok := outer.Pattern.(*ast.MoveBindNamePattern); !ok || pattern.Name != "group" {
		t.Fatalf("expected outer binder group, got %T %#v", outer.Pattern, outer.Pattern)
	}
	if len(outer.Body) != 1 {
		t.Fatalf("expected outer body to contain nested loop, got %d statements", len(outer.Body))
	}
	inner, ok := outer.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected inner iterable for stmt, got %T", outer.Body[0])
	}
	if pattern, ok := inner.Pattern.(*ast.MoveBindNamePattern); !ok || pattern.Name != "value" {
		t.Fatalf("expected inner binder value, got %T %#v", inner.Pattern, inner.Pattern)
	}
	if len(inner.Body) != 1 {
		t.Fatalf("expected inner body to contain original loop body, got %d statements", len(inner.Body))
	}
}

func TestParseChainedRangeForStatementLowersToNestedLoops(t *testing.T) {
	file, errs := parseSourceFile(t, "def mix(rounds: usize, len: usize) -> void:\n    for round in 0..<rounds for i in 0..<len:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	outer, ok := decl.Body[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected outer range for stmt, got %T", decl.Body[0])
	}
	if outer.Name != "round" {
		t.Fatalf("expected outer binder round, got %q", outer.Name)
	}
	if len(outer.Body) != 1 {
		t.Fatalf("expected outer body to contain nested loop, got %d statements", len(outer.Body))
	}
	inner, ok := outer.Body[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("expected inner range for stmt, got %T", outer.Body[0])
	}
	if inner.Name != "i" {
		t.Fatalf("expected inner binder i, got %q", inner.Name)
	}
	if len(inner.Body) != 1 {
		t.Fatalf("expected inner body to contain original loop body, got %d statements", len(inner.Body))
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
	file, errs := parseSourceFile(t, "def keep(values: dict[cstr[key_shape], i64], key: cstr[key_shape]):\n    slot = values.get_or_insert(key):\n        42\n")
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
	file, errs := parseSourceFile(t, "def keep(values: dict[cstr[key_shape], i64], key: cstr[key_shape]):\n    slot = values.get_or_insert(key):\n        base = 40\n        base + 2\n")
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
	file, errs := parseSourceFile(t, "def keep(values: dict[cstr[key_shape], i64], key: cstr[key_shape]):\n    slot = values.entry(key).get_or_insert():\n        42\n")
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
func TestParseRejectsDoExprBlockFinalMatchStatement(t *testing.T) {
	_, errs := parseSourceFile(t, "const enum Op of i32:\n    ADD = 1\n    SUB = 2\n\ndef keep(op: Op) -> i64:\n    return do:\n        match op:\n            Op.ADD:\n                10\n            Op.SUB:\n                20\n")
	if len(errs) == 0 {
		t.Fatal("expected parser error for final match statement in do expression block, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "expression block requires a final expression statement in the block") {
		t.Fatalf("expected final-expression diagnostic, got:\n%s", all)
	}
}
func TestParseDirectMatchExprSyntax(t *testing.T) {
	file, errs := parseSourceFile(t, "const enum Op of i32:\n    ADD = 1\n\ndef keep(op: Op) -> i64:\n    return match op:\n        Op.ADD:\n            10\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[1].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[1])
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", fn.Body[0])
	}
	if _, ok := ret.Value.(*ast.MatchExpr); !ok {
		t.Fatalf("expected match expression return value, got %T", ret.Value)
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
