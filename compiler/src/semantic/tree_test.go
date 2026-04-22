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
	nodeRoot, ok := result.NamedTypes["Lua.Node"].(*TreeNodeType)
	if !ok || nodeRoot.Family != family {
		t.Fatalf("expected synthesized Lua.Node tree root, got %#v", result.NamedTypes["Lua.Node"])
	}
}

func TestAnalyzeSynthesizesTreeCategoryKindTypesAndShorthandComparisons(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_kind_types.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(child left: Expr, child right: Expr)
	@role(stmt)
	node Stmt:
		Return(child value: Expr)
	block Block:
		stmts: darray[Stmt]

def expr_kind(node: Lua.Expr) -> Lua.Expr.Kind:
	return node.kind

def binary_kind(node: Lua.Expr.Binary) -> Lua.Expr.Kind:
	return node.kind

def node_kind(node: Lua.Node) -> Lua.Node.Kind:
	return node.kind

def block_kind(block: Lua.Block) -> Lua.Node.Kind:
	return block.kind

def stmt_is_return(node: Lua.Stmt) -> bool:
	return node.kind == .Return

def node_is_binary(node: Lua.Node) -> bool:
	return node.kind == .Expr.Binary

def explicit_binary_kind() -> Lua.Expr.Kind:
	return Lua.Expr.Kind.Binary

def explicit_root_binary_kind() -> Lua.Node.Kind:
	return Lua.Node.Kind.Expr.Binary

def explicit_root_block_kind() -> Lua.Node.Kind:
	return Lua.Node.Kind.Block

def shorthand_nil_kind() -> Lua.Expr.Kind:
	return .Nil

def shorthand_root_binary_kind() -> Lua.Node.Kind:
	return .Expr.Binary
`)

	exprType, ok := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if !ok || exprType == nil {
		t.Fatalf("expected Lua.Expr tree category type, got %T", result.NamedTypes["Lua.Expr"])
	}
	stmtType, ok := result.NamedTypes["Lua.Stmt"].(*TreeCategoryType)
	if !ok || stmtType == nil {
		t.Fatalf("expected Lua.Stmt tree category type, got %T", result.NamedTypes["Lua.Stmt"])
	}
	exprKindType, ok := result.NamedTypes["Lua.Expr.Kind"].(*ConstEnumType)
	if !ok || exprKindType == nil {
		t.Fatalf("expected synthesized Lua.Expr.Kind const enum type, got %T", result.NamedTypes["Lua.Expr.Kind"])
	}
	stmtKindType, ok := result.NamedTypes["Lua.Stmt.Kind"].(*ConstEnumType)
	if !ok || stmtKindType == nil {
		t.Fatalf("expected synthesized Lua.Stmt.Kind const enum type, got %T", result.NamedTypes["Lua.Stmt.Kind"])
	}
	nodeType, ok := result.NamedTypes["Lua.Node"].(*TreeNodeType)
	if !ok || nodeType == nil {
		t.Fatalf("expected Lua.Node tree root type, got %T", result.NamedTypes["Lua.Node"])
	}
	nodeKindType, ok := result.NamedTypes["Lua.Node.Kind"].(*ConstEnumType)
	if !ok || nodeKindType == nil {
		t.Fatalf("expected synthesized Lua.Node.Kind const enum type, got %T", result.NamedTypes["Lua.Node.Kind"])
	}
	if exprType.KindType != exprKindType {
		t.Fatalf("expected Lua.Expr kind type to point at synthesized const enum, got %#v", exprType.KindType)
	}
	if stmtType.KindType != stmtKindType {
		t.Fatalf("expected Lua.Stmt kind type to point at synthesized const enum, got %#v", stmtType.KindType)
	}
	if nodeType.KindType != nodeKindType {
		t.Fatalf("expected Lua.Node kind type to point at synthesized const enum, got %#v", nodeType.KindType)
	}
	binaryVariant, ok := exprType.Variant("Binary")
	if !ok || binaryVariant == nil {
		t.Fatalf("expected Binary variant on Lua.Expr, got %#v", exprType.VariantMap)
	}
	returnVariant, ok := stmtType.Variant("Return")
	if !ok || returnVariant == nil {
		t.Fatalf("expected Return variant on Lua.Stmt, got %#v", stmtType.VariantMap)
	}
	binaryMember, ok := exprKindType.Member("Binary")
	if !ok || binaryMember == nil || binaryMember.Value != int64(binaryVariant.Tag) {
		t.Fatalf("expected Lua.Expr.Kind.Binary to mirror Binary tag %d, got %#v", binaryVariant.Tag, binaryMember)
	}
	returnMember, ok := stmtKindType.Member("Return")
	if !ok || returnMember == nil || returnMember.Value != int64(returnVariant.Tag) {
		t.Fatalf("expected Lua.Stmt.Kind.Return to mirror Return tag %d, got %#v", returnVariant.Tag, returnMember)
	}
	nilMember, ok := exprKindType.Member("Nil")
	if !ok || nilMember == nil {
		t.Fatalf("expected Lua.Expr.Kind.Nil member, got %#v", exprKindType.MemberMap)
	}
	rootBinaryMember, ok := nodeKindType.Member("Expr.Binary")
	if !ok || rootBinaryMember == nil || rootBinaryMember.Value != int64(binaryVariant.Tag) {
		t.Fatalf("expected Lua.Node.Kind.Expr.Binary to mirror Binary tag %d, got %#v", binaryVariant.Tag, rootBinaryMember)
	}
	rootReturnMember, ok := nodeKindType.Member("Stmt.Return")
	if !ok || rootReturnMember == nil || rootReturnMember.Value != int64(returnVariant.Tag) {
		t.Fatalf("expected Lua.Node.Kind.Stmt.Return to mirror Return tag %d, got %#v", returnVariant.Tag, rootReturnMember)
	}
	rootBlockMember, ok := nodeKindType.Member("Block")
	if !ok || rootBlockMember == nil {
		t.Fatalf("expected Lua.Node.Kind.Block member, got %#v", nodeKindType.MemberMap)
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
		return Lua.Expr.Nil(span: 1)

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

func TestAnalyzeRejectsMissingCommonTreeConstructorFields(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_common_fields_required.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def make_nil() -> Lua.Expr:
	return new[perm] Lua.Expr.Nil

def make_binary(left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
	in perm:
		return Lua.Expr.Binary(left: left, right: right)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree constructor "Lua.Expr.Nil" requires explicit common fields; use call syntax with named arguments`) {
		t.Fatalf("expected explicit common field diagnostic for bare tree constructor, got:\n%s", all)
	}
	if !strings.Contains(all, `tree constructor "Lua.Expr.Binary" is missing common field "span"`) {
		t.Fatalf("expected missing common field diagnostic for tree constructor call, got:\n%s", all)
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

func TestAnalyzeTreeVariantViewTypeStringCanonicalizesToBareVariant(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_variant_view_string.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

def keep_binary(view_node: treeview[Lua.Expr.Binary]) -> treeview[Lua.Expr.Binary]:
	return view_node
`)
	sym, ok := result.GlobalScope.Lookup("keep_binary")
	if !ok {
		t.Fatal("expected keep_binary symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected keep_binary function type, got %T", sym.Type)
	}
	if len(fnType.Params) != 1 || fnType.Return == nil {
		t.Fatalf("unexpected keep_binary function type shape: %#v", fnType)
	}
	if fnType.Params[0].String() != "Lua.Expr.Binary" {
		t.Fatalf("expected canonical bare refined variant parameter type, got %q", fnType.Params[0].String())
	}
	if fnType.Return.String() != "Lua.Expr.Binary" {
		t.Fatalf("expected canonical bare refined variant return type, got %q", fnType.Return.String())
	}
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

func TestAnalyzeTreeChildrenMixedToRootLoop(t *testing.T) {
	analyzeTreeTestSource(t, "tree_children_root_surface.llcontext", `tree Lua:
	common:
		span: i64
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
	for child in children(stmt to Lua.Node):
		total <- total + child.kind.i64()
	return total
`)
}

func TestAnalyzeTreeVisitExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_visit_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def score(node: Lua.Expr) -> i64:
	return visit node:
		Lua.Expr.Nil(expr):
			expr.span
		Lua.Expr.Int(expr):
			expr.value
		Lua.Expr.Binary(expr):
			expr.left.span + expr.right.span
`)
}

func TestAnalyzeTreeVisitExactMemberExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_visit_exact_member_surface.llcontext", `tree Lua:
	@role(stmt)
	node Stmt:
		ExprStmt(child expr: Expr)
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def stmt_total(block: Lua.Block) -> i64:
	return visit block:
		Lua.Block(node):
			node.stmts.len.i64()
`)
}

func TestAnalyzeTreeSequenceFieldsSurfaceAsViews(t *testing.T) {
	analyzeTreeTestSource(t, "tree_sequence_field_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		Return(child value: Expr)
		ElseIf(child condition: Expr, child body: Block)
		IfStmt(child condition: Expr, child then_block: Block, children elseifs: darray[Stmt], has_else: bool, child else_block: Block)
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def block_total(block: Lua.Block) -> i64:
	total: mutable i64 = block.stmts.len.i64()
	for stmt in block.stmts:
		total <- total + stmt.kind.i64()
	return total + block.stmts[0].kind.i64()

def elseif_total(stmt: Lua.Stmt.IfStmt) -> i64:
	total: mutable i64 = stmt.elseifs.len.i64()
	for branch in stmt.elseifs:
		total <- total + branch.kind.i64()
	return total + stmt.elseifs[0].kind.i64()
`)
}

func TestAnalyzeTreeOptionalChildFields(t *testing.T) {
	analyzeTreeTestSource(t, "tree_optional_child_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		ElseIf(child condition: Expr, child body: Block)
		IfStmt(child condition: Expr, child then_block: Block, children elseifs: darray[Stmt], child else_block?: Block)
		NumericFor(name_index: u32, child start: Expr, child limit: Expr, child step?: Expr, child body: Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Stmt]

def optional_i64_value(value: i64?) -> i64:
	return value if value != null else 0

def has_else(stmt: Lua.Stmt.IfStmt) -> bool:
	else_block: Lua.Block? = stmt.else_block
	return else_block != null

def count_children(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	for child in children(stmt to Lua.Node):
		total <- total + child.kind.i64()
	return total

def score(node: Lua.Stmt) -> i64:
	return fold node as Lua.Node into i64:
		Lua.Expr.Name(expr):
			expr.name_index.i64() + expr.span
		Lua.Block(block, children):
			children.len.i64() + block.span
		Lua.Stmt.ElseIf(stmt, condition, body):
			condition + body + stmt.span
		Lua.Stmt.IfStmt(stmt, condition, then_block, elseifs: elseif_values, else_block):
			optional_i64_value(else_block) + condition + then_block + elseif_values.len.i64() + stmt.span
		Lua.Stmt.NumericFor(stmt, start, limit, step, body):
			optional_i64_value(step) + start + limit + body + stmt.name_index.i64()
`)
}

func TestAnalyzeTreeFoldExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_fold_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Call(child callee: Expr, children args: darray[Expr])
		Binary(child left: Expr, child right: Expr)

def score(node: Lua.Expr) -> i64:
	return fold node as Lua.Node into i64:
		Lua.Expr.Nil(expr, children):
			expr.span + children.len.i64()
		Lua.Expr.Int(expr, children):
			expr.value + children.len.i64()
		Lua.Expr.Call(expr, callee, args: arg_values):
			callee + arg_values.len.i64() + expr.span
		Lua.Expr.Binary(expr, left, right):
			left + right + expr.span
`)
}

func TestAnalyzeTreeRewriteExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default
`)
}

func TestAnalyzeTreeRewriteExprPreservesHeterogeneousChildTypes(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_heterogeneous_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Function(child body: Block)
	block Block:
		items: darray[Expr]

def clone_expr(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Node:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Function(expr, body: body):
				default
			Lua.Block(block, items: items):
				default

def clone_block(block: Lua.Block) -> Lua.Block:
	in perm:
		return rewrite block as Lua.Node:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Function(expr, body: body):
				default
			Lua.Block(block, items: items):
				default
`)
}

