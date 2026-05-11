package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/unparse"
	"strings"
	"testing"
)

func TestParseListComprehensionExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(items: darray[i64]) -> void:\n    values = [item + 1 for item in items if item > 0]\n")
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
	comp, ok := decl.Value.(*ast.ListComprehensionExpr)
	if !ok {
		t.Fatalf("expected list comprehension expr, got %T", decl.Value)
	}
	if comp.Name != "item" {
		t.Fatalf("expected binder name item, got %q", comp.Name)
	}
	if comp.Filter == nil {
		t.Fatal("expected list comprehension filter")
	}
	formatted := unparse.FormatStmt(fn.Body[0])
	if !strings.Contains(formatted, "["+"(item + 1) for item in items if (item > 0)]") {
		t.Fatalf("expected formatter to preserve list comprehension syntax, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "for item in items") {
		t.Fatalf("expected formatter to preserve comprehension loop, got:\n%s", formatted)
	}
	if !strings.Contains(formatted, "if (item > 0)") {
		t.Fatalf("expected formatter to preserve comprehension filter, got:\n%s", formatted)
	}
	if _, ok := comp.Source.(*ast.Ident); !ok {
		t.Fatalf("expected comprehension source ident, got %T", comp.Source)
	}
	if _, ok := comp.Value.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected comprehension value binary expr, got %T", comp.Value)
	}
	if _, ok := comp.Filter.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected comprehension filter binary expr, got %T", comp.Filter)
	}
}

func TestParseListComprehensionExprOverRange(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(count: usize) -> void:\n    values = [index for index in 1..<count]\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	decl, ok := fn.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl stmt, got %T", fn.Body[0])
	}
	comp, ok := decl.Value.(*ast.ListComprehensionExpr)
	if !ok {
		t.Fatalf("expected list comprehension expr, got %T", decl.Value)
	}
	if comp.RangeEnd == nil || comp.RangeOp != lexer.TOKEN_RANGE_LT {
		t.Fatalf("expected exclusive range comprehension, got op=%s end=%T", lexer.TokenName(comp.RangeOp), comp.RangeEnd)
	}
	formatted := unparse.FormatStmt(fn.Body[0])
	if !strings.Contains(formatted, "[index for index in 1 ..< count]") {
		t.Fatalf("expected formatter to preserve range comprehension syntax, got:\n%s", formatted)
	}
}

func TestParseChildrenToOverrideExpr(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(stmt)\n    node Stmt:\n        BreakStmt\n\ndef keep(stmt: Lua.Stmt) -> Lua.Node:\n    return children(stmt as Lua.Node).node\n")
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
	if cast.Origin != ast.CastExprOriginAsSyntax {
		t.Fatalf("expected as-syntax cast origin, got %v", cast.Origin)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "children(stmt as Lua.Node)") {
		t.Fatalf("expected unparse to preserve children as-cast syntax, got:\n%s", formatted)
	}
}
func TestParseRejectsLegacyChildrenToCastSyntax(t *testing.T) {
	_, errs := parseSourceFile(t, "tree Lua:\n    @role(stmt)\n    node Stmt:\n        BreakStmt\n\ndef keep(stmt: Lua.Stmt) -> Lua.Node:\n    return children(stmt to Lua.Node).node\n")
	if !strings.Contains(strings.Join(errs, "\n"), "legacy children cast syntax `expr to T` is deprecated") {
		t.Fatalf("expected legacy children to-cast diagnostic, got %v", errs)
	}
}
func TestParsePostfixShorthandCastFormatsAsPostfixShorthand(t *testing.T) {
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
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "return value.u32()") {
		t.Fatalf("expected unparse to preserve postfix cast shorthand, got:\n%s", formatted)
	}
}
func TestParsePostfixOptionalShorthandCastFormatsAsPostfixShorthand(t *testing.T) {
	file, errs := parseSourceFile(t, "def keep(value: i64) -> u32?:\n    return value.u32?()\n")
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
	if _, ok := cast.Target.(*ast.OptionalTypeExpr); !ok {
		t.Fatalf("expected optional cast target, got %T", cast.Target)
	}
	if formatted := unparse.FormatDecl(decl); !strings.Contains(formatted, "return value.u32?()") {
		t.Fatalf("expected unparse to preserve optional postfix cast shorthand, got:\n%s", formatted)
	}
}
func TestParsePostfixShorthandCastRoundTripsInsideBranchHead(t *testing.T) {
	file, errs := parseSourceFile(t, "def lower(ch: char) -> char:\n    if ch >= 'A' and ch <= 'Z':\n        return (ch.i64() + 32).char()\n    return ch\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	formatted := unparse.FormatFile(file)
	for _, want := range []string{
		"if ((ch >= 'A') and (ch <= 'Z')):",
		"return ((ch.i64() + 32)).char()",
	} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted output to contain %q, got:\n%s", want, formatted)
		}
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
func TestParseAsCastInsideIfConditionCallArgs(t *testing.T) {
	file, errs := parseSourceFile(t, "def accepts(text: u8&) -> bool:\n    return true\n\ndef keep() -> i64:\n    if accepts(\"hello\" as u8&):\n        return 1\n    return 0\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl := file.Decls[1].(*ast.FuncDecl)
	ifStmt, ok := decl.Body[0].(*ast.IfStmt)
	if !ok {
		t.Fatalf("expected if stmt, got %T", decl.Body[0])
	}
	call, ok := ifStmt.Cond.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected call condition, got %T", ifStmt.Cond)
	}
	cast, ok := call.Args[0].(*ast.CastExpr)
	if !ok {
		t.Fatalf("expected as-cast call arg, got %T", call.Args[0])
	}
	if cast.Origin != ast.CastExprOriginAsSyntax {
		t.Fatalf("expected as-syntax cast origin, got %v", cast.Origin)
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
