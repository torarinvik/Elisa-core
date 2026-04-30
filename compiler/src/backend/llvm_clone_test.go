//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersCloneBuiltinForDArrayAndTree(t *testing.T) {
	src := `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
		Binary(left: Expr, right: Expr)
	block Block:
		items: darray[Expr]

def clone_items(owner: mutable Arena&, items: dview[u32], block: Lua.Block) -> Lua.Block:
	can Abort.Panic, Memory.Allocate:
		in owner:
			copied: darray[u32] = clone[darray[u32]](items)
			_ = copied.count
			return clone[Lua.Block](block)
`
	result := parseAndAnalyzeBackendTest(t, "backend_clone_builtin.llcontext", src)
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
	src := `tree Syntax:
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
	result := parseAndAnalyzeBackendTest(t, "backend_clone_category_children.llcontext", src)
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
