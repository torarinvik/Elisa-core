package semantic

import (
	"strings"
	"testing"

	"llcontext/src/lexer"
	"llcontext/src/parser"
)

func analyzeTreeTestSource(t *testing.T, filename string, src string) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	result := Analyze(file)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	return result
}

func analyzeTreeTestSourceWithSemanticErrors(t *testing.T, filename string, src string) *Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return Analyze(file)
}

func TestAnalyzeRegistersTreeFamilyAndMembers(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_registers.llcontext", `tree Lua:
    common:
        @storage(side_table)
        span: i64
    expr Expr:
        Nil
        Binary(op: i32, left: Expr, right: Expr)
    stmt Stmt:
        Return(value: Expr)
    block Block:
        stmts: darray[Stmt]
    struct ElseIf:
        condition: Expr
        body: Block
`)

	family, ok := result.NamedTypes["Lua"].(*TreeType)
	if !ok {
		t.Fatalf("expected Lua tree type, got %T", result.NamedTypes["Lua"])
	}
	if family.Name != "Lua" {
		t.Fatalf("expected family name Lua, got %q", family.Name)
	}
	if len(family.Common) != 1 {
		t.Fatalf("expected one common field, got %d", len(family.Common))
	}
	if _, ok := family.Common["span"]; !ok {
		t.Fatalf("expected common span field, got %#v", family.Common)
	}

	exprType, ok := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr tree category type, got %T", result.NamedTypes["Lua.Expr"])
	}
	if exprType.Kind != "expr" || exprType.Family != family {
		t.Fatalf("unexpected expr category metadata: %#v", exprType)
	}
	if len(exprType.Common) != 1 {
		t.Fatalf("expected expr category to inherit one common field, got %#v", exprType.Common)
	}
	if len(exprType.Variants) != 2 {
		t.Fatalf("expected two expr variants, got %d", len(exprType.Variants))
	}
	binaryVariant, ok := exprType.Variant("Binary")
	if !ok {
		t.Fatalf("expected Binary variant, got %#v", exprType.VariantMap)
	}
	if len(binaryVariant.Payload) != 3 {
		t.Fatalf("expected Binary payload arity 3, got %#v", binaryVariant.Payload)
	}
	if !SameType(binaryVariant.Payload[1], exprType) || !SameType(binaryVariant.Payload[2], exprType) {
		t.Fatalf("expected Binary child payloads to resolve to Lua.Expr, got %#v", binaryVariant.Payload)
	}

	stmtType, ok := result.NamedTypes["Lua.Stmt"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Stmt tree category type, got %T", result.NamedTypes["Lua.Stmt"])
	}
	returnVariant, ok := stmtType.Variant("Return")
	if !ok || len(returnVariant.Payload) != 1 || !SameType(returnVariant.Payload[0], exprType) {
		t.Fatalf("expected Return(value: Expr) payload to resolve to Lua.Expr, got %#v", returnVariant)
	}

	blockType, ok := result.NamedTypes["Lua.Block"].(*TreeBlockType)
	if !ok {
		t.Fatalf("expected Lua.Block tree block type, got %T", result.NamedTypes["Lua.Block"])
	}
	stmtsField, ok := blockType.Fields["stmts"]
	if !ok {
		t.Fatalf("expected stmts field on block type, got %#v", blockType.Fields)
	}
	darrayType, ok := stmtsField.Type.(*DArrayType)
	if !ok || !SameType(darrayType.Elem, stmtType) {
		t.Fatalf("expected stmts field to resolve to darray[Lua.Stmt], got %T %#v", stmtsField.Type, stmtsField.Type)
	}

	elseIfType, ok := result.NamedTypes["Lua.ElseIf"].(*TreeStructType)
	if !ok {
		t.Fatalf("expected Lua.ElseIf tree struct type, got %T", result.NamedTypes["Lua.ElseIf"])
	}
	if !SameType(elseIfType.Fields["condition"].Type, exprType) || !SameType(elseIfType.Fields["body"].Type, blockType) {
		t.Fatalf("expected ElseIf fields to resolve to sibling tree member types, got %#v", elseIfType.Fields)
	}

	memberExpr, ok := family.Member("Expr")
	if !ok || !SameType(memberExpr, exprType) {
		t.Fatalf("expected family member lookup for Expr to resolve to Lua.Expr, got %#v", memberExpr)
	}
}

func TestAnalyzeRejectsDuplicateTreeMemberTypes(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_duplicate_member.llcontext", `tree Lua:
    expr Expr:
        Nil
    struct Expr:
        value: i64
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, `duplicate type "Lua.Expr"`) {
		t.Fatalf("expected duplicate tree member type diagnostic, got:\n%s", errText)
	}
}

func TestAnalyzeTreeFieldAccess(t *testing.T) {
	analyzeTreeTestSource(t, "tree_field_access.llcontext", `tree Lua:
	common:
		span: i64
	expr Expr:
		Nil
	block Block:
		stmts: darray[i64]
	struct ElseIf:
		condition: Expr
		body: Block

def span_of(node: Lua.Expr) -> i64:
	return node.span

def stmt_count(block: Lua.Block) -> usize:
	return block.stmts.count

def cond_span(branch: Lua.ElseIf) -> i64:
	return branch.condition.span
`)
}
