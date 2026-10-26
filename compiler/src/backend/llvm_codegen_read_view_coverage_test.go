//go:build cgo

package backend

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// Keep the highest-volume semantic maps behind the explicit per-generator
// read view. This source gate is intentionally structural: adding a new direct
// field read must be routed through the view before the audit can grow.
func TestCodegenGlobalSemanticReadsUseReadView(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		check := func(node ast.Node, function string) {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return
			}
			if selector.Sel.Name == "NamedTypes" || selector.Sel.Name == "GlobalScope" {
				allowedViewMethods := map[string]bool{
					"LookupNamedType": true, "HasNamedTypes": true, "NamedTypesMap": true,
					"HasGlobalScope": true, "LookupGlobalSymbol": true, "GlobalSymbolsMap": true,
				}
				if name != "llvm_codegen_read_view.go" || !allowedViewMethods[function] {
					t.Errorf("%s: direct Result.%s access in %s bypasses the read view", name, selector.Sel.Name, function)
				}
			}
		}
		if name != "llvm_codegen_read_view.go" {
			ast.Inspect(file, func(node ast.Node) bool {
				check(node, "<package>")
				return true
			})
			continue
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				check(node, function.Name.Name)
				return true
			})
		}
	}
}
