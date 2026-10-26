//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func returnElementStatements(fn *ast.FuncDecl) []*ast.ReturnStmt {
	var returns []*ast.ReturnStmt
	returnElementWalk(reflect.ValueOf(fn.Body), func(ret *ast.ReturnStmt) {
		returns = append(returns, ret)
	}, map[uintptr]bool{})
	return returns
}

func specializationReturnElementWorker(t *testing.T, root *Result, fn *ast.FuncDecl) (*Analyzer, *specializationContext, *ast.FuncDecl) {
	t.Helper()
	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{worker: 0, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	private := context.copyDecl(fn)
	if context.err != nil {
		t.Fatal(context.err)
	}
	return worker, context, private
}

func TestSpecializationReturnElementStmtStatesImportOnlyOwnedFacts(t *testing.T) {
	root := analyzeTreeTestSource(t, "specialization_return_element_stmt_states.elisa", `def state_owner[T](value: T) -> T:
    return value

def foreign(value: i64) -> i64:
    return value
`)
	target := testFuncDeclByName(t, root.File, "state_owner")
	foreign := testFuncDeclByName(t, root.File, "foreign")
	targetReturn, foreignReturn := returnElementStatements(target)[0], returnElementStatements(foreign)[0]
	// Keep three distinct owned nodes: one row whose empty value is meaningful,
	// one nested fact row that must be isolated, and one absent row that must
	// remain absent after the ownership projection.
	nestedReturn, absentReturn := &ast.ReturnStmt{}, &ast.ReturnStmt{}
	target.Body = append(target.Body, nestedReturn, absentReturn)
	nested := regionRefState{Fields: map[string]regionRefState{
		"child": {Fields: map[string]regionRefState{
			"leaf": {ParamDeps: intBitSetOf(130, 131)},
		}},
	}}
	root.analyzer.returnElementStmtStates = map[*ast.ReturnStmt]regionRefState{
		targetReturn:  {},
		nestedReturn:  nested,
		foreignReturn: nested,
	}
	root.analyzer.returnElementSummaries = map[*ast.FuncDecl]regionRefState{foreign: nested}
	for _, source := range []map[*ast.ReturnStmt]regionRefState{nil, {}} {
		clone := cloneAnalyzerValueForSpecialization(&Analyzer{returnElementStmtStates: source}).Addr().Interface().(*Analyzer)
		if (source == nil) != (clone.returnElementStmtStates == nil) || len(clone.returnElementStmtStates) != 0 {
			t.Fatal("return-statement state clone changed nil/empty shape")
		}
	}

	worker := cloneAnalyzerValueForSpecialization(root.analyzer).Addr().Interface().(*Analyzer)
	if worker.returnElementStmtStates == nil || len(worker.returnElementStmtStates) != 0 {
		t.Fatal("specialization copied canonical return-statement state rows")
	}
	if !reflect.DeepEqual(worker.returnElementSummaries[foreign], root.analyzer.returnElementSummaries[foreign]) {
		t.Fatal("return-element summary cloning changed")
	}
	isolateWorkerScopes(root.analyzer, worker)
	context := &specializationContext{base: root, worker: worker, syntax: newSpecializationSyntax(), declarations: map[*ast.FuncDecl]*ast.FuncDecl{}}
	worker.parallel = &parallelWorkerState{shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, readShadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}, specialization: context}
	context.copyDecl(target)
	if context.err != nil {
		t.Fatal(context.err)
	}
	privateEmpty, ok := context.syntax.nodes[targetReturn].(*ast.ReturnStmt)
	if !ok {
		t.Fatal("owned empty-state return was not copied")
	}
	privateNested, ok := context.syntax.nodes[nestedReturn].(*ast.ReturnStmt)
	if !ok {
		t.Fatal("owned nested-state return was not copied")
	}
	privateAbsent, ok := context.syntax.nodes[absentReturn].(*ast.ReturnStmt)
	if !ok {
		t.Fatal("owned absent-state return was not copied")
	}
	if state, present := worker.returnElementStmtStates[privateEmpty]; !present || len(state.Fields) != 0 {
		t.Fatal("present empty state was not preserved")
	}
	privateState, present := worker.returnElementStmtStates[privateNested]
	if !present || !privateState.Fields["child"].Fields["leaf"].ParamDeps.Contains(130) {
		t.Fatal("nested state was not imported")
	}
	if _, present := worker.returnElementStmtStates[privateAbsent]; present {
		t.Fatal("copyDecl synthesized a missing owned state row")
	}
	if _, present := worker.returnElementStmtStates[targetReturn]; present {
		t.Fatal("canonical owned key remained in the private map")
	}
	if _, present := worker.returnElementStmtStates[foreignReturn]; present {
		t.Fatal("foreign return state was imported")
	}
	if len(worker.returnElementStmtStates) != 2 {
		t.Fatalf("imported %d return rows, want exactly 2", len(worker.returnElementStmtStates))
	}
	privateState.Fields["private_only"] = regionRefState{}
	child := privateState.Fields["child"]
	leaf := child.Fields["leaf"]
	leaf.ParamDeps.extra[1] ^= uint64(1) << 2 // clear parameter 130 in the private slice
	if _, present := nested.Fields["private_only"]; present || !nested.Fields["child"].Fields["leaf"].ParamDeps.Contains(130) {
		t.Fatal("imported nested maps or slices alias canonical state")
	}

	ordinary := cloneAnalyzerValueForBodyWorker(root.analyzer).Addr().Interface().(*Analyzer)
	if len(ordinary.returnElementStmtStates) != len(root.analyzer.returnElementStmtStates) {
		t.Fatal("ordinary body worker lost return-statement state rows")
	}
	ordinaryState := ordinary.returnElementStmtStates[nestedReturn]
	ordinaryState.Fields["ordinary_only"] = regionRefState{}
	if _, present := root.analyzer.returnElementStmtStates[nestedReturn].Fields["ordinary_only"]; present {
		t.Fatal("ordinary worker nested map isolation changed")
	}
}

