package semantic

import (
	"strings"
	"testing"

	"llcontext/src/ast"
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
    @role(expr)
    node Expr:
        Nil
        Binary(op: i32, child left: Expr, child right: Expr)
    @role(stmt)
    node Stmt:
        Return(child value: Expr)
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
	if exprType.Role != "expr" || exprType.Family != family {
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
	if binaryVariant.PayloadRelation(1) != ast.EnumPayloadRelationChild || binaryVariant.PayloadRelation(2) != ast.EnumPayloadRelationChild {
		t.Fatalf("expected Binary left/right payloads to be structural children, got %#v", binaryVariant.PayloadRelations)
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
	if returnVariant.PayloadRelation(0) != ast.EnumPayloadRelationChild {
		t.Fatalf("expected Return value payload to be a structural child, got %#v", returnVariant.PayloadRelations)
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
    node Expr:
        Nil
    struct Expr:
        value: i64
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, `duplicate type "Lua.Expr"`) {
		t.Fatalf("expected duplicate tree member type diagnostic, got:\n%s", errText)
	}
}

func TestAnalyzeTreeCategoryRoleDirectives(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_role_directives.llcontext", `tree Lua:
    @role(expr)
    @role(stmt)
    node Expr:
        Nil

    @role
    node Broken:
        Nil
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `duplicate @role annotation on tree node "Expr"`) {
		t.Fatalf("expected duplicate @role diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, `@role on tree node "Broken" expects exactly one role argument`) {
		t.Fatalf("expected malformed @role diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeTreeFieldAccess(t *testing.T) {
	analyzeTreeTestSource(t, "tree_field_access.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
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

func TestAnalyzeTreeVariantConstructorsAndIsExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_variant_behaviors.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def make_nil() -> Lua.Expr:
	in perm:
		return Lua.Expr.Nil

def make_binary(span: i64, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
	in perm:
		return Lua.Expr.Binary(span: span, left: left, right: right)

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return node.left.span
	return node.span

def starts_with_nil(node: Lua.Expr) -> bool:
	return node is Lua.Expr.Binary(Lua.Expr.Nil, _)
`)
}

func TestAnalyzeTreeConstructorsSupportExplicitAndScopedOwners(t *testing.T) {
	analyzeTreeTestSource(t, "tree_owner_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def build(owner: Arena) -> Lua.Expr:
	alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
	in alloc:
		left: Lua.Expr = Lua.Expr.Nil(span: 1)
		right: Lua.Expr = new[alloc] Lua.Expr.Nil(span: 2)
		return Lua.Expr.Binary(span: 3, left: left, right: right)

def build_perm() -> Lua.Expr:
	return new[perm] Lua.Expr.Nil(span: 7)
`)
}

func TestAnalyzeRejectsBareTreeConstructorsOutsideOwnerScope(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_owner_required.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def make_nil() -> Lua.Expr:
	return Lua.Expr.Nil(span: 1)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree constructor "Lua.Expr.Nil" requires an active in <owner>: scope or explicit new[owner]`) {
		t.Fatalf("expected bare tree constructor owner diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeTreeViewSurfaceTypeAndRefinedCalls(t *testing.T) {
	analyzeTreeTestSource(t, "tree_view_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:
	return view_node

def score_binary(view_node: treeview[Lua.Expr.Binary]) -> i64:
	kept: treeview[Lua.Expr.Binary] = keep_binary(view_node)
	return kept.left.span + kept.right.span + kept.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`)
}

func TestAnalyzeTreeVariantBareTypeSugarAndRefinedCalls(t *testing.T) {
	analyzeTreeTestSource(t, "tree_variant_bare_type_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: Lua.Expr.Binary) -> Lua.Expr.Binary:
	return view_node

def score_binary(view_node: Lua.Expr.Binary) -> i64:
	kept: Lua.Expr.Binary = keep_binary(view_node)
	return kept.left.span + kept.right.span + kept.span

def child_span(node: Lua.Expr) -> i64:
	if node is Lua.Expr.Binary:
		return score_binary(node)
	return node.span
`)
}

func TestAnalyzeTreeMatchStatementsAndExpressions(t *testing.T) {
	analyzeTreeTestSource(t, "tree_match_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def child_span(node: Lua.Expr) -> i64:
	match node:
		Lua.Expr.Binary(left: left, right: _):
			return node.left.span + left.span
		_:
			return node.span

def eval(node: Lua.Expr) -> i64:
	return match node:
		Lua.Expr.Nil:
			0
		Lua.Expr.Int(value: value):
			value
		Lua.Expr.Binary(left: Lua.Expr.Int(value: lhs), right: right):
			lhs + eval(right)
`)
}

func TestAnalyzeTreeOpenAndViewStatements(t *testing.T) {
	analyzeTreeTestSource(t, "tree_open_view_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:
	return view_node

def child_span(node: Lua.Expr) -> i64:
	view node as Lua.Expr.Binary(binary):
		kept: treeview[Lua.Expr.Binary] = keep_binary(binary)
		return kept.left.span + binary.right.span + node.left.span
	return node.span

def left_value(node: Lua.Expr) -> i64:
	open node as Lua.Expr.Binary(Lua.Expr.Int(value), rhs):
		return value + rhs.span
	return node.span
`)
}

func TestAnalyzeTreeChildrenLoops(t *testing.T) {
	analyzeTreeTestSource(t, "tree_children_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Unary(op: i32, child expr: Expr)
		Binary(op: i32, child left: Expr, child right: Expr)
		Call(child callee: Expr, children args: darray[Expr], link source_expr: Expr)

def count_nodes(node: Lua.Expr) -> i64:
	total: mutable i64 = 1
	for child in children(node):
		total <- total + count_nodes(child)
	return total

def count_binary(binary: Lua.Expr.Binary) -> i64:
	total: mutable i64 = 0
	for child in children(binary):
		total <- total + child.span
	return total
`)
}

func TestAnalyzeRejectsChildrenOnMixedStructuralItemTypes(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_children_mixed.llcontext", `tree Lua:
	@role(stmt)
	node Stmt:
		IfStmt(child condition: Lua.Expr, child body: Lua.Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Lua.Stmt]

def visit(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	for child in children(stmt):
		total <- total + 1
	return total
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `children(...) requires all structural child payloads to have the same item type`) {
		t.Fatalf("expected mixed structural child type diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsTreeOpenAndViewStoreClauses(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_open_view_store_reject.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)

def bad_open(node: Lua.Expr, slot: i64) -> i64:
	open node in slot as Lua.Expr.Int(value: value):
		return value
	return 0

def bad_view(node: Lua.Expr, slot: i64) -> i64:
	view node in slot as Lua.Expr.Int(value: value):
		return value + node.value
	return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree open over "Lua.Expr" does not take an in-store clause`) {
		t.Fatalf("expected tree open in-store rejection, got:\n%s", all)
	}
	if !strings.Contains(all, `tree view over "Lua.Expr" does not take an in-store clause`) {
		t.Fatalf("expected tree view in-store rejection, got:\n%s", all)
	}
}
