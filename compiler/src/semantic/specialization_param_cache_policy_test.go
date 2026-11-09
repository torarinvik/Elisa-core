package semantic

import (
	"crypto/sha256"
	"fmt"
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

func TestSpecializationParamCacheSourcePolicy(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	golden := map[string]string{"region_store_scan_param_writes.go|storeScanParamLocalFacts": "16fc7cd91e8c4b043f8ef27107eae112aa33bed67c60b396781139a2860bc8d3", "region_store_scan_param_facts.go|computeStoreScanParamFacts": "3277b486be3da657baf4d8811113066fbc53261a444a2f815aa054294711e6f6", "region_store_scan_param_writes.go|storeScanParamReaches": "32e152da73dd74d542194ab1453c326b6af6ab38d63d90f93d1344536569089e"}
	allowedCalls := map[string]bool{"storeScanParamLocalFacts": true, "storeScanParamReaches": true, "lookupStoreScanParamWriteCache": true, "setStoreScanParamWriteCache": true}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok {
				continue
			}
			name := fn.Name.Name
			if expected, ok := golden[entry.Name()+"|"+name]; ok {
				start := set.Position(fn.Pos()).Offset
				end := set.Position(fn.End()).Offset
				if fmt.Sprintf("%x", sha256.Sum256(source[start:end])) != expected {
					t.Errorf("review settled-cache producer/consumer change: %s %s", entry.Name(), name)
				}
				delete(golden, entry.Name()+"|"+name)
			}
			goast.Inspect(fn.Body, func(node goast.Node) bool {
				if literal, ok := node.(*goast.BasicLit); ok && literal.Kind == token.STRING {
					value, _ := strconv.Unquote(literal.Value)
					if (value == "storeScanParamFactsCache" && !(entry.Name() == "parallel_body_worker_clone.go" && name == "cloneAnalyzerValueForParallelPolicy") && !isGeneratedSpecializationClone(entry.Name(), name)) ||
						(value == "storeScanParamWriteCache" && !(entry.Name() == "parallel_body_worker_clone.go" && name == "cloneAnalyzerValueForParallelPolicy") && !isGeneratedSpecializationClone(entry.Name(), name)) {
						t.Errorf("named reflection bypass needs review: %s", set.Position(node.Pos()))
					}
				}
				selector, ok := node.(*goast.SelectorExpr)
				if !ok {
					return true
				}
				if selector.Sel.Name == "storeScanParamFactsCache" && !(entry.Name() == "parallel_body_worker_clone.go" && name == "cloneAnalyzerValueForParallelPolicy") && !isGeneratedSpecializationClone(entry.Name(), name) && !(entry.Name() == "analyzer_store_scan_param_facts_overlay.go" && (name == "lookupStoreScanParamFacts" || name == "setStoreScanParamFacts")) && !(entry.Name() == "retained_cache_invalidation.go" && name == "invalidateRetainedBodyCaches") {
					t.Errorf("raw cache access outside reviewed policy: %s", set.Position(node.Pos()))
				}
				if selector.Sel.Name == "storeScanParamWriteCache" && !(entry.Name() == "analyzer_no_storage_overlay.go" && (name == "lookupStoreScanParamWriteCache" || name == "setStoreScanParamWriteCache")) && !(entry.Name() == "parallel_body_worker_clone.go" && name == "cloneAnalyzerValueForParallelPolicy") && !isGeneratedSpecializationClone(entry.Name(), name) && !(entry.Name() == "retained_cache_invalidation.go" && name == "invalidateRetainedBodyCaches") {
					t.Errorf("raw reachability cache access outside reviewed policy: %s", set.Position(node.Pos()))
				}
				if selector.Sel.Name == "storeScanParamLocalFacts" && !allowedCalls[name] {
					t.Errorf("new cache consumer needs review: %s", set.Position(node.Pos()))
				}
				return true
			})
		}
	}
	if len(golden) != 0 {
		t.Fatal("reviewed functions missing", golden)
	}
}
