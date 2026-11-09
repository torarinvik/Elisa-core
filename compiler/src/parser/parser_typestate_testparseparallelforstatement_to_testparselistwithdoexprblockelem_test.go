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
	file, errs := parseSourceFile(t, "struct Pair:\n    left: int\n    right: int\n\ndef walk(items: array[Pair, 2]) -> void:\n    for Pair(left, right) in items:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindValue {
		t.Fatalf("expected value bind mode, got %v", iterStmt.Mode)
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

// The `mutable ref` / `ref` binder spellings have been removed: copy-vs-ref on an
// immutable binding is the compiler's decision (affine read-only iteration
// auto-borrows), and `mutable` alone is the mutate-in-place spelling. Both removed
// forms get a directed diagnostic with recover-as-canonical (mode preserved, no
// cascade).
func TestParseIterableForStatementMutableRefBinderRemoved(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for mutable ref item in items:\n        pass\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "`for mutable ref x in ...` has been removed") {
		t.Fatalf("expected exactly the mutable-ref removal diagnostic, got: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindMutableRef {
		t.Fatalf("expected recovery to keep mutable-ref bind mode, got %v", iterStmt.Mode)
	}
}

func TestParseIterableForStatementRefBinderRemoved(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for ref item in items:\n        pass\n")
	if len(errs) != 1 || !strings.Contains(errs[0], "`for ref x in ...` has been removed") {
		t.Fatalf("expected exactly the ref removal diagnostic, got: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindRef {
		t.Fatalf("expected recovery to keep ref bind mode, got %v", iterStmt.Mode)
	}
}

// A loop variable literally named `ref` (`for ref in xs`) is untouched by the removal.
func TestParseIterableForStatementRefAsVariableName(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for ref in items:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindValue {
		t.Fatalf("expected value bind mode for a variable named ref, got %v", iterStmt.Mode)
	}
}

// `for mutable item in ...` is the sole spelling of the mutate-in-place binder.
func TestParseIterableForStatementWithMutableBinderShorthand(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for mutable item in items:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[0].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.Mode != ast.IterBindMutableRef {
		t.Fatalf("expected mutable ref bind mode from `mutable` shorthand, got %v", iterStmt.Mode)
	}
	pattern, ok := iterStmt.Pattern.(*ast.MoveBindNamePattern)
	if !ok || pattern.Name != "item" {
		t.Fatalf("expected name pattern item, got %T %#v", iterStmt.Pattern, iterStmt.Pattern)
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

func TestParseIterableForStatementWithSubjectIsPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    None\n\ndef walk(items: darray[Expr]) -> void:\n    for item in items where item is Expr.Int(value):\n        pass\n")
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
	if iterStmt.PatternFilterSubject != "item" {
		t.Fatalf("expected explicit pattern subject item, got %q", iterStmt.PatternFilterSubject)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where item is Expr.Int(value):") {
		t.Fatalf("expected subject-is filter to preserve its subject, got:\n%s", formatted)
	}
}

func TestParseIterableForTupleStatementWithSubjectIsPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    None\n\ndef walk(items: darray[Expr]) -> void:\n    for index, item in items.enumerate() where item is Expr.Int(value):\n        pass\n")
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
	if iterStmt.PatternFilterSubject != "item" {
		t.Fatalf("expected explicit pattern subject item, got %q", iterStmt.PatternFilterSubject)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for index, item in items.enumerate() where item is Expr.Int(value):") {
		t.Fatalf("expected formatter to preserve tuple subject pattern filter, got:\n%s", formatted)
	}
}

func TestParseIterableForStructStatementWithSubjectIsPatternFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    None\n\nstruct Entry:\n    index: int\n    item: Expr\n\ndef walk(entries: darray[Entry]) -> void:\n    for {index, item} in entries where item is Expr.Int(value):\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[2].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	pattern, ok := iterStmt.Pattern.(*ast.MoveBindStructPattern)
	if !ok || len(pattern.Args) != 2 {
		t.Fatalf("expected struct loop pattern, got %T %#v", iterStmt.Pattern, iterStmt.Pattern)
	}
	filter, ok := iterStmt.PatternFilter.(*ast.MatchVariantPattern)
	if !ok {
		t.Fatalf("expected variant pattern filter, got %T", iterStmt.PatternFilter)
	}
	if filter.EnumName != "Expr" || filter.Variant != "Int" {
		t.Fatalf("unexpected pattern filter target: %#v", filter)
	}
	if iterStmt.PatternFilterSubject != "item" {
		t.Fatalf("expected explicit pattern subject item, got %q", iterStmt.PatternFilterSubject)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for {index, item} in entries where item is Expr.Int(value):") {
		t.Fatalf("expected formatter to preserve struct subject pattern filter, got:\n%s", formatted)
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

func TestParseIterableForStatementWithPatternFilterGuard(t *testing.T) {
	file, errs := parseSourceFile(t, "enum Expr:\n    Int(value: int)\n    None\n\ndef walk(items: darray[Expr]) -> void:\n    for item in items where Expr.Int(value): value > 0:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	iterStmt, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected iterable for stmt, got %T", decl.Body[0])
	}
	if iterStmt.PatternFilter == nil || iterStmt.WhereFilter == nil {
		t.Fatalf("expected guarded pattern filter, got pattern=%T where=%T", iterStmt.PatternFilter, iterStmt.WhereFilter)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for item in items where Expr.Int(value): (value > 0):") {
		t.Fatalf("expected formatter to preserve guarded pattern filter, got:\n%s", formatted)
	}
}

func TestParseIterableForStatementWithEnumerateTuplePattern(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for index, value in items.enumerate():\n        pass\n")
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
	callee, ok := call.Func.(*ast.FieldExpr)
	if !ok || callee.Field != "enumerate" {
		t.Fatalf("expected enumerate callee, got %T %#v", call.Func, call.Func)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for index, value in items.enumerate():") {
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

func TestParseIterableForStatementWithEnumerateInlineWhereFilter(t *testing.T) {
	file, errs := parseSourceFile(t, "def walk(items: darray[int]) -> void:\n    for index, value in items.enumerate() where index > 0 and value > 2:\n        pass\n")
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
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for index, value in items.enumerate() where ((index > 0) and (value > 2)):") {
		t.Fatalf("expected formatter to preserve enumerate inline where filter, got:\n%s", formatted)
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
	file, errs := parseSourceFile(t, "def walk(decls: darray[Pascal::Decl]) -> void:\n    for decl in decls where Pascal::Decl.VarDecl(var_name_id, type_expr, _):\n        pass\n")
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
