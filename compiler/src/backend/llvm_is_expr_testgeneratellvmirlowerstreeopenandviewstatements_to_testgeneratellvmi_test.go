//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersTreeOpenAndViewStatements(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
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
	if node as Lua.Expr.Binary:
		binary: treeview[Lua.Expr.Binary] = node
		kept: treeview[Lua.Expr.Binary] = keep_binary(binary)
		return kept.left.span + binary.right.span + node.left.span
	return node.span

def left_value(node: Lua.Expr) -> i64:
	if node as Lua.Expr.Binary(Lua.Expr.Int(value), rhs):
		return value + rhs.span
	return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_open_view.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @child_span(%Lua__TreeHandle ", "define i64 @left_value(%Lua__TreeHandle ", "call %Lua__TreeHandle @keep_binary(%Lua__TreeHandle ", "match.tree.tag", "%Lua_Expr_Binary__TreeTable = type { i64, i64, ptr }", "%Lua_Expr_Binary__TreeRow = type", "tree.field.rows.ptr", "tree.field.row.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree open/view lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "TreeView__") || strings.Contains(output, "treeview.handle") {
		t.Fatalf("expected tree if-pattern lowering to keep treeview as the existing tree handle carrier, got:\n%s", output)
	}
	if strings.Contains(output, "tree.field.column.ptr") {
		t.Fatalf("expected tree open/view lowering to use row arrays, got old column pointer IR:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersCategoryUnionTreeConstructor(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)

def make_int(value: i64) -> Lua.Expr:
	in perm:
		return Lua.Expr.Int(span: 1, value: value)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_ctor.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Lua_Expr__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"tree.category.kind.ptr",
		"tree.category.payload.memcpy",
		"tree.category.count.ptr",
		"define i32 @make_int",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union constructor lowering to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "%Lua__TreeHandle = type") {
		t.Fatalf("expected category_union constructor to lower handles as dense i32 rows, got legacy handle carrier:\n%s", output)
	}
	if strings.Contains(output, "active_tree_store") {
		t.Fatalf("expected category_union constructor to avoid hidden active store globals, got:\n%s", output)
	}
}

func TestGenerateLLVMIRDefaultsTreeLayoutToCategoryUnion(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)

def make_int(value: i64) -> Lua.Expr:
	in perm:
		return Lua.Expr.Int(span: 1, value: value)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_default_category_union.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Lua_Expr__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"define i32 @make_int",
		"tree.category.kind.ptr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected default tree layout to lower as category_union and contain %q, got:\n%s", check, output)
		}
	}
	for _, oldShape := range []string{"%Lua__TreeHandle = type", "active_tree_store", "tree.root.coerce"} {
		if strings.Contains(output, oldShape) {
			t.Fatalf("expected default category-local tree lowering to avoid %q, got:\n%s", oldShape, output)
		}
	}
}

