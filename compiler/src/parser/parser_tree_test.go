package parser

import (
	"strings"
	"testing"

	"llcontext/src/ast"
	"llcontext/src/unparse"
)

func TestParseTreeDeclWithAnnotationsAndMembers(t *testing.T) {
	file, errs := parseSourceFile(t, "@packed_profile(retained_reads)\ntree Lua:\n    common:\n        @storage(side_table)\n        span: LuaSpan\n    @packed_profile(retained_reads)\n    expr Expr:\n        Nil\n        Binary(op: LuaBinaryOp, left: Expr, right: Expr)\n    stmt Stmt:\n        Return(value: Expr)\n    block Block:\n        stmts: List[Stmt]\n    struct ElseIf:\n        condition: Expr\n        body: Block\n")
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
		t.Fatalf("expected expr category member, got %T", decl.Members[0])
	}
	if exprMember.Kind != "expr" || exprMember.Name != "Expr" || len(exprMember.Variants) != 2 {
		t.Fatalf("unexpected expr category member: %#v", exprMember)
	}
	if len(exprMember.Annotations) != 1 || exprMember.Annotations[0].Name != "packed_profile" {
		t.Fatalf("expected expr category annotation, got %#v", exprMember.Annotations)
	}

	stmtMember, ok := decl.Members[1].(*ast.TreeCategoryDecl)
	if !ok {
		t.Fatalf("expected stmt category member, got %T", decl.Members[1])
	}
	if stmtMember.Kind != "stmt" || stmtMember.Name != "Stmt" || len(stmtMember.Variants) != 1 {
		t.Fatalf("unexpected stmt category member: %#v", stmtMember)
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
	for _, want := range []string{"tree Lua:", "expr Expr:", "stmt Stmt:", "block Block:", "struct ElseIf:"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("expected formatted tree decl to contain %q, got:\n%s", want, formatted)
		}
	}
}

func TestParseTreeDeclPreservesGenericCategoryNames(t *testing.T) {
	file, errs := parseSourceFile(t, "tree Compiler:\n    pattern Pattern:\n        Bind(name: Symbol)\n")
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
	if member.Kind != "pattern" || member.Name != "Pattern" {
		t.Fatalf("expected pattern Pattern category, got %#v", member)
	}
	if len(member.Variants) != 1 || member.Variants[0].Name != "Bind" {
		t.Fatalf("expected Bind variant in pattern category, got %#v", member.Variants)
	}
}