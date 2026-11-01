package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/semantic"
)

func BenchmarkTypeGraphObserverFunctionCount(b *testing.B) {
	for _, count := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("functions_%d", count), func(b *testing.B) {
			functions := make([]*ast.FuncDecl, 0, count)
			var expr *ast.IntLit
			for i := 0; i < count; i++ {
				node := &ast.IntLit{Value: fmt.Sprint(i)}
				functions = append(functions, &ast.FuncDecl{Name: fmt.Sprintf("f%d", i), Body: []ast.Stmt{&ast.ReturnStmt{Value: node}}})
				if i == count-1 {
					expr = node
				}
			}
			result := &semantic.Result{ExprTypes: map[ast.Expr]semantic.Type{expr: &semantic.BuiltinType{Name: "i64"}}}
			observer, err := newTypeGraphObserverForFunctions(functions)
			if err != nil {
				b.Fatal(err)
			}
			view := semantic.NewCodegenReadView(result, observer.observe)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				view.LookupExprTypes(expr)
				observer.observations = observer.observations[:0]
			}
		})
	}
}

func BenchmarkTypeGraphObserverBodySize(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("statements_%d", count), func(b *testing.B) {
			fn := &ast.FuncDecl{Name: "f"}
			var expr *ast.IntLit
			for i := 0; i < count; i++ {
				expr = &ast.IntLit{Value: fmt.Sprint(i)}
				fn.Body = append(fn.Body, &ast.ReturnStmt{Value: expr})
			}
			result := &semantic.Result{ExprTypes: map[ast.Expr]semantic.Type{expr: &semantic.BuiltinType{Name: "i64"}}}
			observer, err := newTypeGraphObserverForFunctions([]*ast.FuncDecl{fn})
			if err != nil {
				b.Fatal(err)
			}
			view := semantic.NewCodegenReadView(result, observer.observe)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				view.LookupExprTypes(expr)
				observer.observations = observer.observations[:0]
			}
		})
	}
}

func TestPortableLocalFactKeysStableAndFailClosed(t *testing.T) {
	fixture := func(value string) (*ast.FuncDecl, *ast.IntLit) {
		expr := &ast.IntLit{Value: value}
		return &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}, expr
	}
	first, firstExpr := fixture("1")
	read := semantic.CodegenFactRead{Table: "ExprTypes", Operation: "lookup", Key: firstExpr, Present: true}
	want, err := portableLocalFactKeys(first, []semantic.CodegenFactRead{read})
	if err != nil {
		t.Fatal(err)
	}
	second, secondExpr := fixture("1")
	read.Key = secondExpr
	got, err := portableLocalFactKeys(second, []semantic.CodegenFactRead{read})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("equivalent trees: %v, %v", got, err)
	}
	changed, changedExpr := fixture("2")
	read.Key = changedExpr
	got, err = portableLocalFactKeys(changed, []semantic.CodegenFactRead{read})
	if err != nil || reflect.DeepEqual(got, want) {
		t.Fatalf("changed tree: %v, %v", got, err)
	}
	read.Key = firstExpr
	if _, err := portableLocalFactKeys(second, []semantic.CodegenFactRead{read}); err == nil {
		t.Fatal("accepted foreign node")
	}
	read.Key = secondExpr
	read.Operation = "range-end"
	if _, err := portableLocalFactKeys(second, []semantic.CodegenFactRead{read}); err == nil {
		t.Fatal("accepted incomplete operation")
	}
}

func TestLocalFactAnchorDoesNotIdentifySourceFunction(t *testing.T) {
	fixture := func(path string) (*ast.File, *ast.FuncDecl, *ast.IntLit) {
		expr := &ast.IntLit{Value: "1"}
		fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
		return &ast.File{Filename: path, Decls: []ast.Decl{fn}}, fn, expr
	}
	fileA, fnA, exprA := fixture("a.elisa")
	fileB, fnB, exprB := fixture("b.elisa")
	read := semantic.CodegenFactRead{Table: "ExprTypes", Operation: "lookup", Present: true, Key: exprA}
	a, err := portableLocalFactKeys(fnA, []semantic.CodegenFactRead{read})
	if err != nil {
		t.Fatal(err)
	}
	read.Key = exprB
	b, err := portableLocalFactKeys(fnB, []semantic.CodegenFactRead{read})
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("equivalent local syntax: %v", err)
	}
	locA, err := semantic.DiagnosticFunctionLocators(fileA)
	if err != nil {
		t.Fatal(err)
	}
	locB, err := semantic.DiagnosticFunctionLocators(fileB)
	if err != nil {
		t.Fatal(err)
	}
	if locA[fnA] == locB[fnB] {
		t.Fatal("distinct source roots collapsed")
	}
}

