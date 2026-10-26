package semantic

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestNoStorageOverlayClosedAccessPolicy(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	allowed := map[string]map[string]bool{
		"analyzer_no_storage_overlay.go":   {"lookupStoreScanNoStorage": true, "setStoreScanNoStorage": true, "lookupStoreScanShares": true, "setStoreScanShares": true, "lookupCompilerBuiltinHelperCall": true, "setCompilerBuiltinHelperCall": true},
		"parallel_bodies.go":               {"cloneAnalyzerValueForParallelPolicy": true},
		"specialization_maps_generated.go": {"cloneSpecializationOpaqueMap": true},
		"shared_codegen.go":                {"BeginSharedCodegen": true},
		"optimization_result_api.go":       {"IsCompilerBuiltinHelperCall": true},
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok {
				continue
			}
			goast.Inspect(fn.Body, func(n goast.Node) bool {
				if selector, ok := n.(*goast.SelectorExpr); ok && (selector.Sel.Name == "storeScanNoStorageCache" || selector.Sel.Name == "storeScanSharesCache" || selector.Sel.Name == "compilerBuiltinHelperCalls") && !allowed[entry.Name()][fn.Name.Name] {
					t.Errorf("raw analyzer cache access bypasses overlay at %s", set.Position(n.Pos()))
				}
				if literal, ok := n.(*goast.BasicLit); ok && literal.Kind == token.STRING {
					value, _ := strconv.Unquote(literal.Value)
					if (value == "storeScanNoStorageCache" || value == "storeScanSharesCache" || value == "compilerBuiltinHelperCalls") && !allowed[entry.Name()][fn.Name.Name] {
						t.Errorf("named analyzer cache reflection needs review at %s", set.Position(n.Pos()))
					}
				}
				return true
			})
		}
	}
	policy, err := os.ReadFile(filepath.Join(dir, "parallel_bodies.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(policy), "ownedFunctionAnalyses && name == \"storeScanNoStorageCache\"") {
		t.Fatal("specialization-only clone boundary lost")
	}
	if !strings.Contains(string(policy), "ownedFunctionAnalyses && name == \"storeScanSharesCache\"") {
		t.Fatal("specialization-only shares cache clone boundary lost")
	}
	if !strings.Contains(string(policy), "ownedFunctionAnalyses && name == \"compilerBuiltinHelperCalls\"") {
		t.Fatal("specialization-only helper map clone boundary lost")
	}
	invalidation, err := os.ReadFile(filepath.Join(dir, "retained_struct_edit.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(invalidation), "rejectNoStorageOverlayOperation(\"retained type memo invalidation\")") {
		t.Fatal("overlay canonical invalidation guard lost")
	}
}
