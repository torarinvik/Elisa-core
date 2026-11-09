package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"path/filepath"
	"sync"
	"testing"
)

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
