package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// A parameter's region (`sview @source`) is part of the signature. The retained
// analysis compares unparsed headers, so a header that dropped the region spliced
// the edit in as a body change and kept the old declaration's diagnostics.
func TestRetainedHeaderIncludesParameterRegion(t *testing.T) {
	header := func(src string) string {
		return retainedText(retainedParse(t, src).Decls[0].(*ast.FuncDecl)).header
	}
	plain := header("def f[@source](s: sview) -> void:\n    pass\n")
	pinned := header("def f[@source](s: sview @source) -> void:\n    pass\n")
	if plain == pinned {
		t.Fatalf("adding a parameter region left the header unchanged: %q", plain)
	}
}