func TestFunctionEndpointHashesDoNotDetectTransientMutation(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	index, err := semantic.IndexFunctionNodes(fn)
	if err != nil {
		t.Fatal(err)
	}
	before, err := index.Anchor(expr)
	if err != nil {
		t.Fatal(err)
	}
	expr.Value = "2"
	if _, err := index.Anchor(expr); err == nil {
		t.Fatal("mutation during observation was not detected")
	}
	expr.Value = "1"
	after, err := index.Anchor(expr)
	if err != nil || before != after {
		t.Fatalf("restored endpoint: %v", err)
	}
	// Equal endpoint hashes cannot establish that no intermediate observation
	// saw a different tree; batching needs a proven mutation-free interval.
}

func TestLocatedTypeGraphFactsDistinguishSourceRoots(t *testing.T) {
	fixture := func(path string) (*ast.File, *ast.FuncDecl, *ast.IntLit, *semantic.Result) {
		expr := &ast.IntLit{Value: "1"}
		fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
		return &ast.File{Filename: path, Decls: []ast.Decl{fn}}, fn, expr, &semantic.Result{ExprTypes: map[ast.Expr]semantic.Type{expr: &semantic.BuiltinType{Name: "i64"}}}
	}
	aFile, aFn, aExpr, aResult := fixture("a.elisa")
	bFile, bFn, _, bResult := fixture("b.elisa")
	observer, err := newTypeGraphObserverForFunctions([]*ast.FuncDecl{aFn})
	if err != nil {
		t.Fatal(err)
	}
	semantic.NewCodegenReadView(aResult, observer.observe).LookupExprTypes(aExpr)
	facts, err := portableLocatedTypeGraphFacts(aFile, aFn, observer.snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := compareLocatedTypeGraphFacts(aFile, aFn, aResult, facts); !ok || err != nil {
		t.Fatalf("same source: %v %v", ok, err)
	}
	if ok, err := compareLocatedTypeGraphFacts(bFile, bFn, bResult, facts); ok || err != nil {
		t.Fatalf("different source accepted: %v %v", ok, err)
	}
	if _, err := portableLocatedTypeGraphFacts(bFile, aFn, observer.snapshot()); err == nil {
		t.Fatal("accepted function outside source graph")
	}
	aFile.Decls = append(aFile.Decls, aFn)
	if _, err := portableLocatedTypeGraphFacts(aFile, aFn, observer.snapshot()); err == nil {
		t.Fatal("accepted ambiguous duplicate declaration")
	}
}

func TestPortableLocalBoolFactsDistinguishValuesAndPresence(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	read := semantic.CodegenFactRead{Table: "PermGrowthOps", Operation: "lookup", Key: expr, Present: true, Value: false}
	a, err := portableLocalBoolFacts(fn, []semantic.CodegenFactRead{read})
	if err != nil {
		t.Fatal(err)
	}
	read.Value = true
	b, err := portableLocalBoolFacts(fn, []semantic.CodegenFactRead{read})
	if err != nil || reflect.DeepEqual(a, b) {
		t.Fatalf("true and false collapsed: %v", err)
	}
	read.Value = false
	read.Present = false
	c, err := portableLocalBoolFacts(fn, []semantic.CodegenFactRead{read})
	if err != nil || reflect.DeepEqual(a, c) {
		t.Fatalf("absent and stored false collapsed: %v", err)
	}
	for _, value := range []any{nil, int64(0), "false", &semantic.BuiltinType{Name: "i64"}} {
		read.Value = value
		if _, err := portableLocalBoolFacts(fn, []semantic.CodegenFactRead{read}); err == nil {
			t.Fatalf("accepted non-boolean value %T", value)
		}
	}
}

func TestCompareLocalBoolFactsRebindsAndDetectsChanges(t *testing.T) {
	fixture := func(value string) (*ast.FuncDecl, *ast.IntLit) {
		expr := &ast.IntLit{Value: value}
		return &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}, expr
	}
	old, oldExpr := fixture("1")
	read := semantic.CodegenFactRead{Table: "PermGrowthOps", Operation: "lookup", Key: oldExpr, Present: true, Value: false}
	facts, err := portableLocalBoolFacts(old, []semantic.CodegenFactRead{read})
	if err != nil {
		t.Fatal(err)
	}
	current, expr := fixture("1")
	result := &semantic.Result{PermGrowthOps: map[ast.Expr]bool{expr: false}}
	if ok, err := compareLocalBoolFacts(current, result, facts); !ok || err != nil {
		t.Fatalf("equivalent graph: %v %v", ok, err)
	}
	result.PermGrowthOps[expr] = true
	if ok, err := compareLocalBoolFacts(current, result, facts); ok || err != nil {
		t.Fatalf("changed value: %v %v", ok, err)
	}
	delete(result.PermGrowthOps, expr)
	if ok, err := compareLocalBoolFacts(current, result, facts); ok || err != nil {
		t.Fatalf("missing value: %v %v", ok, err)
	}
	changed, _ := fixture("2")
	if ok, err := compareLocalBoolFacts(changed, result, facts); ok || err == nil {
		t.Fatalf("changed tree: %v %v", ok, err)
	}
	facts[0].Key.Table = "unsupported"
	if ok, err := compareLocalBoolFacts(current, result, facts); ok || err == nil {
		t.Fatalf("unsupported table: %v %v", ok, err)
	}
}