func TestSpecializationReanalyzesMultiReturnElementDependencies(t *testing.T) {
	root := analyzeTreeTestSource(t, "specialization_multi_return_elements.elisa", `def choose[T, @r](values: mutable darray[sview] @r, empty: bool, marker: T) -> darray[sview] @r:
    can Memory.Allocate, Abort.Panic:
        copied: mutable darray[sview] @r = []
        for value in values |copied|:
            copied.push(value)
        if empty:
            return []
        return copied
`)
	fn := testFuncDeclByName(t, root.File, "choose")
	returns := returnElementStatements(fn)
	if len(returns) != 2 {
		t.Fatalf("fixture has %d returns, want empty and container returns", len(returns))
	}
	var emptyReturn, containerReturn *ast.ReturnStmt
	for _, ret := range returns {
		switch ret.Value.(type) {
		case *ast.ListLitExpr:
			emptyReturn = ret
		case *ast.Ident:
			containerReturn = ret
		}
	}
	if emptyReturn == nil || containerReturn == nil {
		t.Fatal("fixture does not contain the expected empty and container return forms")
	}
	check := func(states map[*ast.ReturnStmt]regionRefState, empty, container *ast.ReturnStmt) {
		t.Helper()
		if state, present := states[empty]; !present || len(state.Fields) != 0 || regionRefStateHasParamDep(state, 0) {
			t.Fatal("empty return lost its present-empty element state")
		}
		if state, present := states[container]; !present || !regionRefStateHasParamDep(state, 0) {
			t.Fatal("container return lost its dependency on the values parameter")
		}
	}
	check(root.analyzer.returnElementStmtStates, emptyReturn, containerReturn)

	worker, context, private := specializationReturnElementWorker(t, root, fn)
	defer worker.closeSMT()
	privateEmpty := context.syntax.nodes[emptyReturn].(*ast.ReturnStmt)
	privateContainer := context.syntax.nodes[containerReturn].(*ast.ReturnStmt)
	check(worker.returnElementStmtStates, privateEmpty, privateContainer)
	worker.analyzeFuncWithTypeArgs(private, []Type{root.NamedTypes["i64"]})
	for _, diagnostic := range worker.diagnostics {
		if diagnostic.Severity == DiagnosticSeverityError {
			t.Fatalf("specialized multi-return body failed: %s", diagnostic.String())
		}
	}
	check(worker.returnElementStmtStates, privateEmpty, privateContainer)
	if _, present := worker.returnElementStmtStates[emptyReturn]; present {
		t.Fatal("specialized state remained keyed by canonical return syntax")
	}
}

func TestSpecializationCallerConsumesForeignReturnElementSummary(t *testing.T) {
	root := analyzeTreeTestSourceWithSemanticErrors(t, "specialization_foreign_return_element_summary.elisa", `def inner(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        roots: mutable darray[sview] = []
        for name in names |roots|:
            roots.push(name)
        return roots

def caller[T](names: darray[sview]&, dst: mutable darray[sview]&, marker: T) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[sview] = []
        text: dstr = f"x{names[0]}"
        local.push(text.as_sview())
        returned: darray[sview] = inner(local)
        for value in returned |dst|:
            dst.push(value)
`)
	inner := testFuncDeclByName(t, root.File, "inner")
	caller := testFuncDeclByName(t, root.File, "caller")
	canonicalSummary, present := root.analyzer.returnElementSummaries[inner]
	if !present || !regionRefStateHasParamDep(canonicalSummary, 0) {
		t.Fatal("foreign helper did not publish its parameter-dependent element summary")
	}
	worker, _, private := specializationReturnElementWorker(t, root, caller)
	defer worker.closeSMT()
	if summary, present := worker.returnElementSummaries[inner]; !present || !regionRefStateHasParamDep(summary, 0) {
		t.Fatal("specialization lost the foreign return-element summary")
	}
	worker.analyzeFuncWithTypeArgs(private, []Type{root.NamedTypes["i64"]})
	var diagnostics []string
	for _, diagnostic := range worker.diagnostics {
		if diagnostic.Severity == "" || diagnostic.Severity == DiagnosticSeverityError {
			diagnostics = append(diagnostics, diagnostic.String())
		}
	}
	joined := strings.Join(diagnostics, "\n")
	if !strings.Contains(joined, "is stored into longer-lived region") {
		t.Fatalf("specialized caller failed to reject foreign-summary elements containing a frame-local view: %s", joined)
	}
	if !reflect.DeepEqual(worker.returnElementSummaries[inner], canonicalSummary) {
		t.Fatal("private caller analysis changed the canonical foreign summary")
	}
}

var specializationReturnElementStmtCloneSink *Analyzer

func BenchmarkSpecializationSyntheticReturnElementStmtStateClone(b *testing.B) {
	for _, foreignRows := range []int{0, 290} {
		root := &Analyzer{returnElementStmtStates: make(map[*ast.ReturnStmt]regionRefState, foreignRows)}
		for i := 0; i < foreignRows; i++ {
			root.returnElementStmtStates[&ast.ReturnStmt{}] = regionRefState{Fields: map[string]regionRefState{
				"nested": {ParamDeps: intBitSetOf(130)},
			}}
		}
		b.Run(fmt.Sprintf("synthetic_foreign_rows_%d", foreignRows), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				specializationReturnElementStmtCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
			}
		})
	}
}
