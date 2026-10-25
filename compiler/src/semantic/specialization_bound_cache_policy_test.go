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

func TestSpecializationBoundCacheSourcePolicy(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	golden := map[string]string{"region_struct_target_store_scan.go|storeScanBoundNames": "684bef7c777d713094485eb7198ef3168883c473e83ea6b9a8dee90e777bd7fc", "region_struct_target_store_scan.go|storeScanParamFieldChainType": "fa52511c07e7ccdded4b011b2d8e6e0593fb5f58aaaca5687d5a22f9bb547d00", "region_store_scan_pattern_binder.go|storeScanSubjectDeclType": "24dae7d6e50b15a8fc842fe2c3449b2729d791f402dd14db36c19ee9543819e6"}
	allowedCalls := map[string]bool{"storeScanBoundNames": true, "storeScanParamFieldChainType": true, "storeScanSubjectDeclType": true}
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
					if value == "storeScanBoundCache" && !(entry.Name() == "parallel_bodies.go" && name == "cloneAnalyzerValueForParallelPolicy") && !(entry.Name() == "specialization_maps_generated.go" && name == "cloneSpecializationOpaqueMap") {
						t.Errorf("named reflection bypass needs review: %s", set.Position(node.Pos()))
					}
				}
				selector, ok := node.(*goast.SelectorExpr)
				if !ok {
					return true
				}
				if selector.Sel.Name == "storeScanBoundCache" && !(entry.Name() == "parallel_bodies.go" && name == "cloneAnalyzerValueForParallelPolicy") && !(entry.Name() == "specialization_maps_generated.go" && name == "cloneSpecializationOpaqueMap") && !(entry.Name() == "region_struct_target_store_scan.go" && name == "storeScanBoundNames") {
					t.Errorf("raw cache access outside reviewed policy: %s", set.Position(node.Pos()))
				}
				if selector.Sel.Name == "storeScanBoundNames" && !allowedCalls[name] {
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