func TestCompareLocalBoolFactsIndependentAnalyses(t *testing.T) {
	const source = `def main() -> i64:
    return 1
`
	old := parseAndAnalyzeBackendTest(t, "portable-bool.elisa", source)
	current := parseAndAnalyzeBackendTest(t, "portable-bool.elisa", source)
	oldFn := old.ActiveFile().Decls[0].(*ast.FuncDecl)
	newFn := current.ActiveFile().Decls[0].(*ast.FuncDecl)
	oldExpr := oldFn.Body[0].(*ast.ReturnStmt).Value
	newExpr := newFn.Body[0].(*ast.ReturnStmt).Value
	old.PermGrowthOps = map[ast.Expr]bool{oldExpr: true}
	current.PermGrowthOps = map[ast.Expr]bool{newExpr: true}
	var reads []semantic.CodegenFactRead
	view := semantic.NewCodegenReadView(old, func(read semantic.CodegenFactRead) { reads = append(reads, read) })
	view.LookupPermGrowthOps(oldExpr)
	facts, err := portableLocalBoolFacts(oldFn, reads)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := compareLocalBoolFacts(newFn, current, facts); !ok || err != nil {
		t.Fatalf("independent analysis: %v %v", ok, err)
	}
	current.PermGrowthOps[newExpr] = false
	if ok, err := compareLocalBoolFacts(newFn, current, facts); ok || err != nil {
		t.Fatalf("semantic value changed without syntax change: %v %v", ok, err)
	}
}

func TestPortableBuiltinTypeFactsIndependentAnalyses(t *testing.T) {
	const source = `def main() -> i64:
    return 1
`
	old := parseAndAnalyzeBackendTest(t, "portable-types.elisa", source)
	current := parseAndAnalyzeBackendTest(t, "portable-types.elisa", source)
	oldFn := old.ActiveFile().Decls[0].(*ast.FuncDecl)
	newFn := current.ActiveFile().Decls[0].(*ast.FuncDecl)
	oldExpr := oldFn.Body[0].(*ast.ReturnStmt).Value
	newExpr := newFn.Body[0].(*ast.ReturnStmt).Value
	var reads []semantic.CodegenFactRead
	view := semantic.NewCodegenReadView(old, func(read semantic.CodegenFactRead) { reads = append(reads, read) })
	view.LookupExprTypes(oldExpr)
	facts, err := portableLocalBuiltinTypeFacts(oldFn, reads)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := compareLocalBuiltinTypeFacts(newFn, current, facts); !ok || err != nil {
		t.Fatalf("independent analysis: %v %v", ok, err)
	}
	current.ExprTypes[newExpr] = &semantic.BuiltinType{Name: "u8"}
	if ok, err := compareLocalBuiltinTypeFacts(newFn, current, facts); ok || err != nil {
		t.Fatalf("changed type: %v %v", ok, err)
	}
	reads[0].Value = &semantic.StructType{Name: "Record"}
	if _, err := portableLocalBuiltinTypeFacts(oldFn, reads); err == nil {
		t.Fatal("accepted nominal type without layout")
	}
	reads[0].Value = (*semantic.BuiltinType)(nil)
	if _, err := portableLocalBuiltinTypeFacts(oldFn, reads); err == nil {
		t.Fatal("accepted typed nil")
	}
}

