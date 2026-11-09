package backend

import (
	"context"
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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