func TestAnalyzeTreeExactRecordUpdateExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_exact_update_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	in perm:
		return node{left, right}

def rewrite_binary_explicit(owner: Arena, node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
	return new[alloc] node{left, right}
`)
}

func TestAnalyzeTreeExactRecordUpdateRequiresOwner(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_exact_update_owner_required.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	return node{left, right}
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `tree update of "Lua.Expr.Binary" requires an active in <owner>: scope or explicit new[owner]`) {
		t.Fatalf("expected tree update owner diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeTreeRewriteDefaultExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_default_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default
`)
}

func TestAnalyzeTreeRewriteImplicitDefaultExpr(t *testing.T) {
	analyzeTreeTestSource(t, "tree_rewrite_implicit_default_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr default:
			Lua.Expr.Binary(expr, left, right):
				default{span = expr.span, left, right}
`)
}

func TestAnalyzeSequenceRewriteExpr(t *testing.T) {
	analyzeTreeTestSource(t, "sequence_rewrite_surface.llcontext", `def keep_non_zero(owner: mutable Arena&, items: dview[u32]) -> darray[u32]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return rewrite items as sequence[u32]:
				item when item != 0u32:
					emit item
`)
}

func TestAnalyzeTreeTargetSequenceRewriteExpr(t *testing.T) {
	analyzeTreeTestSource(t, "sequence_rewrite_tree_target_surface.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Name(name: u32)

def keep_positive_int_values(owner: mutable Arena&, items: dview[Lua.Expr]) -> darray[i64]:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return rewrite items as sequence[i64]:
				Lua.Expr.Int(expr) when expr.value > 0:
					emit expr.value
`)
}

