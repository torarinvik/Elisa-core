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
		"analyzer_no_storage_overlay.go": {"lookupStoreScanNoStorage": true, "setStoreScanNoStorage": true, "lookupStoreScanShares": true, "setStoreScanShares": true, "lookupStoreScanParamWriteCache": true, "setStoreScanParamWriteCache": true, "lookupCompilerBuiltinHelperCall": true, "setCompilerBuiltinHelperCall": true, "lookupReturnElementStmtUnknown": true, "setReturnElementStmtUnknown": true, "clearReturnElementStmtUnknown": true},
		// analyzeDeclsParallel rejects every active construction overlay before
		// clearing canonical per-return scratch maps for its next body pass.
		"parallel_bodies.go":                {"cloneAnalyzerValueForParallelPolicy": true, "analyzeDeclsParallel": true},
		"specialization_maps_generated.go":  {"cloneSpecializationOpaqueMap": true},
		"shared_codegen.go":                 {"BeginSharedCodegen": true},
		"optimization_result_api.go":        {"IsCompilerBuiltinHelperCall": true},
		"retained_plain_default_product.go": {"captureRetainedPlainDefaultProductPhase": true},
		"retained_analysis.go":              {"UpdatePrepared": true},
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
				if selector, ok := n.(*goast.SelectorExpr); ok && (selector.Sel.Name == "storeScanNoStorageCache" || selector.Sel.Name == "storeScanSharesCache" || selector.Sel.Name == "storeScanParamWriteCache" || selector.Sel.Name == "compilerBuiltinHelperCalls" || selector.Sel.Name == "returnElementStmtUnknown") && !allowed[entry.Name()][fn.Name.Name] {
					t.Errorf("raw analyzer cache access bypasses overlay at %s", set.Position(n.Pos()))
				}
				if literal, ok := n.(*goast.BasicLit); ok && literal.Kind == token.STRING {
					value, _ := strconv.Unquote(literal.Value)
					if (value == "storeScanNoStorageCache" || value == "storeScanSharesCache" || value == "storeScanParamWriteCache" || value == "compilerBuiltinHelperCalls" || value == "returnElementStmtUnknown") && !allowed[entry.Name()][fn.Name.Name] {
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
	if !strings.Contains(string(policy), "ownedFunctionAnalyses && name == \"returnElementStmtUnknown\"") {
		t.Fatal("specialization-only return-element map clone boundary lost")
	}
	if !strings.Contains(string(policy), "ownedFunctionAnalyses && name == \"storeScanParamWriteCache\"") {
		t.Fatal("specialization-only parameter-write map clone boundary lost")
	}
	invalidation, err := os.ReadFile(filepath.Join(dir, "retained_struct_edit.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(invalidation), "rejectNoStorageOverlayOperation(\"retained type memo invalidation\")") {
		t.Fatal("overlay canonical invalidation guard lost")
	}
	retainedUpdate, err := os.ReadFile(filepath.Join(dir, "retained_analysis.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(retainedUpdate), "a.storeScanParamWriteCache = nil") || !strings.Contains(string(retainedUpdate), "a.storeScanParamFactsCache = nil") {
		t.Fatal("retained semantic updates no longer invalidate parameter-write reachability and facts")
	}
}
