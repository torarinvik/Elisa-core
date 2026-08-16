package frontendir

// Enumerating the AST's types is the one thing reflection cannot do: Go offers no
// way to list the implementations of an interface, and `ast.File` reaches almost
// everything through `[]Decl` / `Expr` / `Stmt`. The v1 gob registry solved this
// with a hand-maintained list of 150 `gob.Register` calls, which is exactly the
// kind of list that rots — a node type added to the parser and forgotten here
// fails at ENCODE time, on a user's file, not in a test.
//
// So the root set is derived from the package source instead, and
// TestSchemaRootsMatchSource re-scans and fails if the generated list drifts.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ScanASTStructNames returns every exported struct type declared in the Go
// package rooted at dir, sorted. Test files are ignored.
func ScanASTStructNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				if _, ok := ts.Type.(*ast.StructType); !ok {
					continue
				}
				names = append(names, ts.Name.Name)
			}
		}
	}
	sort.Strings(names)
	return names, nil
}
