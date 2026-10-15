//go:build cgo

package semantic

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func walkNodesReflect(root any) []any {
	var nodes []any
	seen := map[uintptr]bool{}
	fillMayAdoptWalkReflect(reflect.ValueOf(root), func(n any) { nodes = append(nodes, n) }, seen)
	return nodes
}

func walkNodesTyped(t *testing.T, root any) []any {
	var nodes []any
	seen := map[uintptr]bool{}
	if !fillMayAdoptWalkTyped(root, func(n any) { nodes = append(nodes, n) }, seen) {
		t.Fatalf("typed walker declined root %T", root)
	}
	return nodes
}

func scanNodesReflect(stmts []ast.Stmt) []any {
	var nodes []any
	storeScanWalkReflect(reflect.ValueOf(stmts), func(n any) { nodes = append(nodes, n) }, map[uintptr]bool{})
	return nodes
}

func scanNodesTyped(t *testing.T, stmts []ast.Stmt) []any {
	var nodes []any
	if !storeScanWalkTyped(stmts, func(n any) { nodes = append(nodes, n) }, map[uintptr]bool{}) {
		t.Fatal("typed scan walker declined []ast.Stmt")
	}
	return nodes
}

// sameVisits compares two visit sequences by identity (pointer and dynamic type), in order.
func sameVisits(a, b []any) (bool, string) {
	if len(a) != len(b) {
		return false, fmt.Sprintf("length %d vs %d", len(a), len(b))
	}
	for i := range a {
		if reflect.TypeOf(a[i]) != reflect.TypeOf(b[i]) || reflect.ValueOf(a[i]).Pointer() != reflect.ValueOf(b[i]).Pointer() {
			return false, fmt.Sprintf("visit %d: %T vs %T", i, a[i], b[i])
		}
	}
	return true, ""
}

// DIFFERENTIAL: the generated typed walkers visit exactly the pointers the reflective walker
// visits, in the same order, over every .elisa program in the stage0 repository, both as parsed
// and after analysis (which attaches lowered and resolved nodes). The walk roots are the file and
// every top-level declaration, the shapes the call sites pass.
func TestTypedASTPointerWalkMatchesReflectiveWalk(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && filepath.Ext(path) == ".elisa" {
			files = append(files, path)
		}
		return nil
	})
	if len(files) < 100 {
		t.Fatalf("corpus too small: %d files under %s", len(files), root)
	}
	compared := 0
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		l := lexer.New(path, src)
		tokens := l.Tokenize()
		if len(l.Errors()) != 0 {
			continue
		}
		p := parser.New(tokens)
		file := p.ParseFile(path)
		if file == nil {
			continue
		}
		check := func(stage string) {
			roots := []any{file}
			for _, d := range file.Decls {
				roots = append(roots, d)
			}
			roots = append(roots, file.Decls)
			for _, r := range roots {
				if r == nil || (reflect.ValueOf(r).Kind() == reflect.Pointer && reflect.ValueOf(r).IsNil()) {
					continue
				}
				if ok, why := sameVisits(walkNodesReflect(r), walkNodesTyped(t, r)); !ok {
					t.Fatalf("%s (%s): root %T: %s", path, stage, r, why)
				}
				compared++
			}
			// walkStoreScanNodes (no map values) over every function body.
			for _, d := range file.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn == nil {
					continue
				}
				if ok, why := sameVisits(scanNodesReflect(fn.Body), scanNodesTyped(t, fn.Body)); !ok {
					t.Fatalf("%s (%s): scan walk of %s: %s", path, stage, fn.Name, why)
				}
				compared++
			}
		}
		check("parsed")
		func() {
			defer func() { _ = recover() }() // analysis of an arbitrary probe may panic; parse-stage diff still counted
			Analyze(file)
		}()
		check("analyzed")
	}
	if compared == 0 {
		t.Fatal("nothing compared")
	}
	t.Logf("compared %d walk roots over %d files", compared, len(files))
}