func TestFunctionSymbolKeyIsNotCompleteCodegenEvidence(t *testing.T) {
	// A symbol identity has a different contract from a lowering dependency:
	// fast-math changes code generation but is not part of this symbol key.
	regular := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}}
	fast := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}, FastMath: true}
	a, err := semantic.TypeSymbolKey(regular)
	if err != nil {
		t.Fatal(err)
	}
	b, err := semantic.TypeSymbolKey(fast)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Skip("symbol identity now distinguishes fast-math; revisit diagnostic coverage")
	}
	expr := &ast.IntLit{Value: "1"}
	fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	read := semantic.CodegenFactRead{Table: "ExprTypes", Operation: "lookup", Key: expr, Present: true, Value: regular}
	if _, err := portableLocalBuiltinTypeFacts(fn, []semantic.CodegenFactRead{read}); err == nil {
		t.Fatal("accepted function type using an incomplete symbol identity")
	}
}

func TestPortableFunctionTypeGraphDetectsFastMath(t *testing.T) {
	fixture := func() (*ast.FuncDecl, *ast.IntLit) {
		expr := &ast.IntLit{Value: "1"}
		return &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}, expr
	}
	oldFn, oldExpr := fixture()
	newFn, newExpr := fixture()
	oldType := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}}
	newType := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}}
	old := &semantic.Result{PermissionEffectsConverged: true, ExprTypes: map[ast.Expr]semantic.Type{oldExpr: oldType}}
	current := &semantic.Result{PermissionEffectsConverged: true, ExprTypes: map[ast.Expr]semantic.Type{newExpr: newType}}
	var reads []semantic.CodegenFactRead
	semantic.NewCodegenReadView(old, func(read semantic.CodegenFactRead) { reads = append(reads, read) }).LookupExprTypes(oldExpr)
	facts, err := portableLocalTypeGraphFacts(oldFn, reads)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := compareLocalTypeGraphFacts(newFn, current, facts); !ok || err != nil {
		t.Fatalf("equivalent graph: %v %v", ok, err)
	}
	newType.FastMath = true
	if ok, err := compareLocalTypeGraphFacts(newFn, current, facts); ok || err != nil {
		t.Fatalf("fast-math change: %v %v", ok, err)
	}
	current.PermissionEffectsConverged = false
	if ok, err := compareLocalTypeGraphFacts(newFn, current, facts); ok || err == nil {
		t.Fatalf("unconverged effects: %v %v", ok, err)
	}
}

func TestDiagnosticTypeObservationBorrowsMutableGraph(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	typ := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}}
	result := &semantic.Result{PermissionEffectsConverged: true, ExprTypes: map[ast.Expr]semantic.Type{expr: typ}}
	var reads []semantic.CodegenFactRead
	semantic.NewCodegenReadView(result, func(read semantic.CodegenFactRead) { reads = append(reads, read) }).LookupExprTypes(expr)
	before, err := portableLocalTypeGraphFacts(fn, reads)
	if err != nil {
		t.Fatal(err)
	}
	typ.FastMath = true
	after, err := portableLocalTypeGraphFacts(fn, reads)
	if err != nil || reflect.DeepEqual(before, after) {
		t.Fatalf("borrowed observation unexpectedly preserved old type state: %v", err)
	}
}

func TestTypeGraphObserverCapturesAtReadTime(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	typ := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}}
	result := &semantic.Result{PermissionEffectsConverged: true, ExprTypes: map[ast.Expr]semantic.Type{expr: typ}}
	observer := &irTypeGraphObserver{}
	view := semantic.NewCodegenReadView(result, observer.observe)
	view.LookupExprTypes(expr)
	typ.FastMath = true
	view.LookupExprTypes(expr)
	rows := observer.snapshot()
	if len(rows) != 2 || !rows[0].Valid || !rows[1].Valid || rows[0].Digest == rows[1].Digest {
		t.Fatalf("observation-time change lost: %#v", rows)
	}
	result.PermissionEffectsConverged = false
	view.LookupExprTypes(expr)
	rows = observer.snapshot()
	if len(rows) != 3 || rows[2].Valid || rows[2].Error == "" {
		t.Fatalf("unconverged graph not recorded as invalid: %#v", rows)
	}
}