func TestAnalyzeTreeRewriteRemainsExhaustiveWithoutImplicitDefault(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_rewrite_requires_exhaustive.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Binary(expr, left, right):
				default{span = expr.span, left, right}
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `non-exhaustive rewrite over Lua.Expr; missing Lua.Expr.Int`) {
		t.Fatalf("expected non-exhaustive rewrite diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeTreeRewriteDefaultRequiresExactArm(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_rewrite_default_requires_exact.llcontext", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(child left: Expr, child right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Node:
			_:
				default
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `default is only allowed inside an exact tree rewrite arm`) {
		t.Fatalf("expected exact rewrite default diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeTreeVariantPayloadKindShadowsSyntheticKind(t *testing.T) {
	analyzeTreeTestSource(t, "tree_variant_payload_kind_shadow.llcontext", `tree Syntax:
	node Form:
		Atom(kind: bool)

def payload_kind(form: Syntax.Form) -> bool:
	return visit form as Syntax.Form:
		Syntax.Form.Atom(node):
			node.kind
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

func TestAnalyzeRejectsChildrenOverrideIncompatibleType(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_children_override_incompatible.llcontext", `tree Lua:
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
	for child in children(stmt to Lua.Expr):
		total <- total + child.name_index.i64()
	return total
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `children(...) override Lua.Expr is incompatible with structural child Lua.Block`) {
		t.Fatalf("expected incompatible children override diagnostic, got:\n%s", all)
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
