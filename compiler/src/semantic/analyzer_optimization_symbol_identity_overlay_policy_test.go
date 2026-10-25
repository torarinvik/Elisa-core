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

func TestOptimizationSymbolIdentityUsesClosedOverlayAPI(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	boundary := map[string]map[string]bool{
		"analyzer_optimization_symbol_identity_overlay.go": {"lookupOptimizationSymbolIdentity": true, "setOptimizationSymbolIdentity": true},
		"parallel_bodies.go": {"cloneAnalyzerValueForParallelPolicy": true},
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, filepath.Join(directory, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		parents := map[goast.Node]goast.Node{}
		var stack []goast.Node
		goast.Inspect(file, func(node goast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			if len(stack) != 0 {
				parents[node] = stack[len(stack)-1]
			}
			stack = append(stack, node)
			return true
		})
		goast.Inspect(file, func(node goast.Node) bool {
			function := ""
			for parent := node; parent != nil; parent = parents[parent] {
				if fn, ok := parent.(*goast.FuncDecl); ok {
					function = fn.Name.Name
					break
				}
			}
			if literal, ok := node.(*goast.BasicLit); ok && literal.Kind == token.STRING {
				value, _ := strconv.Unquote(literal.Value)
				if value == "optimizationSymbolIDs" && !(entry.Name() == "parallel_bodies.go" && function == "cloneAnalyzerValueForParallelPolicy") {
					t.Errorf("named/reflection fact-table access needs explicit policy at %s", set.Position(literal.Pos()))
				}
			}
			if pair, ok := node.(*goast.KeyValueExpr); ok {
				if key, ok := pair.Key.(*goast.Ident); ok && key.Name == "optimizationSymbolIDs" {
					t.Errorf("Analyzer iteration-map initializer needs explicit canonical-owner policy at %s", set.Position(pair.Pos()))
				}
			}
			selector, ok := node.(*goast.SelectorExpr)
			if !ok || selector.Sel.Name != "optimizationSymbolIDs" {
				return true
			}
			if !boundary[entry.Name()][function] {
				t.Errorf("raw Analyzer.optimizationSymbolIDs read/write/delete/range/len/alias bypasses overlay at %s (%s)", set.Position(selector.Pos()), function)
			}
			return true
		})
	}
}