func TestPortableObservedTypeGraphRejectsPrivateAndKeepsObservedDigest(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	typ := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}}
	result := &semantic.Result{PermissionEffectsConverged: true, ExprTypes: map[ast.Expr]semantic.Type{expr: typ}}
	observer, err := newTypeGraphObserverForFunctions([]*ast.FuncDecl{fn})
	if err != nil {
		t.Fatal(err)
	}
	semantic.NewCodegenReadView(result, observer.observe).LookupExprTypes(expr)
	rows := observer.snapshot()
	typ.FastMath = true
	facts, err := portableObservedTypeGraphFacts(fn, rows)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := compareLocalTypeGraphFacts(fn, result, facts); ok || err != nil {
		t.Fatalf("missed change after observation: %v %v", ok, err)
	}
	expr.Value = "2"
	if _, err := portableObservedTypeGraphFacts(fn, rows); err == nil {
		t.Fatal("accepted syntax changed after observation")
	}
	expr.Value = "1"
	rows[0].PrivateResult = true
	if _, err := portableObservedTypeGraphFacts(fn, rows); err == nil {
		t.Fatal("accepted unverified private result")
	}
	rows[0].PrivateResult = false
	rows[0].Valid = false
	if _, err := portableObservedTypeGraphFacts(fn, rows); err == nil {
		t.Fatal("accepted invalid observation")
	}
}

