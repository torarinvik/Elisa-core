//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersCloneBuiltinForDArrayAndTree(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)
	block Block:
		items: darray[Expr]

def clone_items(owner: mutable Arena&, items: view[u32], block: Lua.Block) -> Lua.Block:
	can Abort.Panic, Memory.Allocate:
		in owner:
			copied: darray[u32] = clone[darray[u32]](items)
			_ = copied.count
			return clone[Lua.Block](block)
`
	result := parseAndAnalyzeBackendTest(t, "backend_clone_builtin.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Lua__TreeHandle @clone_items", "clone.alloc", "define private %Lua__TreeHandle @tree_fold_", "node.default.arm"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected clone lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersCloneBuiltinForTreeCategoryWithCrossCategoryChildren(t *testing.T) {
	src := `@layout(per_variant_rows)
tree Syntax:
	common:
		span: i64
	@role(form)
	node Form:
		Atom(text: sview)
		Syntax(raw: Sequence)
	block Sequence:
		items: darray[Form]

def clone_form(owner: mutable Arena&, form: Syntax.Form) -> Syntax.Form:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return clone[Syntax.Form](form)
`
	result := parseAndAnalyzeBackendTest(t, "backend_clone_category_children.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"define %Syntax__TreeHandle @clone_form", "define private %Syntax__TreeHandle @tree_fold_", "node.default.arm"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category clone lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersCloneBuiltinForCategoryUnionTree(t *testing.T) {
	src := `@layout(category_union)
tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)

def clone_expr(owner: mutable Arena&, expr: Lua.Expr) -> Lua.Expr:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return clone[Lua.Expr](expr)
`
	result := parseAndAnalyzeBackendTest(t, "backend_clone_category_union.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{
		"define i32 @clone_expr",
		"ret i32 %expr",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected category_union clone lowering to include %q, got:\n%s", check, output)
		}
	}
	for _, unexpected := range []string{"define private i32 @tree_fold_", "%Lua_Expr_Binary__TreeTable"} {
		if strings.Contains(output, unexpected) {
			t.Fatalf("expected category_union clone lowering to avoid %q, got:\n%s", unexpected, output)
		}
	}
}
