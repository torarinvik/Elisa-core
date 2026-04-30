package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseTreeDeclWithAnnotationsAndMembers(t *testing.T) {
	file, errs := parseSourceFile(t, "@packed_profile(retained_reads)\ntree Lua:\n    common:\n        @storage(side_table)\n        span: LuaSpan\n    @role(expr)\n    @packed_profile(retained_reads)\n    node Expr:\n        Nil\n        Binary(op: LuaBinaryOp, left: Expr, right: Expr)\n    @role(stmt)\n    node Stmt:\n        Return(value: Expr)\n    block Block:\n        stmts: List[Stmt]\n    struct ElseIf:\n        condition: Expr\n        body: Block\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.TreeDecl)
	if !ok {
		t.Fatalf("expected tree decl, got %T", file.Decls[0])
	}
	if decl.Name != "Lua" {
		t.Fatalf("expected tree name Lua, got %q", decl.Name)
	}
	if len(decl.Annotations) != 1 || decl.Annotations[0].Name != "packed_profile" {
		t.Fatalf("expected tree-level packed_profile annotation, got %#v", decl.Annotations)
	}
	if len(decl.Common) != 1 {
		t.Fatalf("expected one common field, got %d", len(decl.Common))
	}
	if decl.Common[0].Name != "span" {
		t.Fatalf("expected common field span, got %#v", decl.Common[0])
	}
	if len(decl.Common[0].Annotations) != 1 || decl.Common[0].Annotations[0].Name != "storage" || len(decl.Common[0].Annotations[0].Args) != 1 || decl.Common[0].Annotations[0].Args[0] != "side_table" {
		t.Fatalf("expected @storage(side_table) on common field, got %#v", decl.Common[0].Annotations)
	}
	if len(decl.Members) != 4 {
		t.Fatalf("expected four tree members, got %d", len(decl.Members))
	}

	exprMember, ok := decl.Members[0].(*ast.TreeCategoryDecl)
	if !ok {
		t.Fatalf("expected node member, got %T", decl.Members[0])
	}
	if exprMember.Name != "Expr" || len(exprMember.Variants) != 2 {
		t.Fatalf("unexpected Expr node member: %#v", exprMember)
	}
	if len(exprMember.Annotations) != 2 || exprMember.Annotations[0].Name != "role" || len(exprMember.Annotations[0].Args) != 1 || exprMember.Annotations[0].Args[0] != "expr" || exprMember.Annotations[1].Name != "packed_profile" {
		t.Fatalf("expected stacked Expr node annotations, got %#v", exprMember.Annotations)
	}

	stmtMember, ok := decl.Members[1].(*ast.TreeCategoryDecl)
	if !ok {
		t.Fatalf("expected node member, got %T", decl.Members[1])
	}
	if stmtMember.Name != "Stmt" || len(stmtMember.Variants) != 1 {
		t.Fatalf("unexpected Stmt node member: %#v", stmtMember)
	}
	if len(stmtMember.Annotations) != 1 || stmtMember.Annotations[0].Name != "role" || len(stmtMember.Annotations[0].Args) != 1 || stmtMember.Annotations[0].Args[0] != "stmt" {
		t.Fatalf("expected @role(stmt) on Stmt node, got %#v", stmtMember.Annotations)
	}

	blockMember, ok := decl.Members[2].(*ast.TreeBlockDecl)
	if !ok {
		t.Fatalf("expected block member, got %T", decl.Members[2])
	}
	if blockMember.Name != "Block" || len(blockMember.Fields) != 1 || blockMember.Fields[0].Name != "stmts" {
		t.Fatalf("unexpected block member: %#v", blockMember)
	}
	if generic, ok := blockMember.Fields[0].Type.(*ast.GenericType); !ok || generic.Name != "List" || len(generic.Args) != 1 {
		t.Fatalf("expected block field type List[Stmt], got %T %#v", blockMember.Fields[0].Type, blockMember.Fields[0].Type)
	}

	structMember, ok := decl.Members[3].(*ast.TreeStructDecl)
	if !ok {
		t.Fatalf("expected nested struct member, got %T", decl.Members[3])
	}
	if structMember.Name != "ElseIf" || len(structMember.Fields) != 2 {
		t.Fatalf("unexpected nested struct member: %#v", structMember)
	}

	formatted := unparse.FormatDecl(decl)
	for _, want := range []string{"tree Lua:", "@role(expr)", "node Expr:", "@role(stmt)", "node Stmt:", "block Block:", "struct ElseIf:"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted tree decl to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseNodeConstructionSugar(t *testing.T) {
	file, errs := parseSourceFile(t, `def build(alloc: mutable Arena&, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
    return node[span = combine_span(left.span, right.span)] Lua.Expr.Binary(left: left, right: right)
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("expected function decl, got %T", file.Decls[0])
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatalf("expected return stmt, got %T", fn.Body[0])
	}
	alloc, ok := ret.Value.(*ast.AllocExpr)
	if !ok || !alloc.NodeSugar {
		t.Fatalf("expected node construction sugar to parse as sugared alloc expr, got %T %#v", ret.Value, ret.Value)
	}
	if ident, ok := alloc.Owner.(*ast.Ident); !ok || ident.Name != "alloc" {
		t.Fatalf("expected omitted node alloc owner to default to alloc, got %#v", alloc.Owner)
	}
	call, ok := alloc.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected sugared node value to be constructor call, got %T", alloc.Value)
	}
	if call.ArgName(len(call.Args)-1) != "span" {
		t.Fatalf("expected node span option to inject trailing span arg, got names %#v", call.ArgNames)
	}
	formatted := unparse.FormatFile(file)
	if !strings.Contains(formatted, "return node[span = combine_span(left.span, right.span)] Lua.Expr.Binary(left: left, right: right)") {
		t.Fatalf("expected formatter to preserve node construction sugar, got:\n%s", formatted)
	}
}

func TestParseTreeDeclPreservesGenericCategoryNames(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Compiler:\n    @role(pattern)\n    node Pattern:\n        Bind(name: Symbol)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.TreeDecl)
	if !ok {
		t.Fatalf("expected tree decl, got %T", file.Decls[0])
	}
	member, ok := decl.Members[0].(*ast.TreeCategoryDecl)
	if !ok {
		t.Fatalf("expected tree category member, got %T", decl.Members[0])
	}
	if member.Name != "Pattern" {
		t.Fatalf("expected Pattern node, got %#v", member)
	}
	if len(member.Annotations) != 1 || member.Annotations[0].Name != "role" || len(member.Annotations[0].Args) != 1 || member.Annotations[0].Args[0] != "pattern" {
		t.Fatalf("expected @role(pattern) on Pattern node, got %#v", member.Annotations)
	}
	if len(member.Variants) != 1 || member.Variants[0].Name != "Bind" {
		t.Fatalf("expected Bind variant in pattern category, got %#v", member.Variants)
	}
}

func TestParseTreePayloadRelations(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(expr)\n    node Expr:\n        Binary(op: LuaBinaryOp, child left: Expr, child right: Expr)\n        Call(child callee: Expr, children args: darray[Expr], link origin: Expr)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.TreeDecl)
	if !ok {
		t.Fatalf("expected tree decl, got %T", file.Decls[0])
	}
	member, ok := decl.Members[0].(*ast.TreeCategoryDecl)
	if !ok {
		t.Fatalf("expected tree category member, got %T", decl.Members[0])
	}
	if len(member.Variants) != 2 {
		t.Fatalf("expected two variants, got %#v", member.Variants)
	}
	binary := member.Variants[0]
	if len(binary.Payload) != 3 {
		t.Fatalf("expected Binary payload arity 3, got %#v", binary.Payload)
	}
	if binary.Payload[1].Relation != ast.EnumPayloadRelationChild || binary.Payload[2].Relation != ast.EnumPayloadRelationChild {
		t.Fatalf("expected Binary left/right payloads to preserve child relation, got %#v", binary.Payload)
	}
	call := member.Variants[1]
	if call.Payload[0].Relation != ast.EnumPayloadRelationChild || call.Payload[1].Relation != ast.EnumPayloadRelationChildren || call.Payload[2].Relation != ast.EnumPayloadRelationLink {
		t.Fatalf("expected Call payload relations to be preserved, got %#v", call.Payload)
	}
	formatted := unparse.FormatDecl(decl)
	for _, want := range []string{"child left: Expr", "children args: darray[Expr]", "link origin: Expr"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted tree decl to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseTreePayloadRelationsCanBeOmitted(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(expr)\n    node Expr:\n        Binary(left: Expr, right: Expr)\n        Call(callee: Expr, args: darray[Expr])\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.TreeDecl)
	if !ok {
		t.Fatalf("expected tree decl, got %T", file.Decls[0])
	}
	member := decl.Members[0].(*ast.TreeCategoryDecl)
	binary := member.Variants[0]
	if binary.Payload[0].Relation != ast.EnumPayloadRelationNone || binary.Payload[1].Relation != ast.EnumPayloadRelationNone {
		t.Fatalf("expected omitted payload relations to remain source-implicit in AST, got %#v", binary.Payload)
	}
	call := member.Variants[1]
	if call.Payload[0].Relation != ast.EnumPayloadRelationNone || call.Payload[1].Relation != ast.EnumPayloadRelationNone {
		t.Fatalf("expected omitted payload relations to remain source-implicit in AST, got %#v", call.Payload)
	}
	formatted := unparse.FormatDecl(decl)
	for _, want := range []string{"Binary(left: Expr, right: Expr)", "Call(callee: Expr, args: darray[Expr])"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted tree decl to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseNestedTreeCategoryDecl(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(expr)\n    node Expr:\n        Unary(expr: Expr)\n        node Binary:\n            Add(left: Lua.Expr, right: Lua.Expr)\n            Sub(left: Lua.Expr, right: Lua.Expr)\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.TreeDecl)
	if !ok {
		t.Fatalf("expected tree decl, got %T", file.Decls[0])
	}
	expr, ok := decl.Members[0].(*ast.TreeCategoryDecl)
	if !ok {
		t.Fatalf("expected Expr category, got %T", decl.Members[0])
	}
	if expr.Name != "Expr" || len(expr.Variants) != 1 || len(expr.Nested) != 1 {
		t.Fatalf("unexpected Expr category shape: %#v", expr)
	}
	binary := expr.Nested[0]
	if binary.Name != "Expr.Binary" || len(binary.Variants) != 2 {
		t.Fatalf("expected nested Expr.Binary category with two variants, got %#v", binary)
	}
	formatted := unparse.FormatDecl(decl)
	for _, want := range []string{"node Expr:", "Unary(expr: Expr)", "node Binary:", "Add(left: Lua.Expr, right: Lua.Expr)"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted tree decl to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseTreeOptionalPayloadFields(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Lua:\n    @role(stmt)\n    node Stmt:\n        IfStmt(child condition: Expr, child else_block?: Block)\n        NumericFor(child step?: Expr, children args?: darray[Expr])\n")
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	decl, ok := file.Decls[0].(*ast.TreeDecl)
	if !ok {
		t.Fatalf("expected tree decl, got %T", file.Decls[0])
	}
	member, ok := decl.Members[0].(*ast.TreeCategoryDecl)
	if !ok {
		t.Fatalf("expected tree category member, got %T", decl.Members[0])
	}
	if len(member.Variants) != 2 {
		t.Fatalf("expected two variants, got %#v", member.Variants)
	}
	ifStmt := member.Variants[0]
	if len(ifStmt.Payload) != 2 {
		t.Fatalf("expected IfStmt payload arity 2, got %#v", ifStmt.Payload)
	}
	if ifStmt.Payload[1].Relation != ast.EnumPayloadRelationChild {
		t.Fatalf("expected optional else_block payload to preserve child relation, got %#v", ifStmt.Payload[1])
	}
	if _, ok := ifStmt.Payload[1].Type.(*ast.OptionalTypeExpr); !ok {
		t.Fatalf("expected else_block payload type to be optional, got %T", ifStmt.Payload[1].Type)
	}
	numericFor := member.Variants[1]
	if len(numericFor.Payload) != 2 {
		t.Fatalf("expected NumericFor payload arity 2, got %#v", numericFor.Payload)
	}
	if numericFor.Payload[0].Relation != ast.EnumPayloadRelationChild || numericFor.Payload[1].Relation != ast.EnumPayloadRelationChildren {
		t.Fatalf("expected optional payload relations to be preserved, got %#v", numericFor.Payload)
	}
	if _, ok := numericFor.Payload[0].Type.(*ast.OptionalTypeExpr); !ok {
		t.Fatalf("expected step payload type to be optional, got %T", numericFor.Payload[0].Type)
	}
	if _, ok := numericFor.Payload[1].Type.(*ast.OptionalTypeExpr); !ok {
		t.Fatalf("expected args payload type to be optional, got %T", numericFor.Payload[1].Type)
	}
	formatted := unparse.FormatDecl(decl)
	for _, want := range []string{"child else_block?: Block", "child step?: Expr", "children args?: darray[Expr]"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted tree decl to contain %q, got:\n%s", want, formatted)
		}
	}
}