func TestPortableObservedTypeGraphFreshProcesses(t *testing.T) {
	if os.Getenv("ELISA_FACT_CHILD") == "1" {
		source := "def main() -> i64:\n    return 1\n"
		if os.Getenv("ELISA_FACT_EDIT") == "1" {
			source = "def main() -> i64:\n    return 2\n"
		}
		if os.Getenv("ELISA_FACT_EDIT") == "2" {
			source = "def helper() -> i64:\n    return 0\n\ndef main() -> i64:\n    return 1\n"
		}
		result := parseAndAnalyzeBackendTest(t, "portable-child.elisa", source)
		fn := result.ActiveFile().Decls[len(result.ActiveFile().Decls)-1].(*ast.FuncDecl)
		expr := fn.Body[0].(*ast.ReturnStmt).Value
		observer, err := newTypeGraphObserverForFunctions([]*ast.FuncDecl{fn})
		if err != nil {
			t.Fatal(err)
		}
		semantic.NewCodegenReadView(result, observer.observe).LookupExprTypes(expr)
		facts, err := portableLocatedTypeGraphFacts(result.ActiveFile(), fn, observer.snapshot())
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(facts)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(os.Getenv("ELISA_FACT_OUTPUT"), data, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	run := func(flag string) []byte {
		path := filepath.Join(t.TempDir(), "fact.json")
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPortableObservedTypeGraphFreshProcesses$")
		cmd.Env = append(os.Environ(), "ELISA_FACT_CHILD=1", "ELISA_FACT_EDIT="+flag, "ELISA_FACT_OUTPUT="+path)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child failed: %v: %s", err, output)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, same, changed, reordered := run("0"), run("0"), run("1"), run("2")
	if string(first) != string(same) {
		t.Fatal("equivalent observations differ across processes")
	}
	if string(first) == string(changed) {
		t.Fatal("syntax edit failed to change observed record")
	}
	if string(first) == string(reordered) {
		t.Fatal("declaration insertion failed to change locator")
	}
}

func TestTypeGraphObserverConcurrentImmutableReads(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	typ := &semantic.BuiltinType{Name: "i64"}
	result := &semantic.Result{ExprTypes: map[ast.Expr]semantic.Type{expr: typ}}
	observer := &irTypeGraphObserver{}
	view := semantic.NewCodegenReadView(result, observer.observe)
	const workers, each = 8, 25
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for j := 0; j < each; j++ {
				view.LookupExprTypes(expr)
			}
		}()
	}
	wait.Wait()
	rows := observer.snapshot()
	if len(rows) != workers*each {
		t.Fatalf("lost observations: %d", len(rows))
	}
	for _, row := range rows {
		if !row.Valid || row.Digest != rows[0].Digest {
			t.Fatal("inconsistent immutable graph observation")
		}
	}
}

func TestTypeGraphObserverRejectsAmbiguousNodeOwner(t *testing.T) {
	expr := &ast.IntLit{Value: "1"}
	first := &ast.FuncDecl{Name: "first", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	second := &ast.FuncDecl{Name: "second", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	observer, err := newTypeGraphObserverForFunctions([]*ast.FuncDecl{first, second})
	if err != nil {
		t.Fatal(err)
	}
	result := &semantic.Result{ExprTypes: map[ast.Expr]semantic.Type{expr: &semantic.BuiltinType{Name: "i64"}}}
	semantic.NewCodegenReadView(result, observer.observe).LookupExprTypes(expr)
	rows := observer.snapshot()
	if len(rows) != 1 || rows[0].AnchorValid {
		t.Fatal("ambiguous shared node received an owner")
	}
}

func TestTypeGraphObserverParallelPartitions(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	const source = `def first(value: i64) -> i64:
    return value + 1

def second(value: i64) -> i64:
    return value * 2

def main() -> i64:
    return first(second(23))
`
	result := parseAndAnalyzeBackendTest(t, "portable-parallel.elisa", source)
	observer := &irTypeGraphObserver{}
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.irInputAudit = func(_ int, _ irInputReadReport) {}
	session.irCodegenObserver = observer.observe
	if err := session.WriteObject(result, filepath.Join(t.TempDir(), "out.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.PartitionsEmitted != 2 {
		t.Fatalf("expected two partitions: %+v", stats)
	}
	rows := observer.snapshot()
	if len(rows) == 0 {
		t.Fatal("no observation-time fingerprints from partitioned lowering")
	}
	for _, row := range rows {
		if !row.Valid {
			t.Fatalf("type graph capture declined: %s", row.Error)
		}
	}
}

func TestTypeGraphObserverParallelGenericPartitions(t *testing.T) {
	t.Setenv("ELISACORE_PARALLEL_IR", "1")
	const source = `def keep[T](value: T) -> T:
    return value

def first() -> i64:
    return keep(23)

def second() -> i64:
    return keep(24)

def main() -> i64:
    return first() + second()
`
	result := parseAndAnalyzeBackendTest(t, "portable-generic-parallel.elisa", source)
	var functions []*ast.FuncDecl
	for _, decl := range result.ActiveFile().Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			functions = append(functions, fn)
		}
	}
	observer, err := newTypeGraphObserverForFunctions(functions)
	if err != nil {
		t.Fatal(err)
	}
	session := NewIncrementalObjectSession(2, 32<<20)
	session.minWeight = 0
	session.irInputAudit = func(_ int, _ irInputReadReport) {}
	session.irCodegenObserver = observer.observe
	if err := session.WriteObject(result, filepath.Join(t.TempDir(), "out.o"), LLVMObjectEmitOptions{OptLevel: OptimizationLevel0}); err != nil {
		t.Fatal(err)
	}
	if stats := session.Stats(); stats.PartitionsEmitted < 2 {
		t.Fatalf("expected two or more partitions: %+v", stats)
	}
	rows := observer.snapshot()
	if len(rows) == 0 {
		t.Fatal("no type graph observations")
	}
	valid, declined, private, anchored := 0, 0, 0, 0
	for _, row := range rows {
		if row.AnchorValid {
			anchored++
		}
		if row.PrivateResult {
			private++
		}
		if row.Valid {
			valid++
		} else {
			declined++
			if row.Error == "" {
				t.Fatal("invalid observation without reason")
			}
		}
	}
	if valid == 0 {
		t.Fatal("all type graph observations declined")
	}
	if private == 0 {
		t.Fatal("generic specialization did not record private result provenance")
	}
	if anchored == 0 {
		t.Fatal("no canonical function anchors recorded")
	}
	t.Logf("generic partition observations: valid=%d declined=%d private-result=%d anchored=%d", valid, declined, private, anchored)
}

func TestPortableTypeGraphDetectsRecordLayoutChanges(t *testing.T) {
	fixture := func() (*ast.FuncDecl, *ast.IntLit, *semantic.StructType, *semantic.Result) {
		expr := &ast.IntLit{Value: "1"}
		fn := &ast.FuncDecl{Name: "f", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
		typ := &semantic.StructType{Name: "Record", Fields: map[string]semantic.Field{"value": {Name: "value", Type: &semantic.BuiltinType{Name: "i64"}}}}
		result := &semantic.Result{PermissionEffectsConverged: true, ExprTypes: map[ast.Expr]semantic.Type{expr: typ}}
		return fn, expr, typ, result
	}
	oldFn, oldExpr, _, oldResult := fixture()
	newFn, _, newType, newResult := fixture()
	var reads []semantic.CodegenFactRead
	semantic.NewCodegenReadView(oldResult, func(read semantic.CodegenFactRead) { reads = append(reads, read) }).LookupExprTypes(oldExpr)
	facts, err := portableLocalTypeGraphFacts(oldFn, reads)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := compareLocalTypeGraphFacts(newFn, newResult, facts); !ok || err != nil {
		t.Fatalf("equivalent record: %v %v", ok, err)
	}
	newType.Fields["value"] = semantic.Field{Name: "value", Type: &semantic.BuiltinType{Name: "u8"}}
	if ok, err := compareLocalTypeGraphFacts(newFn, newResult, facts); ok || err != nil {
		t.Fatalf("changed field type: %v %v", ok, err)
	}
	newType.Fields["value"] = semantic.Field{Name: "value", Type: &semantic.BuiltinType{Name: "i64"}}
	newType.HasAlignment, newType.Alignment = true, 16
	if ok, err := compareLocalTypeGraphFacts(newFn, newResult, facts); ok || err != nil {
		t.Fatalf("changed alignment: %v %v", ok, err)
	}
}

func TestDiagnosticTypeGraphDetectsParsedFieldReordering(t *testing.T) {
	const a = `struct Pair:
    first: u8
    second: i64

def main() -> i64:
    return 0
`
	const b = `struct Pair:
    second: i64
    first: u8

def main() -> i64:
    return 0
`
	one := parseAndAnalyzeBackendTest(t, "ordered.elisa", a)
	same := parseAndAnalyzeBackendTest(t, "ordered.elisa", a)
	reordered := parseAndAnalyzeBackendTest(t, "ordered.elisa", b)
	left, err := semantic.DiagnosticCodegenTypeFingerprint(one.NamedTypes["Pair"], one)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := semantic.DiagnosticCodegenTypeFingerprint(same.NamedTypes["Pair"], same)
	if err != nil || equal != left {
		t.Fatalf("identical parsed records: %v", err)
	}
	changed, err := semantic.DiagnosticCodegenTypeFingerprint(reordered.NamedTypes["Pair"], reordered)
	if err != nil || changed == left {
		t.Fatalf("field order was not detected: %v", err)
	}
}

func TestDiagnosticTypeGraphRecursiveAndMissingInputs(t *testing.T) {
	fixture := func() *semantic.StructType {
		typ := &semantic.StructType{Name: "Node", Fields: map[string]semantic.Field{}}
		typ.Fields["next"] = semantic.Field{Name: "next", Type: &semantic.RefType{Elem: typ}}
		return typ
	}
	result := &semantic.Result{PermissionEffectsConverged: true}
	first, second := fixture(), fixture()
	a, errA := semantic.DiagnosticCodegenTypeFingerprint(first, result)
	b, errB := semantic.DiagnosticCodegenTypeFingerprint(second, result)
	if (errA == nil) != (errB == nil) {
		t.Fatalf("inconsistent handling of equivalent recursive graphs: %v, %v", errA, errB)
	}
	if errA == nil {
		t.Log("recursive type graph was fingerprinted")
		if a != b {
			t.Fatal("equivalent recursive graphs differ")
		}
		second.Fields["extra"] = semantic.Field{Name: "extra", Type: &semantic.BuiltinType{Name: "u8"}}
		changed, err := semantic.DiagnosticCodegenTypeFingerprint(second, result)
		if err != nil || changed == a {
			t.Fatalf("recursive graph change not detected: %v", err)
		}
	} else {
		t.Logf("recursive type graph declined: %v", errA)
	}
	if _, err := semantic.DiagnosticCodegenTypeFingerprint(nil, result); err == nil {
		t.Fatal("accepted missing type")
	}
	if _, err := semantic.DiagnosticCodegenTypeFingerprint(first, nil); err == nil {
		t.Fatal("accepted missing result")
	}
}

func TestDiagnosticTypeGraphNestedFunctionRequiresConvergedEffects(t *testing.T) {
	callback := &semantic.FuncType{Return: &semantic.BuiltinType{Name: "i64"}}
	record := &semantic.StructType{Name: "Holder", Fields: map[string]semantic.Field{"callback": {Name: "callback", Type: callback}}}
	result := &semantic.Result{}
	if _, err := semantic.DiagnosticCodegenTypeFingerprint(record, result); err == nil {
		t.Fatal("accepted nested function with unconverged effects")
	}
	result.PermissionEffectsConverged = true
	before, err := semantic.DiagnosticCodegenTypeFingerprint(record, result)
	if err != nil {
		t.Fatal(err)
	}
	callback.FastMath = true
	after, err := semantic.DiagnosticCodegenTypeFingerprint(record, result)
	if err != nil || before == after {
		t.Fatalf("nested function change not detected: %v", err)
	}
}

func TestPortableOwnMapPresenceDistinguishesNilAndEmpty(t *testing.T) {
	old := &semantic.Result{}
	var read semantic.CodegenFactRead
	view := semantic.NewCodegenReadView(old, func(v semantic.CodegenFactRead) { read = v })
	view.HasOwnPermGrowthOpsMap()
	fact, err := portableOwnMapPresence(read)
	if err != nil {
		t.Fatal(err)
	}
	current := &semantic.Result{}
	if ok, err := compareOwnMapPresence(current, fact); !ok || err != nil {
		t.Fatalf("nil map: %v %v", ok, err)
	}
	current.PermGrowthOps = map[ast.Expr]bool{}
	if ok, err := compareOwnMapPresence(current, fact); ok || err != nil {
		t.Fatalf("empty map: %v %v", ok, err)
	}
	read.Present = true
	if _, err := portableOwnMapPresence(read); err == nil {
		t.Fatal("accepted inconsistent observation")
	}
	read.Table = "unsupported"
	if _, err := portableOwnMapPresence(read); err == nil {
		t.Fatal("accepted unsupported table")
	}
}

func TestPortableLocalFactKeysRealCodegen(t *testing.T) {
	const source = `def first(value: i64) -> i64:
    return value + 1

def main() -> i64:
    return first(23)
`
	result := parseAndAnalyzeBackendTest(t, "portable-facts.elisa", source)
	other := parseAndAnalyzeBackendTest(t, "portable-facts.elisa", source)
	observer := &irTypeGraphObserver{}
	recorder := &irInputReadRecorder{codegenObserver: observer.observe}
	g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) { g.inputReads = recorder })
	if err != nil {
		t.Fatal(err)
	}
	defer g.dispose()
	if err := g.verify(); err != nil {
		t.Fatal(err)
	}
	report := recorder.snapshot()
	observed := observer.snapshot()
	if len(observed) == 0 {
		t.Fatal("no type graphs were captured during real lowering")
	}
	for _, row := range observed {
		if !row.Valid {
			t.Logf("type graph capture declined: %s", row.Error)
		}
	}
	if len(report.Uncovered) == 0 {
		t.Fatal("diagnostic observations unexpectedly grant reuse authority")
	}
	var functions []*ast.FuncDecl
	for _, decl := range result.ActiveFile().Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			functions = append(functions, fn)
		}
	}
	resolved, rejected := 0, 0
	builtinValues, otherTypeValues, comparableBuiltins, graphMatches, graphDeclines := 0, 0, 0, 0, 0
	byKind := make(map[string]int)
	for _, read := range report.Codegen {
		byKind[read.Table+"/"+read.Operation]++
		if read.Table == "ExprTypes" && (read.Operation == "lookup" || read.Operation == "value") {
			if _, ok := read.Value.(*semantic.BuiltinType); ok {
				builtinValues++
			} else {
				otherTypeValues++
				t.Logf("unsupported expression type: %T present=%v", read.Value, read.Present)
			}
		}
		matched := 0
		for _, fn := range functions {
			if _, err := portableLocalFactKeys(fn, []semantic.CodegenFactRead{read}); err == nil {
				matched++
			}
			if read.Table == "ExprTypes" {
				if facts, err := portableLocalBuiltinTypeFacts(fn, []semantic.CodegenFactRead{read}); err == nil {
					if ok, err := compareLocalBuiltinTypeFacts(fn, read.Result, facts); err == nil && ok {
						comparableBuiltins++
					}
				}
				if facts, err := portableLocalTypeGraphFacts(fn, []semantic.CodegenFactRead{read}); err == nil {
					for _, decl := range other.ActiveFile().Decls {
						if otherFn, ok := decl.(*ast.FuncDecl); ok && otherFn.Name == fn.Name {
							if equal, err := compareLocalTypeGraphFacts(otherFn, other, facts); err == nil && equal {
								graphMatches++
							} else {
								graphDeclines++
								t.Logf("type graph comparison declined: type=%T match=%v error=%v", read.Value, equal, err)
							}
						}
					}
				}
			}
		}
		if matched > 1 {
			t.Fatalf("fact key resolved to multiple functions: %#v", read)
		}
		if matched == 1 {
			resolved++
		} else {
			rejected++
		}
	}
	if resolved == 0 || rejected == 0 {
		t.Fatalf("expected both local and unrepresentable reads: resolved=%d rejected=%d", resolved, rejected)
	}
	if comparableBuiltins == 0 {
		t.Fatal("no actual builtin observations could be compared")
	}
	if graphMatches != builtinValues+otherTypeValues || graphDeclines != 0 {
		t.Fatalf("independent type graphs did not match: matches=%d declines=%d observations=%d", graphMatches, graphDeclines, builtinValues+otherTypeValues)
	}
	t.Logf("codegen observations: resolved keys=%d rejected keys=%d builtin types=%d comparable builtins=%d other types=%d graph matches=%d declines=%d kinds=%v", resolved, rejected, builtinValues, comparableBuiltins, otherTypeValues, graphMatches, graphDeclines, byKind)
}
