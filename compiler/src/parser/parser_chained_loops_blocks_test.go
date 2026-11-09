package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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

func TestParseChainedIterableForStatementAfterWhereLowersToNestedLoops(t *testing.T) {
	file, errs := parseSourceFile(t, "struct Block:\n    labels: darray[int]\n\ndef collect(blocks: darray[Block]) -> void:\n    for block in blocks where block.labels.count > 0 for label in block.labels:\n        pass\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	outer, ok := decl.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected outer iterable for stmt, got %T", decl.Body[0])
	}
	if outer.WhereFilter == nil {
		t.Fatalf("expected outer loop to keep where filter")
	}
	if len(outer.Body) != 1 {
		t.Fatalf("expected outer body to contain nested loop, got %d statements", len(outer.Body))
	}
	inner, ok := outer.Body[0].(*ast.IterForStmt)
	if !ok {
		t.Fatalf("expected inner iterable for stmt, got %T", outer.Body[0])
	}
	if pattern, ok := inner.Pattern.(*ast.MoveBindNamePattern); !ok || pattern.Name != "label" {
		t.Fatalf("expected inner binder label, got %T %#v", inner.Pattern, inner.Pattern)
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

func TestParseReverseIterableForScopeStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]\n\ndef walk(items: darray[int]) -> void:\n    for value in rev(items):\n        pass\n    scope pool_new(2):\n        pass\n")
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
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "for value in rev(items):") || !strings.Contains(formatted, "scope pool_new(2):") {
		t.Fatalf("expected formatter to preserve reverse/scope syntax, got:\n%s", formatted)
	}
}
func TestParseCheckpointStatementRejected(t *testing.T) {
	for _, src := range []string{
		"def walk(items: darray[int], others: darray[int]) -> void:\n    checkpoint items, others:\n        pass\n",
		"def walk(items: darray[int]) -> void:\n    checkpoint mark = items:\n        pass\n",
	} {
		_, errs := parseSourceFile(t, src)
		if len(errs) == 0 {
			t.Fatalf("expected `checkpoint` to be rejected, but parse succeeded for:\n%s", src)
		}
		if !strings.Contains(strings.Join(errs, "\n"), "`checkpoint …:` has been removed") {
			t.Fatalf("expected checkpoint removal diagnostic, got: %v", errs)
		}
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
func TestParseStoreDeclRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "store PendingGotoStore:\n    name_key: u32\n    depth: u32\n")
	if len(errs) == 0 {
		t.Fatal("expected `store Name:` to be rejected")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "`store Name:` declarations have been removed") {
		t.Fatalf("expected removed store declaration diagnostic, got: %v", errs)
	}
}
func TestParseNodeConstructorSugarRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "def make(span: Span, left: Expr, right: Expr) -> Expr:\n    return node[span = span] Expr.Binary(left: left, right: right)\n")
	if len(errs) == 0 {
		t.Fatal("expected `node[...]` constructor sugar to be rejected")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "`node[...] Tree.Member(...)` has been removed") {
		t.Fatalf("expected removed node constructor diagnostic, got: %v", errs)
	}
}
func TestParseGetOrInsertExplicitDefaultArg(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(values: dict[u32, i64], key: u32):\n    slot = values.get_or_insert(key, 42)\n")
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
		t.Fatalf("expected get_or_insert call to have key and default args, got %d", len(call.Args))
	}
	if _, ok := call.Args[0].(*ast.Ident); !ok {
		t.Fatalf("expected first arg to stay the key ident, got %T", call.Args[0])
	}
	if _, ok := call.Args[1].(*ast.IntLit); !ok {
		t.Fatalf("expected block expression to become second call arg, got %T", call.Args[1])
	}
}
func TestParseGetOrInsertBlockSugarRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "def keep(values: dict[u32, i64], key: u32):\n    slot = values.get_or_insert(key):\n        42\n")
	if len(errs) == 0 {
		t.Fatalf("expected get_or_insert block default syntax to be rejected")
	}
	joined := strings.Join(errs, "\n")
	if !strings.Contains(joined, "expected newline") {
		t.Fatalf("expected parser rejection for block default syntax, got: %s", joined)
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

// docs/119 §4: a block-bodied `match` (or `if`) as the final statement of an
// expression block is now the block's value — it maps onto MatchExpr — rather than
// the old "block requires a final expression" error.
func TestParseExprBlockFinalMatchIsValue(t *testing.T) {
	_, errs := parseSourceFile(t, "const enum Op of i32:\n    ADD = 1\n    SUB = 2\n\ndef keep(op: Op) -> i64:\n    return do:\n        match op:\n            Op.ADD:\n                10\n            Op.SUB:\n                20\n")
	if len(errs) != 0 {
		t.Fatalf("a final block-bodied match is now the block value (docs/119 §4), got errors: %v", errs)
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

// `::` is the only module-path separator. A `.` where a module path is required is a
// parse error in every declaration/pattern position, reported at the head identifier
// with the corrected spelling; the chain is still consumed so nothing cascades.
func TestParseRejectsDotAsModuleSeparatorInDeclarationPositions(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"generic bound", "def g[T: Pack.Show](x: int) -> int:\n    return x\n", "write `Pack::Show`"},
		{"using path", "using Pack.Sub\n", "write `Pack::Sub`"},
		{"from import", "from Pack.Sub import f\n", "write `Pack::Sub`"},
		{"enum parent", "enum Child is M2.Base:\n    C\n", "write `M2::Base`"},
		{"match variant path", "def f(k: int) -> int:\n    match k:\n        Pack.Kind.A:\n            return 1\n        _:\n            return 0\n", "write `Pack::Kind.A`"},
		{"let destructure", "def f() -> int:\n    let Pack.Item{v} = it\n    return v\n", "write `Pack::Item`"},
	}
	for _, tc := range cases {
		_, errs := parseSourceFile(t, tc.src)
		if len(errs) != 1 {
			t.Fatalf("%s: expected exactly one parse error, got %v", tc.name, errs)
		}
		if !strings.Contains(errs[0], "module paths are separated by `::`, not `.`; "+tc.want) {
			t.Fatalf("%s: unexpected diagnostic: %s", tc.name, errs[0])
		}
	}
}
