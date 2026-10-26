package semantic

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	compilerast "elisacore/src/ast"
)

func TestSpecializationFuncDeclUsingsSharesRowsAndIsolatesOuterMap(t *testing.T) {
	decl := &compilerast.FuncDecl{Name: "cached"}
	newDecl := &compilerast.FuncDecl{Name: "private"}
	row := make([]string, 2, 4)
	row[0], row[1] = "Std", "Util"
	root := &Analyzer{funcDeclUsings: map[*compilerast.FuncDecl][]string{decl: row}}
	private := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if reflect.ValueOf(private.funcDeclUsings).Pointer() == reflect.ValueOf(root.funcDeclUsings).Pointer() {
		t.Fatal("specialization shares the mutable outer using map")
	}
	if &private.funcDeclUsings[decl][0] != &root.funcDeclUsings[decl][0] || cap(private.funcDeclUsings[decl]) != cap(row) {
		t.Fatal("specialization did not retain the canonical immutable slice backing")
	}

	// Resolution contexts copy the row before exposing it as currentUsings.
	private.withResolutionContext("Module", private.funcDeclUsings[decl], func() {
		if len(private.currentUsings) != len(row) || &private.currentUsings[0] == &row[0] {
			t.Error("resolution context aliases the cached using row")
			return
		}
		private.currentUsings[0] = "Private"
		private.currentUsings = append(private.currentUsings, "Extra")
	})
	if root.funcDeclUsings[decl][0] != "Std" || len(root.funcDeclUsings[decl]) != 2 {
		t.Fatal("context mutation reached the canonical using row")
	}

	private.funcDeclUsings[newDecl] = []string{"Private"}
	delete(private.funcDeclUsings, decl)
	if _, ok := root.funcDeclUsings[newDecl]; ok {
		t.Fatal("specialization insertion leaked into the canonical using map")
	}
	if _, ok := root.funcDeclUsings[decl]; !ok {
		t.Fatal("specialization deletion removed the canonical using row")
	}

	// Ordinary workers keep the existing deepCloneValue policy: a private
	// outer map and cap-limited slice values, so appending cannot reuse a
	// canonical row's spare capacity.
	worker := cloneAnalyzerValueForBodyWorker(root).Addr().Interface().(*Analyzer)
	if reflect.ValueOf(worker.funcDeclUsings).Pointer() == reflect.ValueOf(root.funcDeclUsings).Pointer() {
		t.Fatal("ordinary worker shares the mutable outer using map")
	}
	workerRow := worker.funcDeclUsings[decl]
	if cap(workerRow) != len(workerRow) {
		t.Fatal("ordinary worker slice capacity policy changed")
	}
	if &workerRow[0] != &row[0] {
		t.Fatal("ordinary worker no longer follows deepCloneValue's slice-sharing policy")
	}
	worker.funcDeclUsings[decl] = append(workerRow, "WorkerOnly")
	if len(root.funcDeclUsings[decl]) != len(row) || root.funcDeclUsings[decl][:cap(row)][2] != "" {
		t.Fatal("ordinary worker append reused or changed canonical spare capacity")
	}
	worker.funcDeclUsings[newDecl] = []string{"Worker"}
	delete(worker.funcDeclUsings, decl)
	if _, ok := root.funcDeclUsings[newDecl]; ok {
		t.Fatal("ordinary worker insertion leaked into the canonical map")
	}
	if _, ok := root.funcDeclUsings[decl]; !ok {
		t.Fatal("ordinary worker deletion removed the canonical row")
	}

	var clones sync.WaitGroup
	for i := 0; i < 8; i++ {
		clones.Add(1)
		go func() {
			defer clones.Done()
			concurrent := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
			concurrent.withResolutionContext("Module", concurrent.funcDeclUsings[decl], func() {
				if len(concurrent.currentUsings) != len(row) || &concurrent.currentUsings[0] == &row[0] {
					t.Error("concurrent resolution context aliases its cached row")
					return
				}
				concurrent.currentUsings[0] = "ConcurrentPrivate"
			})
			concurrent.funcDeclUsings[newDecl] = []string{"Private"}
			delete(concurrent.funcDeclUsings, decl)
		}()
	}
	clones.Wait()
	if row[0] != "Std" || len(root.funcDeclUsings) != 1 {
		t.Fatal("concurrent specialization mutated canonical using state")
	}
}

func TestFuncDeclUsingsRowsAreOnlyInstalledAsFreshCopies(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Dir(file)
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		writer := ""
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch item := node.(type) {
			case *ast.AssignStmt:
				for i, lhs := range item.Lhs {
					depth, found := funcDeclUsingsIndexDepth(lhs)
					if !found {
						continue
					}
					if depth > 1 {
						writer = "cached using slice element assignment"
						return false
					}
					if depth == 1 {
						if filepath.Base(path) != "analyzer_value_symbols.go" && filepath.Base(path) != "retained_using_edit.go" {
							writer = "new funcDeclUsings row writer in " + filepath.Base(path)
							return false
						}
						if len(item.Rhs) != len(item.Lhs) || i >= len(item.Rhs) || !freshFuncDeclUsingsCopy(item.Rhs[i]) {
							writer = "funcDeclUsings row is not installed from a fresh slice copy"
							return false
						}
					}
				}
			case *ast.IncDecStmt:
				if depth, found := funcDeclUsingsIndexDepth(item.X); found && depth > 1 {
					writer = "cached using slice increment/decrement"
					return false
				}
			case *ast.CallExpr:
				if mutatingFuncDeclUsingsCall(item) {
					writer = "mutating call receives a cached using slice"
					return false
				}
			}
			return writer == ""
		})
		if writer != "" {
			t.Fatalf("%s: %s", filepath.Base(path), writer)
		}
	}
}

func funcDeclUsingsIndexDepth(expr ast.Expr) (int, bool) {
	switch node := expr.(type) {
	case *ast.SelectorExpr:
		return 0, node.Sel != nil && node.Sel.Name == "funcDeclUsings"
	case *ast.IndexExpr:
		depth, found := funcDeclUsingsIndexDepth(node.X)
		return depth + 1, found
	case *ast.SliceExpr:
		depth, found := funcDeclUsingsIndexDepth(node.X)
		return depth + 1, found
	case *ast.ParenExpr:
		return funcDeclUsingsIndexDepth(node.X)
	case *ast.StarExpr:
		return funcDeclUsingsIndexDepth(node.X)
	default:
		return 0, false
	}
}

func freshFuncDeclUsingsCopy(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) < 2 {
		return false
	}
	appendName, ok := call.Fun.(*ast.Ident)
	if !ok || appendName.Name != "append" {
		return false
	}
	seed, ok := call.Args[0].(*ast.CallExpr)
	if !ok || len(seed.Args) != 1 {
		return false
	}
	seedType, ok := seed.Fun.(*ast.ArrayType)
	if !ok {
		return false
	}
	stringType, ok := seedType.Elt.(*ast.Ident)
	if !ok || stringType.Name != "string" {
		return false
	}
	nilValue, ok := seed.Args[0].(*ast.Ident)
	return ok && nilValue.Name == "nil"
}

func mutatingFuncDeclUsingsCall(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	mutator := ""
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		mutator = fun.Name
	case *ast.SelectorExpr:
		if fun.Sel != nil {
			mutator = fun.Sel.Name
		}
	}
	switch mutator {
	case "append", "clear", "copy", "Compact", "Delete", "Insert", "Reverse", "Sort", "Strings":
		depth, found := funcDeclUsingsIndexDepth(call.Args[0])
		return found && depth == 1
	default:
		return false
	}
}
