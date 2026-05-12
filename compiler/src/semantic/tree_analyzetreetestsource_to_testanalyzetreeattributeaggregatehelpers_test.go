package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
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
	result := analyzeTreeTestSource(t, "tree_registers.elisa", `tree Lua:
    common:
        @storage(side_table)
        span: i64
    @role(expr)
    node Expr:
        Nil
        Binary(op: i32, left: Expr, right: Expr)
    @role(stmt)
    node Stmt:
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

func TestAnalyzeTreeLayoutAnnotations(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_layout_annotations.elisa", `@layout(category_union)
@index(kind)
tree Lua:
    @role(expr)
    @layout(soa)
    @index(kind)
    @index(span)
    node Expr:
        Nil
        Binary(left: Expr, right: Expr)
        node Atom:
            Int(value: i64)
    @layout(per_variant_rows)
    node Stmt:
        Return(value: Expr)
`)

	family, ok := result.NamedTypes["Lua"].(*TreeType)
	if !ok {
		t.Fatalf("expected Lua tree type, got %T", result.NamedTypes["Lua"])
	}
	if family.Layout != TreeLayoutCategoryUnion || !family.LayoutExplicit {
		t.Fatalf("expected explicit category_union tree layout, got %s explicit=%v", family.Layout, family.LayoutExplicit)
	}
	if len(family.Indexes) != 1 || !family.Indexes[0].Kind {
		t.Fatalf("expected tree kind index metadata, got %#v", family.Indexes)
	}
	exprType, ok := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr tree category type, got %T", result.NamedTypes["Lua.Expr"])
	}
	if exprType.Layout != TreeLayoutSOA || !exprType.LayoutExplicit {
		t.Fatalf("expected Expr explicit soa layout, got %s explicit=%v", exprType.Layout, exprType.LayoutExplicit)
	}
	if len(exprType.Indexes) != 2 || !exprType.Indexes[0].Kind || exprType.Indexes[1].Name != "span" {
		t.Fatalf("expected Expr index metadata, got %#v", exprType.Indexes)
	}
	atomType, ok := result.NamedTypes["Lua.Expr.Atom"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr.Atom tree category type, got %T", result.NamedTypes["Lua.Expr.Atom"])
	}
	if atomType.Layout != TreeLayoutSOA || atomType.LayoutExplicit {
		t.Fatalf("expected nested Atom to inherit Expr soa layout, got %s explicit=%v", atomType.Layout, atomType.LayoutExplicit)
	}
	stmtType, ok := result.NamedTypes["Lua.Stmt"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Stmt tree category type, got %T", result.NamedTypes["Lua.Stmt"])
	}
	if stmtType.Layout != TreeLayoutPerVariantRows || !stmtType.LayoutExplicit {
		t.Fatalf("expected Stmt explicit per_variant_rows layout, got %s explicit=%v", stmtType.Layout, stmtType.LayoutExplicit)
	}
}

func TestAnalyzeTreeLayoutAnnotationsDefaultAndDiagnostics(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_layout_default.elisa", `tree Lua:
    @role(expr)
    node Expr:
        Nil
`)
	family, ok := result.NamedTypes["Lua"].(*TreeType)
	if !ok {
		t.Fatalf("expected Lua tree type, got %T", result.NamedTypes["Lua"])
	}
	if family.Layout != TreeLayoutCategoryUnion || family.LayoutExplicit {
		t.Fatalf("expected default category_union tree layout, got %s explicit=%v", family.Layout, family.LayoutExplicit)
	}
	exprType, ok := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr tree category type, got %T", result.NamedTypes["Lua.Expr"])
	}
	if exprType.Layout != TreeLayoutCategoryUnion || exprType.LayoutExplicit {
		t.Fatalf("expected Expr to inherit default category_union layout, got %s explicit=%v", exprType.Layout, exprType.LayoutExplicit)
	}

	bad := analyzeTreeTestSourceWithSemanticErrors(t, "tree_layout_bad.elisa", `@layout(banana)
@layout(category_union)
tree Bad:
    @layout
    @layout(per_variant_rows)
    node Broken:
        Nil
`)
	all := strings.Join(bad.Errors(), "\n")
	if !strings.Contains(all, `@layout on tree "Bad" uses unsupported layout "banana"`) {
		t.Fatalf("expected unsupported tree @layout diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, `duplicate @layout annotation on tree "Bad"`) {
		t.Fatalf("expected duplicate tree @layout diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, `@layout on tree node "Broken" expects exactly one layout argument`) {
		t.Fatalf("expected malformed node @layout diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, `duplicate @layout annotation on tree node "Broken"`) {
		t.Fatalf("expected duplicate node @layout diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeTreeFieldTemperatureAndIndexDiagnostics(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_layout_field_temperature.elisa", `tree Lua:
    common:
        @hot
        span: i64
    @layout(aos)
    node Expr:
        Nil
    struct Row:
        @cold
        payload: i64
`)
	family := result.NamedTypes["Lua"].(*TreeType)
	if got := family.Common["span"].TreeTemp; got != TreeFieldTemperatureHot {
		t.Fatalf("expected common span to be hot, got %#v", got)
	}
	exprType := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if exprType.Layout != TreeLayoutAOS {
		t.Fatalf("expected aos category layout, got %s", exprType.Layout)
	}
	rowType := result.NamedTypes["Lua.Row"].(*TreeStructType)
	if got := rowType.Fields["payload"].TreeTemp; got != TreeFieldTemperatureCold {
		t.Fatalf("expected tree struct payload to be cold, got %#v", got)
	}

	bad := analyzeTreeTestSourceWithSemanticErrors(t, "tree_layout_index_bad.elisa", `@index(kind)
@index(kind)
tree Bad:
    @index()
    node Expr:
        Nil
    struct Row:
        @hot(foo)
        value: i64
`)
	all := strings.Join(bad.Errors(), "\n")
	for _, want := range []string{
		`duplicate @index(kind) on tree "Bad"`,
		`@index on tree node "Expr" expects exactly one argument`,
		`@hot on tree struct field "value" does not take arguments`,
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected diagnostic %q, got:\n%s", want, all)
		}
	}
}

func TestAnalyzeFreezeAcceptsLocalTreeStores(t *testing.T) {
	analyzeTreeTestSource(t, "tree_freeze_store.elisa", `tree Lua:
    node Expr:
        Int(value: i64)

def build(owner: Arena) -> Lua.Store[Frozen]:
    store = Lua.Store(owner)
    frozen: Lua.Store[Frozen] = freeze(move store)
    return frozen
`)

	bad := analyzeTreeTestSourceWithSemanticErrors(t, "tree_freeze_requires_move.elisa", `tree Lua:
    node Expr:
        Int(value: i64)

def build(owner: Arena) -> Lua.Store[Frozen]:
    store = Lua.Store(owner)
    return freeze(store)
`)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "local tree store") || !strings.Contains(all, "must be moved explicitly before freeze") {
		t.Fatalf("expected tree store move diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeInfersTreePayloadRelations(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_inferred_payload_relations.elisa", `tree Lua:
    @role(expr)
    node Expr:
        Nil
        Unary(expr: Expr)
        Binary(left: Expr, right: Expr)
        Call(callee: Lua.Expr, args: darray[Lua.Expr])
`)

	exprType, ok := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr tree category type, got %T", result.NamedTypes["Lua.Expr"])
	}
	unary, ok := exprType.Variant("Unary")
	if !ok || unary.PayloadRelation(0) != ast.EnumPayloadRelationChild {
		t.Fatalf("expected Unary expr payload to infer child relation, got %#v", unary)
	}
	binary, ok := exprType.Variant("Binary")
	if !ok || binary.PayloadRelation(0) != ast.EnumPayloadRelationChild || binary.PayloadRelation(1) != ast.EnumPayloadRelationChild {
		t.Fatalf("expected Binary left/right payloads to infer child relations, got %#v", binary)
	}
	call, ok := exprType.Variant("Call")
	if !ok || call.PayloadRelation(0) != ast.EnumPayloadRelationChild || call.PayloadRelation(1) != ast.EnumPayloadRelationChildren {
		t.Fatalf("expected Call callee/args payloads to infer child/children relations, got %#v", call)
	}
	if len(result.Deprecations()) != 0 {
		t.Fatalf("expected inferred relations to avoid deprecations, got:\n%s", strings.Join(result.Deprecations(), "\n"))
	}
}
func TestAnalyzeRegistersNestedTreeCategories(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_nested_categories.elisa", `tree Lua:
    @role(expr)
    node Expr:
        Unary(expr: Expr)
        node Binary:
            Add(left: Lua.Expr, right: Lua.Expr)
            Sub(left: Lua.Expr, right: Lua.Expr)
`)

	family, ok := result.NamedTypes["Lua"].(*TreeType)
	if !ok {
		t.Fatalf("expected Lua tree type, got %T", result.NamedTypes["Lua"])
	}
	exprType, ok := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr tree category type, got %T", result.NamedTypes["Lua.Expr"])
	}
	binaryType, ok := result.NamedTypes["Lua.Expr.Binary"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr.Binary tree category type, got %T", result.NamedTypes["Lua.Expr.Binary"])
	}
	if binaryType.Parent != exprType {
		t.Fatalf("expected Lua.Expr.Binary parent to be Lua.Expr, got %#v", binaryType.Parent)
	}
	if member, ok := family.Member("Expr.Binary"); !ok || !SameType(member, binaryType) {
		t.Fatalf("expected family member lookup for Expr.Binary to resolve to Lua.Expr.Binary, got %#v", member)
	}
	if len(exprType.Variants) != 1 {
		t.Fatalf("expected Lua.Expr to keep only direct variants, got %d", len(exprType.Variants))
	}
	unary, ok := exprType.Variant("Unary")
	if !ok || unary.Tag != 0 {
		t.Fatalf("expected Lua.Expr.Unary tag 0, got %#v", unary)
	}
	add, ok := binaryType.Variant("Add")
	if !ok || add.Tag != 1 {
		t.Fatalf("expected Lua.Expr.Binary.Add tag 1, got %#v", add)
	}
	sub, ok := binaryType.Variant("Sub")
	if !ok || sub.Tag != 2 {
		t.Fatalf("expected Lua.Expr.Binary.Sub tag 2, got %#v", sub)
	}
	if add.PayloadRelation(0) != ast.EnumPayloadRelationChild || add.PayloadRelation(1) != ast.EnumPayloadRelationChild {
		t.Fatalf("expected nested Add operands to infer child relations, got %#v", add.PayloadRelations)
	}
}
func TestAnalyzeNestedTreeCategoryAssignableToParent(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_nested_category_assignability.elisa", `tree Lua:
    @role(expr)
    node Expr:
        Unary(expr: Expr)
        node Binary:
            Add(left: Lua.Expr, right: Lua.Expr)

def build(alloc: mutable Arena&, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
    return node[alloc = alloc] Lua.Expr.Binary.Add(left: left, right: right)

def widen(node: Lua.Expr.Binary) -> Lua.Expr:
    return node

def classify(node: Lua.Expr) -> i64:
    match node:
        Lua.Expr.Binary.Add(left: _, right: _):
            return 1
        Lua.Expr.Unary(_):
            return 2
        _:
            return 0
`)

	exprType, ok := result.NamedTypes["Lua.Expr"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr tree category type, got %T", result.NamedTypes["Lua.Expr"])
	}
	binaryType, ok := result.NamedTypes["Lua.Expr.Binary"].(*TreeCategoryType)
	if !ok {
		t.Fatalf("expected Lua.Expr.Binary tree category type, got %T", result.NamedTypes["Lua.Expr.Binary"])
	}
	if !AssignableTo(exprType, binaryType) {
		t.Fatalf("expected nested tree category Lua.Expr.Binary to be assignable to parent Lua.Expr")
	}
	if AssignableTo(binaryType, exprType) {
		t.Fatalf("expected parent tree category Lua.Expr not to be assignable to nested category Lua.Expr.Binary")
	}
	add, ok := binaryType.Variant("Add")
	if !ok {
		t.Fatalf("expected Lua.Expr.Binary.Add variant")
	}
	if !AssignableTo(exprType, binaryType.VariantViewType(add)) {
		t.Fatalf("expected nested tree variant view Lua.Expr.Binary.Add to be assignable to parent Lua.Expr")
	}
}
func TestAnalyzeSynthesizesTreeCategoryKindTypesAndShorthandComparisons(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_kind_types.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)
	@role(stmt)
	node Stmt:
		Return(value: Expr)
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
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_duplicate_member.elisa", `tree Lua:
    node Expr:
        Nil
    struct Expr:
        value: i64
`)
	errText := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errText, DuplicateTypeMessage("Lua.Expr")) {
		t.Fatalf("expected duplicate tree member type diagnostic, got:\n%s", errText)
	}
}
func TestAnalyzeTreeCategoryRoleDirectives(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_role_directives.elisa", `tree Lua:
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
	analyzeTreeTestSource(t, "tree_field_access.elisa", `tree Lua:
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

func TestAnalyzeRejectsTreeFieldMutationAndAddressTaking(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "tree_field_value_only.elisa", `tree Syntax:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)

def set_span(node: Syntax.Expr):
	node.span <- 2

def borrow_span(node: Syntax.Expr) -> i64&:
	return &node.span

def bind_span_ref(node: Syntax.Expr):
	node.span as & <- 3
`)
	all := strings.Join(result.Errors(), "\n")
	for _, want := range []string{
		`cannot assign directly to tree field "span"`,
		`cannot take address of tree field "span"`,
		`cannot bind tree field "span" as a reference`,
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("expected tree value-only field diagnostic %q, got:\n%s", want, all)
		}
	}
}

func TestAnalyzeTreeAttributeFieldAccess(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_attribute_field_access.elisa", `tree Lua:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

attribute Lua.Expr.checksum -> i64:
	Lua.Expr.Int(expr):
		return expr.value
	Lua.Expr.Binary(expr, left, right):
		return left.checksum + right.checksum

def checksum_of(node: Lua.Expr) -> i64:
	return node.checksum
`)

	if len(result.TreeAttributes) == 0 {
		t.Fatalf("expected registered tree attributes")
	}
	if len(result.AttributeFieldRefs) < 3 {
		t.Fatalf("expected attribute field refs for recursive and surface accesses, got %d", len(result.AttributeFieldRefs))
	}
	attrs, ok := result.TreeAttributes[TypeIdentityKey(result.NamedTypes["Lua.Expr"])]
	if !ok || attrs["checksum"] == nil {
		t.Fatalf("expected Lua.Expr.checksum attribute registration, got %#v", attrs)
	}
}
func TestAnalyzeProjectedTreeAttributeFieldAccess(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_attribute_projected_field_access.elisa", `tree Lua:
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

attribute Lua.Expr.node_count -> usize:
	Lua.Expr.Int(_):
		return 1u
	Lua.Expr.Binary(expr, left, right):
		total: mutable usize = 1u
		for child_count in children.node_count:
			total <- total + child_count
		return total

def count_of(node: Lua.Expr) -> usize:
	return node.node_count
`)

	if len(result.TreeAttributes) == 0 {
		t.Fatalf("expected registered tree attributes")
	}
	if len(result.AttributeFieldRefs) < 2 {
		t.Fatalf("expected projected and surface attribute field refs, got %d", len(result.AttributeFieldRefs))
	}
	attrs, ok := result.TreeAttributes[TypeIdentityKey(result.NamedTypes["Lua.Expr"])]
	if !ok || attrs["node_count"] == nil {
		t.Fatalf("expected Lua.Expr.node_count attribute registration, got %#v", attrs)
	}
	projectedCount := 0
	for expr := range result.AttributeFieldRefs {
		if expr != nil && expr.Field == "node_count" {
			projectedCount++
		}
	}
	if projectedCount < 2 {
		t.Fatalf("expected projected and direct node_count field expressions, got %d", projectedCount)
	}
}
func TestAnalyzeTreeAttributeAggregateHelpers(t *testing.T) {
	result := analyzeTreeTestSource(t, "tree_attribute_aggregate_helpers.elisa", `tree Lua:
	@role(expr)
	node Expr:
		Nil
		Binary(left: Expr, right: Expr)

attribute Lua.Expr.is_leaf -> bool:
	Lua.Expr.Nil(_):
		return true
	Lua.Expr.Binary(_):
		return false

attribute Lua.Expr.has_control_flow -> bool:
	Lua.Expr.Nil(_):
		return false
	Lua.Expr.Binary(expr, left, right):
		return any(children.has_control_flow)

def has_control(node: Lua.Expr) -> bool:
	return node.has_control_flow

def all_children_leaf(node: Lua.Expr) -> bool:
	return all(children(node).is_leaf)
`)

	if len(result.TreeAttributes) == 0 {
		t.Fatalf("expected registered tree attributes")
	}
	attrs, ok := result.TreeAttributes[TypeIdentityKey(result.NamedTypes["Lua.Expr"])]
	if !ok || attrs["has_control_flow"] == nil || attrs["is_leaf"] == nil {
		t.Fatalf("expected aggregate helper attribute registration, got %#v", attrs)
	}
	aggregateRefs := 0
	for expr := range result.AttributeFieldRefs {
		if expr == nil {
			continue
		}
		if expr.Field == "has_control_flow" || expr.Field == "is_leaf" {
			aggregateRefs++
		}
	}
	if aggregateRefs < 2 {
		t.Fatalf("expected aggregate helper projected attribute refs, got %d", aggregateRefs)
	}
}
