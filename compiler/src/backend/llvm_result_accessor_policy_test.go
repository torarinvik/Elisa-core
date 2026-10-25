//go:build cgo

package backend

import (
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

// A sparse semantic view cannot implement fallback through raw Go-map reads.
// Keep the consumer inventory fail-closed when a Result node-map or use changes.
func TestBackendResultNodeFactsUseViewAccessors(t *testing.T) {
	nodeType := reflect.TypeOf((*ast.Node)(nil)).Elem()
	resultType := reflect.TypeOf(semantic.Result{})
	fields := map[string]bool{}
	for i := 0; i < resultType.NumField(); i++ {
		field := resultType.Field(i)
		if field.PkgPath == "" && field.Type.Kind() == reflect.Map && field.Type.Key().Implements(nodeType) {
			fields[field.Name] = true
		}
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("test source path unavailable")
	}
	directory := filepath.Dir(source)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(directory, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stack []goast.Node
		goast.Inspect(file, func(node goast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if selector, ok := node.(*goast.SelectorExpr); ok && fields[selector.Sel.Name] {
				var parent goast.Node
				if len(stack) != 0 {
					parent = stack[len(stack)-1]
				}
				function := ""
				for i := len(stack) - 1; i >= 0; i-- {
					if fn, ok := stack[i].(*goast.FuncDecl); ok {
						function = fn.Name.Name
						break
					}
				}
				allowed := false
				if binary, ok := parent.(*goast.BinaryExpr); ok && (binary.Op == token.EQL || binary.Op == token.NEQ) {
					var other goast.Expr
					if binary.X == selector {
						other = binary.Y
					} else if binary.Y == selector {
						other = binary.X
					}
					if ident, ok := other.(*goast.Ident); ok && ident.Name == "nil" {
						allowed = true
					}
				}
				// These two serial setters are the sole intentional raw writes;
				// their shared-codegen paths already use generator-local overlays.
				if name == "llvm_partition_ir.go" && selector.Sel.Name == "ExprTypes" {
					if function == "setResultExprType" {
						if index, ok := parent.(*goast.IndexExpr); ok && index.X == selector && len(stack) > 1 {
							if assignment, ok := stack[len(stack)-2].(*goast.AssignStmt); ok {
								for _, lhs := range assignment.Lhs {
									if lhs == index {
										allowed = true
									}
								}
							}
						}
					}
					if function == "deleteResultExprType" {
						if call, ok := parent.(*goast.CallExpr); ok && len(call.Args) == 2 && call.Args[0] == selector {
							if ident, ok := call.Fun.(*goast.Ident); ok && ident.Name == "delete" {
								allowed = true
							}
						}
					}
				}
				if !allowed {
					t.Errorf("%s: raw node-fact map %s in %s bypasses the sparse view", fset.Position(selector.Pos()), selector.Sel.Name, function)
				}
			}
			stack = append(stack, node)
			return true
		})
	}
}
