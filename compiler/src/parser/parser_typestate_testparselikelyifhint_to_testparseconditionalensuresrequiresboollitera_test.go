package parser

import (
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

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
func TestParseBreakAndContinueStatements(t *testing.T) {
	file, errs := parseSourceFile(t, "def fold(limit: int) -> int:\n    i: mutable int = 0\n    while i < limit:\n        i <- i + 1\n        if i == 2:\n            continue\n        if i == 4:\n            break\n    return i\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected func decl, got %T", file.Decls[0])
	}
	loop, ok := decl.Body[1].(*ast.WhileStmt)
	if !ok {
		t.Fatalf("expected second stmt to be while, got %T", decl.Body[1])
	}
	firstIf, ok := loop.Body[1].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected first loop branch to be if, got %T", loop.Body[1])
	}
	if _, ok := firstIf.Then[0].(*ast.ContinueStmt); !ok {
		t.Fatalf("expected continue stmt, got %T", firstIf.Then[0])
	}
	secondIf, ok := loop.Body[2].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected second loop branch to be if, got %T", loop.Body[2])
	}
	if _, ok := secondIf.Then[0].(*ast.BreakStmt); !ok {
		t.Fatalf("expected break stmt, got %T", secondIf.Then[0])
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "continue") || !strings.Contains(formatted, "break") {
		t.Fatalf("expected unparse to preserve break/continue, got:\n%s", formatted)
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

// `treeview[T]` has been removed (it was a redundant alias for the bare concrete variant type `T`).
func TestParseTreeViewSurfaceTypeRejected(t *testing.T) {
	_, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Nil\n        Binary(left: Expr, right: Expr)\n\ndef keep(view_value: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:\n    return view_value\n")
	if !strings.Contains(strings.Join(errs, "\n"), "`treeview[T]` has been removed") {
		t.Fatalf("expected treeview removal diagnostic, got: %v", errs)
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
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Nil\n        Unary(expr: Expr)\n        Binary(left: Expr, right: Expr)\n        Call(callee: Expr, args: darray[Expr])\n\ndef kind(node: Lua.Expr) -> i64:\n    return visit node:\n        Lua.Expr.Nil(expr):\n            0\n        Lua.Expr.Binary(expr):\n            expr.left.span\n\ndef score(node: Lua.Expr) -> i64:\n    return fold node as Lua.Node into i64:\n        Lua.Expr.Nil(expr, children):\n            expr.span + children.len.i64()\n        Lua.Expr.Unary(expr, expr: inner):\n            try inner + expr.span\n        Lua.Expr.Binary(expr, left, right):\n            try left + try right + expr.span\n        Lua.Expr.Call(expr, callee, args: arg_values):\n            try callee + arg_values.len.i64() + expr.span\n")
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
		t.Fatalf("unexpected unary fold bindings: %#v", foldExpr.Arms[1].ChildBindings)
	}
	if len(foldExpr.Arms[2].ChildBindings) != 2 || foldExpr.Arms[2].ChildBindings[0].FieldName != "left" || foldExpr.Arms[2].ChildBindings[0].BindName != "left" || foldExpr.Arms[2].ChildBindings[1].FieldName != "right" || foldExpr.Arms[2].ChildBindings[1].BindName != "right" {
		t.Fatalf("unexpected binary fold bindings: %#v", foldExpr.Arms[2].ChildBindings)
	}
	if len(foldExpr.Arms[3].ChildBindings) != 2 || foldExpr.Arms[3].ChildBindings[0].FieldName != "callee" || foldExpr.Arms[3].ChildBindings[0].BindName != "callee" || foldExpr.Arms[3].ChildBindings[1].FieldName != "args" || foldExpr.Arms[3].ChildBindings[1].BindName != "arg_values" {
		t.Fatalf("unexpected call fold bindings: %#v", foldExpr.Arms[3].ChildBindings)
	}
}
func TestParseTreeRewriteExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Int(value: i64)\n        Binary(left: Expr, right: Expr)\n\ndef simplify(node: Lua.Expr) -> Lua.Expr:\n    in perm:\n        return rewrite node as Lua.Expr default:\n            Lua.Expr.Binary(expr, left, right):\n                default{span = expr.span, left, right}\n")
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
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Nil\n        Binary(left: Expr, right: Expr)\n\nattribute Lua.Node.checksum -> i64 error[LuaFrontendError]:\n    Lua.Expr.Binary(node, left, right):\n        lua_binary_checksum(node.span, left.checksum, right.checksum)\n    _:\n        0\n")
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
		t.Fatalf("unexpected bindings: %#v", decl.Arms[0].ChildBindings)
	}
	if !decl.Arms[1].Wildcard {
		t.Fatalf("expected wildcard fallback arm, got %#v", decl.Arms[1])
	}
}
func TestParseSequenceRewriteExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep_non_zero(owner: mutable Arena&, items: view[u32]) -> darray[u32]:\n    can Abort.Panic, Memory.Allocate:\n        in owner:\n            return rewrite items as sequence[u32]:\n                item when item != 0u32:\n                    emit item\n")
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
	file, errs := parseSourceFile(t, "tree Lua:\n    common:\n        span: i64\n    @role(expr)\n    node Expr:\n        Int(value: i64)\n        Name(name: u32)\n\ndef keep_int_values(owner: mutable Arena&, items: view[Lua.Expr]) -> darray[i64]:\n    can Abort.Panic, Memory.Allocate:\n        in owner:\n            return rewrite items as sequence[i64]:\n                Lua.Expr.Int(expr) when expr.value > 0:\n                    emit expr.value\n")
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
	file, errs := parseSourceFile(t, "def concat(owner: mutable Arena&, left: view[u32], right: view[u32]) -> darray[u32]:\n    can Abort.Panic, Memory.Allocate:\n        in owner:\n            segments: darray[view[u32]] = [left, right]\n            return rewrite segments as sequence[u32]:\n                segment:\n                    emit all segment\n")
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
