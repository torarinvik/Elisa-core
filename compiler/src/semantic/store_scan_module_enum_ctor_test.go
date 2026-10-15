package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// An enum variant constructor in a module-scoped function builds a fresh value and writes none
// of its payloads. Scanned as a callee from another namespace, the constructor's enum name must
// still resolve in the function's own module, or it reads as an unresolved call that may write
// every argument it is given.
const storeScanModuleEnumCtorSource = `module Tree:
    enum Node:
        Leaf(value: i64)
        Group(kids: darray[i64])

    def count_of(kids: darray[i64]&) -> i64:
        can Memory.Allocate, Abort.Panic:
            group: Node = Node.Group(kids)
            return kids.count.i64()
`

func TestStoreScanModuleEnumConstructorIsNoWrite(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "store_scan_module_enum_ctor.elisa", storeScanModuleEnumCtorSource, AnalyzeOptions{})
	a := result.analyzer
	var fn *ast.FuncDecl
	for _, d := range result.File.Decls {
		if m, ok := d.(*ast.NamespaceDecl); ok && m != nil {
			for _, inner := range m.Decls {
				if f, isFn := inner.(*ast.FuncDecl); isFn && f.Name == "count_of" {
					fn = f
				}
			}
		}
	}
	if fn == nil {
		t.Fatalf("count_of not found")
	}
	a.withResolutionContext("", nil, func() {
		if a.storeScanParamMayBeWritten(fn, 0) {
			t.Fatalf("Node.Group(kids) counted as a write of kids")
		}
	})
}
