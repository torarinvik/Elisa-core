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
		"analyzer_optimization_symbol_identity_overlay.go": {"lookupOptimizationSymbolIdentity": true, "setOptimizationSymbolIdentity": true, "canonicalOptimizationSymbolIdentities": true, "deleteCanonicalOptimizationSymbolIdentity": true},
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
				if value == "optimizationSymbolIDs" && !(entry.Name() == "parallel_bodies.go" && function == "cloneAnalyzerValueForParallelPolicy") && !(entry.Name() == "analyzer_optimization_symbol_identity_overlay.go" && function == "canonicalOptimizationSymbolIdentityTableName") {
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

// Canonical inventory has a deliberately closed consumer grammar: acquire a
// local in a short declaration, then use it only as a direct noncapturing range.
// Every other identifier context (including helper arguments/composites) rejects.
func canonicalSymbolIdentityBorrowViolations(file *goast.File) []token.Pos {
	parents := map[goast.Node]goast.Node{}
	var stack []goast.Node
	goast.Inspect(file, func(node goast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parents[node] = stack[len(stack)-1]
		}
		stack = append(stack, node)
		return true
	})
	var failures []token.Pos
	definitions := map[*goast.Object]*goast.Ident{}
	captured := func(node goast.Node) bool {
		for current := parents[node]; current != nil; current = parents[current] {
			if _, ok := current.(*goast.FuncLit); ok {
				return true
			}
		}
		return false
	}
	goast.Inspect(file, func(node goast.Node) bool {
		call, ok := node.(*goast.CallExpr)
		if !ok {
			return true
		}
		method, ok := call.Fun.(*goast.SelectorExpr)
		if !ok || method.Sel.Name != "canonicalOptimizationSymbolIdentities" {
			return true
		}
		assignment, ok := parents[call].(*goast.AssignStmt)
		if !ok || assignment.Tok != token.DEFINE || len(assignment.Rhs) != 1 || assignment.Rhs[0] != call || len(assignment.Lhs) != 2 || captured(call) {
			failures = append(failures, call.Pos())
			return true
		}
		id, ok := assignment.Lhs[0].(*goast.Ident)
		if !ok {
			failures = append(failures, assignment.Lhs[0].Pos())
			return true
		}
		if id.Name == "_" {
			return true
		}
		if id.Obj == nil {
			failures = append(failures, id.Pos())
			return true
		}
		definitions[id.Obj] = id
		return true
	})
	goast.Inspect(file, func(node goast.Node) bool {
		id, ok := node.(*goast.Ident)
		if !ok || id.Obj == nil {
			return true
		}
		definition, borrowed := definitions[id.Obj]
		if !borrowed || id == definition {
			return true
		}
		loop, rangeUse := parents[id].(*goast.RangeStmt)
		if !rangeUse || loop.X != id || captured(id) {
			failures = append(failures, id.Pos())
		}
		return true
	})
	return failures
}

func TestCanonicalSymbolIdentityInventoryConsumersRemainReadOnly(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
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
		for _, position := range canonicalSymbolIdentityBorrowViolations(file) {
			t.Errorf("canonical identity borrow left closed readonly consumer grammar at %s", set.Position(position))
		}
	}
}

func TestCanonicalSymbolIdentityBorrowPolicyRejectsEveryEscapeContext(t *testing.T) {
	for name, body := range map[string]string{
		"range":          "ids, err := a.canonicalOptimizationSymbolIdentities(); _ = err; for _, token := range ids { _ = token }",
		"checked-delete": "_, err := a.canonicalOptimizationSymbolIdentities(); _ = err; _ = a.deleteCanonicalOptimizationSymbolIdentity(nil)",
	} {
		t.Run("allowed/"+name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "policy.go", "package p; func f() { "+body+" }", 0)
			if err != nil {
				t.Fatal(err)
			}
			if failures := canonicalSymbolIdentityBorrowViolations(file); len(failures) != 0 {
				t.Fatal("actual readonly consumer grammar rejected")
			}
		})
	}
	for name, body := range map[string]string{
		"arbitrary-helper":     "helper(ids)",
		"composite":            "_ = struct{ values interface{} }{values: ids}",
		"closure":              "_ = func() { for range ids {} }",
		"closure-return":       "_ = func() interface{} { return ids }",
		"alias":                "other := ids; _ = other",
		"return":               "return ids",
		"index-write":          "ids[nil] = \"changed\"",
		"index-read":           "_ = ids[nil]",
		"delete":               "delete(ids,nil)",
		"clear":                "clear(ids)",
		"interface-conversion": "_ = interface{}(ids)",
		"address":              "_ = &ids",
	} {
		t.Run("rejected/"+name, func(t *testing.T) {
			source := "package p; func f() { ids, err := a.canonicalOptimizationSymbolIdentities(); _ = err; " + body + " }"
			file, err := parser.ParseFile(token.NewFileSet(), "policy.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(canonicalSymbolIdentityBorrowViolations(file)) == 0 {
				t.Fatal("unapproved borrow context accepted")
			}
		})
	}
	for name, body := range map[string]string{
		"inline-helper":      "helper(a.canonicalOptimizationSymbolIdentities())",
		"acquire-in-closure": "_ = func() { ids, err := a.canonicalOptimizationSymbolIdentities(); _ = err; for range ids {} }",
	} {
		t.Run("rejected/"+name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "policy.go", "package p; func f() { "+body+" }", 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(canonicalSymbolIdentityBorrowViolations(file)) == 0 {
				t.Fatal("unapproved acquisition context accepted")
			}
		})
	}
}
