package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeCloneBuiltinSupportsDArrayAndTreeTargets(t *testing.T) {
	analyzeTreeTestSource(t, "clone_builtin_surface.elisa", `tree Lua:
	common:
		span: i64
	@role(expr)
	node Expr:
		Int(value: i64)
	block Block:
		items: darray[Expr]

struct Pair:
	items: darray[u32]
	root: Lua.Block

def clone_pair(owner: mutable Arena&, source_items: dview[u32], block: Lua.Block) -> Pair:
	can Abort.Panic, Memory.Allocate:
		in owner:
			return Pair{items: clone[darray[u32]](source_items), root: clone[Lua.Block](block)}
`)
}

func TestAnalyzeCloneBuiltinRejectsAllocatingCloneWithoutOwner(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "clone_builtin_owner_required.elisa", `def clone_items(items: dview[u32]) -> darray[u32]:
	return clone[darray[u32]](items)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `clone of "darray[u32]" requires an active in <owner>: scope`) {
		t.Fatalf("expected clone owner diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeCloneBuiltinSupportsStructsWithSViewFields(t *testing.T) {
	analyzeTreeTestSource(t, "clone_builtin_sview_struct.elisa", `struct NameBox:
	name: sview

def clone_name_box(box: NameBox) -> NameBox:
	return clone[NameBox](box)
`)
}

func TestAnalyzeCloneBuiltinRejectsReferences(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "clone_builtin_rejects_refs.elisa", `def clone_ref(value: i64&) -> i64&:
	return clone[i64&](value)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `clone cannot clone i64& into i64& in v1`) {
		t.Fatalf("expected clone ref rejection, got:\n%s", all)
	}
}
