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