func TestGenerateLLVMIRAcceptsAoSAndSoATreeLayoutsAsDenseHandles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layout string
	}{
		{name: "aos", layout: "aos"},
		{name: "soa", layout: "soa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `tree Lua:
	@layout(` + tc.layout + `)
	@role(expr)
	node Expr:
		Int(value: i64)

def make_int(value: i64) -> Lua.Expr:
	in perm:
		return Lua.Expr.Int(value: value)
`
			result := parseAndAnalyzeBackendTest(t, "backend_tree_"+tc.name+"_dense_layout.elisa", src)
			output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
			if err != nil {
				t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
			}
			for _, check := range []string{
				"%Lua_Expr__TreeUnionTable = type { i64, i64, ptr, ptr }",
				"define i32 @make_int",
				"tree.category.kind.ptr",
			} {
				if !strings.Contains(output, check) {
					t.Fatalf("expected %s tree layout to use dense table lowering and contain %q, got:\n%s", tc.layout, check, output)
				}
			}
			if strings.Contains(output, "%Lua__TreeHandle = type") || strings.Contains(output, "active_tree_store") {
				t.Fatalf("expected %s layout to keep dense i32 handles without active-store globals, got:\n%s", tc.layout, output)
			}
		})
	}
}

func TestGenerateLLVMIRLowersTreeStoreFreezeAsFrozenStoreValue(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	@index(kind)
	@role(expr)
	@index(value)
	node Expr:
		Int(value: i64)

def build(owner: Arena) -> i64:
	store = Lua.Store(owner)
	in store:
		_ = Lua.Expr.Int(value: 7)
	frozen = freeze(move store)
	_ = frozen
	return 7
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_store_freeze.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define i64 @build",
		"ret i64 7",
		"%Lua_Expr__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"%Lua_Expr__TreeFrozenColumns = type { ptr, ptr }",
		"%Lua_Expr__TreeFrozenIndexes = type { ptr, ptr }",
		"tree.freeze.Lua_Expr.tags.alloc",
		"tree.freeze.Lua_Expr.tags.memcpy",
		"tree.freeze.Lua_Expr.columns.tags.ptr",
		"tree.freeze.Lua_Expr.indexes.field.ptr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree store freeze lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersTreeTagsFrozenColumnView(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	@index(kind)
	@role(expr)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def tag_score(tag: u32) -> i64:
	return tag.i64()

def build(owner: Arena) -> i64:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(value: 1)
		right = Lua.Expr.Int(value: 2)
		_ = Lua.Expr.Add(left: left, right: right)
	frozen = freeze(move store)
	tags: dview[u32] = frozen.Expr.column("kind")
	return reduce_sum(tags, tag_score)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_tags_column_view.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"tree.column.columns.tags",
		"tree.column.view.data",
		"tree.column.view.len",
		"reduce_sum.src.ptr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected canonical frozen tree column lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFrozenTreeCommonFieldColumnView(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def add_i64(value: i64) -> i64:
	return value

def build(owner: Arena) -> i64:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: 10, value: 1)
		right = Lua.Expr.Int(span: 20, value: 2)
		_ = Lua.Expr.Add(span: 30, left: left, right: right)
	frozen = freeze(move store)
	spans: dview[i64] = frozen.Expr.column("span")
	return reduce_sum(spans, add_i64)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_common_field_column_view.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"tree.freeze.Lua_Expr.span.alloc",
		"tree.freeze.Lua_Expr.span.cond",
		"tree.freeze.Lua_Expr.span.Int",
		"tree.freeze.Lua_Expr.span.Add",
		"tree.column.columns.field",
		"tree.column.view.data",
		"reduce_sum.src.ptr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree_column lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFrozenTreeCategoryRowViewQuery(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def build(owner: Arena) -> usize:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: 10, value: 1)
		right = Lua.Expr.Int(span: 20, value: 2)
		_ = Lua.Expr.Add(span: 30, left: left, right: right)
	frozen = freeze(move store)
	return count node in frozen.Expr where node.kind == .Int and node.span > 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_tree_rows_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"tree.rows.state",
		"tree.field",
		"query.result",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected frozen tree row-view query lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFrozenTreeIndexedCommonFieldColumnView(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	@index(span)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def add_i64(value: i64) -> i64:
	return value

def build(owner: Arena) -> i64:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: 10, value: 1)
		right = Lua.Expr.Int(span: 20, value: 2)
		_ = Lua.Expr.Add(span: 30, left: left, right: right)
	frozen = freeze(move store)
	spans: dview[i64] = frozen.Expr.column("span")
	return reduce_sum(spans, add_i64)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_indexed_common_field_column_view.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Lua_Expr__TreeFrozenIndexes = type { ptr }",
		"tree.freeze.Lua_Expr.index.span.alloc",
		"tree.freeze.Lua_Expr.indexes.field.ptr",
		"tree.column.indexes.field",
		"tree.column.view.data",
		"reduce_sum.src.ptr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected indexed common field column lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFrozenTreeWhereKindRowViewQuery(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def build(owner: Arena) -> usize:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: 10, value: 1)
		right = Lua.Expr.Int(span: 20, value: 2)
		_ = Lua.Expr.Add(span: 30, left: left, right: right)
	frozen = freeze(move store)
	return count node in frozen.Expr.where_kind(.Int) where node.span > 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_tree_where_kind_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"where_kind.tag.insert",
		"where_kind.cmp",
		"where_kind.filter.body",
		"tree.rows.state",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected frozen tree where_kind lowering to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "where.predicate") {
		t.Fatalf("expected where_kind lowering to avoid predicate function calls, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersFrozenTreeFieldEqualityWhereQuery(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def build(owner: Arena, target: i64) -> usize:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: 10, value: 1)
		right = Lua.Expr.Int(span: 20, value: 2)
		_ = Lua.Expr.Add(span: target, left: left, right: right)
	frozen = freeze(move store)
	return count node in frozen.Expr where span == target
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_tree_field_equality_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"iter.filter.field.cmp",
		"iter.filter.body",
		"iter.filter.field.field",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected frozen tree field equality query lowering to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "where.predicate") {
		t.Fatalf("expected field equality query lowering to avoid predicate function calls, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersCompoundFrozenTreeFieldWhereQuery(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def build(owner: Arena, target: i64) -> usize:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: target, value: 1)
		right = Lua.Expr.Int(span: target, value: 2)
		_ = Lua.Expr.Add(span: target, left: left, right: right)
	frozen = freeze(move store)
	return count node in frozen.Expr where kind == .Int and span == target
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_tree_compound_field_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"iter.filter.and.rhs",
		"iter.filter.and.left.field.cmp",
		"iter.filter.and.right.field.cmp",
		"iter.filter.and.right.field.field",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected compound frozen tree query lowering to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "where.predicate") || strings.Contains(output, "runtime") {
		t.Fatalf("expected compound field query lowering to avoid predicate calls, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersFirstFrozenTreeFieldWhereQuery(t *testing.T) {
	src := `@layout(soa)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def build(owner: Arena, target: i64) -> Lua.Expr?:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: 10, value: 1)
		right = Lua.Expr.Int(span: target, value: 2)
		_ = Lua.Expr.Add(span: target, left: left, right: right)
	frozen = freeze(move store)
	return first node in frozen.Expr where span == target
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_tree_first_field_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"iter.filter.and.rhs",
		"iter.filter.and.right.field.cmp",
		"iter.filter.and.right.field.field",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected first frozen tree query lowering to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "where.predicate") {
		t.Fatalf("expected first field query lowering to avoid predicate calls, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersIndexedFrozenTreeFieldWhereQuery(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	@index(span)
	node Expr:
		Int(value: i64)
		Add(left: Lua.Expr, right: Lua.Expr)

def build(owner: Arena, target: i64) -> usize:
	store = Lua.Store(owner)
	in store:
		left = Lua.Expr.Int(span: 10, value: 1)
		right = Lua.Expr.Int(span: target, value: 2)
		_ = Lua.Expr.Add(span: target, left: left, right: right)
	frozen = freeze(move store)
	return count node in frozen.Expr where span == target
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_tree_indexed_field_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"tree.freeze.Lua_Expr.index.span.alloc",
		"iter.filter.field.indexes.field",
		"iter.filter.field.cmp",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected indexed frozen tree field query lowering to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "where.predicate") {
		t.Fatalf("expected indexed field query lowering to avoid predicate calls, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersIndexedFrozenTreePayloadFieldWhereQuery(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	@index(name_index)
	node Expr:
		Int(value: i64)
		Name(name_index: u32)
		Field(base: Lua.Expr, name_index: u32)

def build(owner: Arena, target: u32) -> usize:
	store = Lua.Store(owner)
	in store:
		base = Lua.Expr.Int(span: 1, value: 7)
		_ = Lua.Expr.Name(span: 2, name_index: target)
		_ = Lua.Expr.Field(span: 3, base: base, name_index: target)
	frozen = freeze(move store)
	return count node in frozen.Expr where kind == .Name and name_index == target
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_tree_indexed_payload_field_query.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"tree.freeze.Lua_Expr.index.name_index.alloc",
		"tree.freeze.Lua_Expr.index.name_index.Name",
		"tree.freeze.Lua_Expr.index.name_index.Field",
		"iter.filter.and.rhs",
		"iter.filter.and.right.field.indexes.field",
		"iter.filter.and.right.field.cmp",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected indexed frozen tree payload query lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRUsesExplicitPermStoreForImplicitTreeStoreCalls(t *testing.T) {
	src := `tree Lua:
	@role(expr)
	node Expr:
		Int(value: i64)

def make_local(owner: mutable Arena&, value: i64) -> Lua.Expr:
	in owner:
		return Lua.Expr.Int(value: value)

def make_persistent(owner: mutable Arena&, value: i64) -> Lua.Expr:
	in perm:
		return make_local(owner, value)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_implicit_store_perm_precedence.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@Lua__perm_tree_store") {
		t.Fatalf("expected perm tree store global in lowering, got:\n%s", output)
	}
	if !strings.Contains(output, "call i32 @make_local(") || !strings.Contains(output, "%tree.perm.store.value") {
		t.Fatalf("expected make_persistent to pass perm store value to make_local, got:\n%s", output)
	}
	if strings.Contains(output, "active_tree_store") {
		t.Fatalf("expected dense implicit store calls to avoid active_tree_store, got:\n%s", output)
	}
}

func TestGenerateLLVMIRMaterializesCategoryUnionRootOnlyForRootType(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)

def make_node(value: i64) -> Lua.Node:
	in perm:
		return Lua.Expr.Int(span: 1, value: value)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_root_materialization.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define i32 @make_node",
		"tree.root.coerce.root.table",
		"tree.root.coerce.payload.handle",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected root-typed tree lowering to materialize root row containing %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "%Lua__TreeHandle = type") || strings.Contains(output, "active_tree_store") {
		t.Fatalf("expected default tree layout to use dense handles without active store, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersCategoryUnionTreeFieldReads(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)

def score_int(view_node: Lua.Expr.Int) -> i64:
	in perm:
		return view_node.value + view_node.span

def score(node: Lua.Expr) -> i64:
	in perm:
		if node is Lua.Expr.Int:
			return node.value + node.span
		return node.span

def payload_score(node: Lua.Expr) -> i64:
	in perm:
		if node is Lua.Expr.Int(value):
			return value
		return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_field_read.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Lua_Expr__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"define i64 @score_int(i32 ",
		"define i64 @score(i32 ",
		"define i64 @payload_score(i32 ",
		"tree.field.payload.row.ptr",
		"tree.field.payload.value",
		"tree.payload.field.payload.value",
		"match.tree.tag",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union field read lowering to contain %q, got:\n%s", check, output)
		}
	}
	for _, oldShape := range []string{
		"%Lua_Expr_Int__TreeTable",
		"tree.field.rows.ptr",
		"tree.field.row.ptr",
	} {
		if strings.Contains(output, oldShape) {
			t.Fatalf("expected category_union field reads to avoid exact per-variant row lowering %q, got:\n%s", oldShape, output)
		}
	}
}

func TestGenerateLLVMIRPredeclaresCategoryUnionTreeTables(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)
		node Atom:
			Name(id: i64)
	@role(stmt)
	node Stmt:
		Return(value: Expr)

def build_store() -> void:
	region scratch(256u)
	store = Lua.Store(scratch)
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_table_shell.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Lua__RootUnionTable = type { i64, i64, ptr, ptr }",
		"%Lua_Expr__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"%Lua_Expr_Atom__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"%Lua_Stmt__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"%Lua__TreeStoreState = type { %Lua__RootUnionTable, %Lua_Expr__TreeUnionTable, %Lua_Expr_Atom__TreeUnionTable, %Lua_Stmt__TreeUnionTable }",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union type shell to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeChildrenLoops(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Unary(op: i32, expr: Expr)
		Binary(op: i32, left: Expr, right: Expr)
		Call(callee: Expr, args: darray[Expr], link source_expr: Expr)

def count_nodes(node: Lua.Expr) -> i64:
	in perm:
		total: mutable i64 = 1
		for child in children(node):
			total <- total + child.kind.i64()
		return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_children.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"TreeChildren", "tree.children.node.insert", "tree.children.count.phi", "tree.children.value.phi"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree children lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersCategoryUnionTreeChildrenLoops(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Unary(expr: Expr)
		Binary(left: Expr, right: Expr)

def count_nodes(node: Lua.Expr) -> i64:
	in perm:
		total: mutable i64 = 1
		for child in children(node):
			total <- total + child.kind.i64()
		return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_children.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"TreeChildren",
		"tree.children.count.phi",
		"tree.children.value.phi",
		"tree.payload.field.payload.row.ptr",
		"tree.payload.field.payload.value",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union children lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "%Lua_Expr_Binary__TreeTable") || strings.Contains(output, "tree.children.rows.ptr") {
		t.Fatalf("expected category_union children lowering to avoid exact per-variant rows, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersCategoryUnionTreeUpdateExpr(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	in perm:
		return node{left, right}
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_update.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define i32 @rewrite_binary",
		"tree.update.kind.ptr",
		"tree.update.src.payload.value",
		"tree.update.payload.memcpy",
		"tree.update.count.ptr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union update lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "%Lua_Expr_Binary__TreeTable") || strings.Contains(output, "tree.update.rows.ptr") {
		t.Fatalf("expected category_union update lowering to avoid exact per-variant rows, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersCategoryUnionMixedTreeChildrenToRootLoops(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		IfStmt(condition: Lua.Expr, body: Lua.Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Lua.Stmt]

def count_children(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	in perm:
		for child in children(stmt as Lua.Node):
			total <- total + child.kind.i64()
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_category_union_children_root.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"%Lua__RootUnionTable", "tree.root.coerce.root.table", "tree.children.value.phi"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union root children lowering to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersNestedCategoryUnionTreeFields(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		node Atom:
			Name(id: i64)
			String(id: i64)
		Binary(left: Expr, right: Expr)

def atom_id(atom: Lua.Expr.Atom) -> i64:
	in perm:
		if atom is Lua.Expr.Atom.Name:
			return atom.id + atom.span
		return atom.span

def name_id(name: Lua.Expr.Atom.Name) -> i64:
	in perm:
		return name.id + name.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_nested_category_union_fields.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"%Lua_Expr__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"%Lua_Expr_Atom__TreeUnionTable = type { i64, i64, ptr, ptr }",
		"define i64 @atom_id(i32 ",
		"define i64 @name_id(i32 ",
		"tree.field.payload.row.ptr",
		"tree.field.payload.value",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected nested category_union field lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "%Lua_Expr_Atom_Name__TreeTable") {
		t.Fatalf("expected nested category_union fields to avoid exact per-variant rows, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersEnumerateTupleLoops(t *testing.T) {
	src := `def sum_pairs(items: darray[usize]) -> usize:
	total: mutable usize = 0
	for index, value in items.enumerate():
		total <- total + index + value
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_enumerate_tuple_loop.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @sum_pairs(", "enumerate.source.insert", "enumerate.item.index.insert", "enumerate.item.value.insert", "iter.tuple.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected enumerate tuple loop lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersMethodEnumerateTupleLoops(t *testing.T) {
	src := `def sum_pairs(items: darray[usize]) -> usize:
	total: mutable usize = 0
	for index, value in items.enumerate():
		total <- total + index + value
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_method_enumerate_tuple_loop.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @sum_pairs(", "enumerate.source.insert", "enumerate.item.index.insert", "enumerate.item.value.insert", "iter.tuple.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected method enumerate tuple loop lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersWhereFilteredViewLoops(t *testing.T) {
	src := `def keep_large(value: i64) -> bool:
	return value > 2

def sum_filtered(items: i64[4]) -> i64:
	total: mutable i64 = 0
	for value in items where keep_large:
		total <- total + value
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_where_filtered_view_loop.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"where.source.insert", "where.predicate.insert", "where.predicate", "where.filter.body"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected where filtered view lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersInlineWhereFilterLoops(t *testing.T) {
	src := `def sum_filtered(items: i64[4]) -> i64:
	total: mutable i64 = 0
	for value in items where value > 2:
		total <- total + value
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_inline_where_filter_loop.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"iter.where.filter.body", "icmp sgt", "define i64 @sum_filtered("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected inline where filter lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersInlineWhereOverEnumerateLoops(t *testing.T) {
	src := `def sum_even_indexed(items: i64[4]) -> i64:
	total: mutable i64 = 0
	for index, value in items.enumerate() where index % 2 == 0:
		total <- total + value + index.i64()
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_inline_where_over_enumerate_loop.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"enumerate.item.index.insert", "enumerate.item.value.insert", "iter.where.filter.body", "iter.tuple.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected inline where-over-enumerate lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersInlineWhereAfterEnumerateLoops(t *testing.T) {
	src := `def sum_indexed_filtered(items: i64[4]) -> i64:
	total: mutable i64 = 0
	for index, value in items.enumerate() where value > 2:
		total <- total + value + index.i64()
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_inline_where_after_enumerate_loop.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"iter.where.filter.body", "enumerate.item.index.insert", "enumerate.item.value.insert", "iter.tuple.field"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected inline where-after-enumerate lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersBoolAggregatesWithRegularWhereFilters(t *testing.T) {
	src := `def keep_true(value: bool) -> bool:
	return value

def has_selected_truth(values: bool[4]) -> bool:
	return (values where value: keep_true(value)).any()

def all_selected_truth(values: bool[4]) -> bool:
	return all((values where value: keep_true(value)))
`
	result := parseAndAnalyzeBackendTest(t, "backend_bool_aggregates_expression_where.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @has_selected_truth(", "define i1 @all_selected_truth(", "where.source.insert", "where.predicate.insert", "where.filter.body", "call i1 @keep_true", "any.short_circuit", "all.short_circuit"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected bool aggregate with regular where lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersExpressionWherePatternFilters(t *testing.T) {
	src := `enum Expr:
	Int(value: i64)
	None

def count_positive(items: Expr[4]) -> usize:
	total: mutable usize = 0
	for item in (items where Expr.Int(value): value > 0):
		total <- total + 1
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_expression_where_pattern_filter.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"where.source.insert", "where.predicate.insert", "where.filter.body", "match.tag", "icmp sgt"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected expression where pattern filter lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersMixedTreeChildrenToRootLoops(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		IfStmt(condition: Lua.Expr, body: Lua.Block)
	@role(expr)
	node Expr:
		Name(name_index: u32)
	block Block:
		stmts: darray[Lua.Stmt]

def count_children(stmt: Lua.Stmt) -> i64:
	total: mutable i64 = 0
	for child in children(stmt as Lua.Node):
		total <- total + child.kind.i64()
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_children_root.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @count_children(%Lua__TreeHandle ", "TreeChildren", "tree.children.value.phi", "tree.field.kind.tag.trunc"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected mixed tree children lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeSequenceFieldViews(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		Return(value: Expr)
		ElseIf(condition: Expr, body: Block)
		IfStmt(condition: Expr, then_block: Block, elseifs: darray[Stmt], has_else: bool, else_block: Block)
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
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_sequence_fields.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @block_total(%Lua__TreeHandle ", "define i64 @elseif_total(%Lua__TreeHandle ", "DynArrayView", "tree.field.surface.view.len", "tree.field.surface.view.elem_size", "iter.len.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree sequence field lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersOptionalTreeChildFields(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		ElseIf(condition: Expr, body: Block)
		IfStmt(condition: Expr, then_block: Block, elseifs: darray[Stmt], else_block?: Block)
		NumericFor(name_index: u32, start: Expr, limit: Expr, step?: Expr, body: Block)
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
	for child in children(stmt as Lua.Node):
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
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_optional_child_fields.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @has_else(%Lua__TreeHandle ", "define i64 @count_children(%Lua__TreeHandle ", "define i64 @score(%Lua__TreeHandle ", "Optional__Lua_Block", "Optional__Lua_Expr", "Optional__i64", "optional.present", "fold.arm.named.else_block.value", "fold.arm.named.step.value", "fold.arm.child.edge.count"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected optional tree child lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeVisitExpr(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def score(node: Lua.Expr) -> i64:
	return visit node:
		Lua.Expr.Nil(expr):
			expr.span
		Lua.Expr.Int(expr):
			expr.value
		Lua.Expr.Binary(expr):
			expr.left.span + expr.right.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_visit_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(%Lua__TreeHandle ", "visit.expr.arm", "visit.expr.phi", "match.tree.tag", "%Lua_Expr_Binary__TreeRow = type", "tree.field.rows.ptr", "tree.field.row.ptr"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree visit lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersGuardedTreeVisitExpr(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(stmt)
	node Stmt:
		ExprStmt(expr: Expr)
	@role(expr)
	node Expr:
		Name(name_index: u32)
		Call(callee: Expr)
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def score_expr(node: Lua.Expr) -> i64:
	return visit node:
		Lua.Expr.Int(expr) when expr.value > 0:
			expr.value
		_:
			0

def score_node(node: Lua.Node) -> i64:
	return visit node as Lua.Node:
		Lua.Stmt.ExprStmt(stmt) when stmt.expr.kind == .Call:
			stmt.span + 1
		Lua.Stmt.ExprStmt(stmt) when stmt.expr.kind == .Name:
			stmt.span + 2
		_:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_visit_guard.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score_expr(%Lua__TreeHandle ", "define i64 @score_node(%Lua__TreeHandle ", "visit.expr.guard.body", "visit.node.exact.guard.body", "visit.node.exact.phi"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected guarded visit lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeKindFieldAndShorthandMembers(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
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

def is_binary(node: Lua.Expr) -> bool:
	return node.kind == .Binary

def stmt_is_return(stmt: Lua.Stmt) -> bool:
	return stmt.kind == .Return

def binary_kind(node: Lua.Expr.Binary) -> Lua.Expr.Kind:
	return node.kind

def node_kind(node: Lua.Node) -> Lua.Node.Kind:
	return node.kind

def node_is_binary(node: Lua.Node) -> bool:
	return node.kind == .Expr.Binary

def block_kind(node: Lua.Block) -> Lua.Node.Kind:
	return node.kind

def explicit_binary_kind() -> Lua.Expr.Kind:
	return Lua.Expr.Kind.Binary

def explicit_root_binary_kind() -> Lua.Node.Kind:
	return Lua.Node.Kind.Expr.Binary
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_kind.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @is_binary(%Lua__TreeHandle ", "define i1 @stmt_is_return(%Lua__TreeHandle ", "define i32 @binary_kind(%Lua__TreeHandle ", "define i32 @node_kind(%Lua__TreeHandle ", "define i1 @node_is_binary(%Lua__TreeHandle ", "define i32 @block_kind(%Lua__TreeHandle ", "define i32 @explicit_binary_kind()", "define i32 @explicit_root_binary_kind()", "icmp eq i32"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree kind lowering to include %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "tree.field.column.ptr") {
		t.Fatalf("expected tree kind lowering to avoid tree field column loads, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersExactTreeVisitExpr(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	@role(stmt)
	node Stmt:
		ExprStmt(expr: Expr)
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		stmts: darray[Stmt]

def stmt_total(block: Lua.Block) -> i64:
	return visit block:
		Lua.Block(node):
			node.stmts.len.i64()
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_visit_exact_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @stmt_total(%Lua__TreeHandle ", "visit.exact.arm", "visit.exact.end"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected exact tree visit lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeFoldExpr(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Nil
		Int(value: i64)
		Call(callee: Expr, args: darray[Expr])
		Binary(left: Expr, right: Expr)

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
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_fold_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @score(%Lua__TreeHandle ", "define private i64 @tree_fold_", "call i64 @tree_fold_", "fold.arm.buffer", "fold.arm.view.len", "fold.arm.named.args.sub.view.len", "fold.arm.named.left.value", "fold.arm.named.right.value"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree fold lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersDirectTreeAttributeReads(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
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
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_attribute_direct.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @checksum_of(%Lua__TreeHandle ", "define private i64 @tree_attr_", "tree.attr.call", "add i64"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected direct tree attribute lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersProjectedTreeAttributeReads(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
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
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_attribute_projected.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i64 @count_of(%Lua__TreeHandle ", "define private i64 @tree_attr_", "TreeAttributeSeq", "call i64 @tree_attr_"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected projected tree attribute lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeAttributeAggregateHelpers(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
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
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_attribute_aggregate_helpers.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define i1 @has_control(%Lua__TreeHandle ", "define i1 @all_children_leaf(%Lua__TreeHandle ", "any.cond", "all.cond", "TreeAttributeSeq", "call i1 @tree_attr_"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree attribute aggregate helper lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersTreeRewriteExpr(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def simplify(node: Lua.Expr) -> Lua.Expr:
	in perm:
		return rewrite node as Lua.Expr:
			Lua.Expr.Int(expr):
				default
			Lua.Expr.Binary(expr, left, right):
				default
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_rewrite_expr.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @simplify(%Lua__TreeHandle ", "define private %Lua__TreeHandle @tree_fold_", "call %Lua__TreeHandle @tree_fold_", "fold.arm.named.left.value", "fold.arm.named.right.value"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected tree rewrite lowering to include %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersExactTreeRecordUpdates(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def rewrite_binary(node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	in perm:
		return node{left, right}

def rewrite_binary_explicit(owner: Arena, node: Lua.Expr.Binary, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr.Binary:
	alloc: mutable Arena& = (&owner).cast[mutable Arena&]
	return new[alloc] node{left, right}
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_exact_update.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @rewrite_binary(%Lua__TreeHandle ", "define %Lua__TreeHandle @rewrite_binary_explicit(%Arena ", "tree.update.src.row", "tree.update.store.state", "load %Lua_Expr_Binary__TreeRow", "insertvalue %Lua_Expr_Binary__TreeRow", "store %Lua_Expr_Binary__TreeRow"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected exact tree update lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIREnsuresTreeRowLayoutMatchesSourceFieldOrder(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Binary(left: Expr, right: Expr)
		Call(callee: Expr, args: darray[Expr])
	block Block:
		stmts: darray[Expr]

def make_binary(span: i64, left: Lua.Expr, right: Lua.Expr) -> Lua.Expr:
	in perm:
		return Lua.Expr.Binary(span: span, left: left, right: right)

def make_call(span: i64, callee: Lua.Expr, args: darray[Lua.Expr]) -> Lua.Expr:
	in perm:
		return Lua.Expr.Call(span: span, callee: callee, args: args)

def make_block(span: i64, stmts: darray[Lua.Expr]) -> Lua.Block:
	in perm:
		return Lua.Block(span: span, stmts: stmts)

def left_span(node: Lua.Expr.Binary) -> i64:
	return node.left.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_tree_row_layout.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "%Lua_Expr_Binary__TreeRow = type { i64, %Lua__TreeHandle, %Lua__TreeHandle }") {
		t.Fatalf("expected Lua.Expr.Binary row layout to be { i64, TreeHandle, TreeHandle } matching field order span, left, right, got:\n%s", output)
	}
	if !strings.Contains(output, "%Lua_Expr_Call__TreeRow = type { i64, %Lua__TreeHandle, %DynArray__Lua_Expr }") {
		t.Fatalf("expected Lua.Expr.Call row layout to be { i64, TreeHandle, DynArray_Lua_Expr } matching field order span, callee, args, got:\n%s", output)
	}
	if !strings.Contains(output, "%Lua_Block__TreeRow = type { i64, %DynArray__Lua_Expr }") {
		t.Fatalf("expected Lua.Block row layout to be { i64, DynArray_Lua_Expr } matching field order span, stmts, got:\n%s", output)
	}
	for _, check := range []string{"insertvalue %Lua_Expr_Binary__TreeRow", "store %Lua_Expr_Binary__TreeRow", "insertvalue %Lua_Expr_Call__TreeRow", "store %Lua_Expr_Call__TreeRow", "insertvalue %Lua_Block__TreeRow", "store %Lua_Block__TreeRow"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected whole-row store lowering to include %q, got:\n%s", check, output)
		}
	}
}
