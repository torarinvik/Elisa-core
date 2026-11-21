package semantic

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

// Every bypass route of an `@append_only` store is rejected for its own reason; the same
// fixtures run through stage1 (test/repro/append_only_*).
func TestAppendOnlyStoreBypassRoutes(t *testing.T) {
	dir := filepath.Join("testdata", "append_only")
	expect, err := os.ReadFile(filepath.Join(dir, "EXPECT"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(expect)), "\n") {
		name, want, _ := strings.Cut(line, "\t")
		source, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
		if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, want) {
			t.Errorf("%s: want %q, got:\n%s", name, want, got)
		}
	}
}

func TestAppendOnlyStorePositive(t *testing.T) {
	// The store itself, and a struct holding one: moves and `&` borrows of the holder stay legal.
	for _, name := range []string{"append_only_store.pos.elisa", "append_only_holder.pos.elisa", "append_only_ternary_move.pos.elisa"} {
		source, err := os.ReadFile(filepath.Join("testdata", "append_only", name))
		if err != nil {
			t.Fatal(err)
		}
		result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
		for _, e := range result.Errors() {
			t.Errorf("%s rejected: %s", name, e)
		}
	}
}

// The direct expression cases must visit exactly the source children that the
// reflective walk would visit, including when the AST gains new fields.
func TestAppendOnlyExpressionFastPathsCoverSourceChildren(t *testing.T) {
	for _, node := range []any{
		ast.Ident{}, ast.IntLit{}, ast.FloatLit{}, ast.StringLit{}, ast.CharLit{},
		ast.BoolLit{}, ast.NullLit{}, ast.ZeroedLit{},
	} {
		if fields := appendOnlyChildFields(reflect.TypeOf(node)); len(fields) != 0 {
			t.Errorf("%T is no longer a leaf in the append-only walk", node)
		}
	}
	for _, item := range []struct {
		node     any
		children []string
	}{
		{ast.BinaryExpr{}, []string{"Left", "Right"}},
		{ast.UnaryExpr{}, []string{"Operand"}},
	} {
		nodeType := reflect.TypeOf(item.node)
		var names []string
		for _, index := range appendOnlyChildFields(nodeType) {
			names = append(names, nodeType.Field(index).Name)
		}
		if !reflect.DeepEqual(names, item.children) {
			t.Errorf("%s source children changed: got %v, want %v", nodeType, names, item.children)
		}
	}
}
