//go:build cgo

package backend

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

func TestIRInputBodyRecorderIncludesRejectedOrdinaryWrapper(t *testing.T) {
	decl := &ast.FuncDecl{Name: "wrapper", Params: []ast.ParamDecl{{Name: "left"}, {Name: "right"}}}
	scope := semantic.NewScope(nil)
	scope.Symbols[decl.Name] = &semantic.Symbol{Name: decl.Name, Node: decl}
	recorder := &irInputReadRecorder{}
	g := &llvmGenerator{result: &semantic.Result{GlobalScope: scope}, inputReads: recorder}
	state := &functionState{g: g}
	if _, _, _, accepted := state.specializedStringViewLiteralCallShape("wrapper"); accepted {
		t.Fatal("empty wrapper unexpectedly accepted")
	}
	if _, _, _, accepted := state.specializedStringViewLiteralCallShape("absent"); accepted {
		t.Fatal("absent wrapper unexpectedly accepted")
	}
	report := recorder.snapshot()
	if len(report.Bodies) != 1 || report.Bodies[0].Declaration != decl || report.Bodies[0].Use != "literal-wrapper-stability" {
		t.Fatal("rejected wrapper body was omitted from input observations")
	}
	if len(report.Queries) != 2 || !report.Queries[0].Found || report.Queries[1].Found {
		t.Fatal("positive or negative wrapper lookup was omitted")
	}
}

func TestIRInputAuditRecordsExprFactsFromLowerer(t *testing.T) {
	left, right := &ast.Ident{Name: "left"}, &ast.Ident{Name: "right"}
	result := &semantic.Result{ExprFacts: map[ast.Expr]semantic.OptimizationFacts{
		left:  {},
		right: {},
	}}
	plainState := &functionState{g: &llvmGenerator{result: result}}
	plain := plainState.memcpyOperandsAreDisjoint(left, right)
	if plain {
		t.Fatal("empty optimization facts unexpectedly proved disjointness")
	}
	recorder := &irInputReadRecorder{}
	auditedState := &functionState{g: &llvmGenerator{result: result, inputReads: recorder}}
	audited := auditedState.memcpyOperandsAreDisjoint(left, right)
	if audited != plain {
		t.Fatal("diagnostic read recording changed a real lowerer predicate")
	}
	lookups := map[ast.Expr]bool{}
	for _, read := range recorder.snapshot().Codegen {
		if read.Result != result || read.Table != "ExprFacts" {
			continue
		}
		if read.Operation == "lookup" && read.Present {
			if expr, ok := read.Key.(ast.Expr); ok {
				lookups[expr] = true
			}
		}
	}
	if !lookups[left] || !lookups[right] {
		t.Fatalf("lowerer optimization query did not record both Result-context ExprFacts rows: %+v", recorder.snapshot().Codegen)
	}
}

func TestIRInputAuditRealGenericLoweringParity(t *testing.T) {
	const source = `def keep[T](value: T) -> T:
    return value

def main() -> i64:
    return keep(23)
`
	result := parseAndAnalyzeBackendTest(t, "input-audit-generic.elisa", source)
	var tracedReport irInputReadReport
	compile := func(traced bool) string {
		recorder := (*irInputReadRecorder)(nil)
		if traced {
			recorder = &irInputReadRecorder{}
		}
		g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) { g.inputReads = recorder })
		if err != nil {
			t.Fatal(err)
		}
		defer g.dispose()
		if err := g.verify(); err != nil {
			t.Fatal(err)
		}
		if traced {
			tracedReport = recorder.snapshot()
		}
		return g.printModule()
	}
	plain, traced := compile(false), compile(true)
	if plain != traced {
		t.Fatal("diagnostic generic lowering changed LLVM IR")
	}
	if len(tracedReport.Semantic) == 0 || len(tracedReport.Codegen) == 0 || len(tracedReport.Uncovered) == 0 {
		t.Fatal("real generic lowering did not retain partial semantic observations")
	}
	for _, read := range tracedReport.Codegen {
		if read.Result == nil || read.Table == "" {
			t.Fatalf("semantic getter observation lost table or Result identity: %+v", read)
		}
	}
}

func TestIRInputAuditCoversEveryO0UnitAndDefaultO3WholeModule(t *testing.T) {
	unsetDiskPresetFlags(t)
	source := "def keep[T](value: T) -> T:\n    return value\n\n" + incrementalObjectFixture(16)
	source = strings.Replace(source, "return work0(20)", "return keep(work0(20))", 1)
	for _, level := range []OptimizationLevel{OptimizationLevel0, OptimizationLevel3} {
		t.Run(fmt.Sprint(level), func(t *testing.T) {
			makeSession := func() *IncrementalObjectSession {
				s := NewIncrementalObjectSession(4, 32<<20)
				s.minWeight = 0
				return s
			}
			plain, audited := makeSession(), makeSession()
			result := parseAndAnalyzeBackendTest(t, "input-audit-units.elisa", source)
			options := LLVMObjectEmitOptions{OptLevel: level}
			if level == OptimizationLevel3 && audited.partitionedIRApplies(result, options) {
				t.Fatal("default O3 quality boundary changed")
			}
			reports := map[int]irInputReadReport{}
			var mu sync.Mutex
			audited.irInputAudit = func(unit int, report irInputReadReport) {
				mu.Lock()
				defer mu.Unlock()
				if _, exists := reports[unit]; exists {
					t.Errorf("unit %d observed twice", unit)
				}
				reports[unit] = report
			}
			dir := t.TempDir()
			plainObject, auditObject := filepath.Join(dir, "plain.o"), filepath.Join(dir, "audit.o")
			if err := plain.WriteObject(result, plainObject, options); err != nil {
				t.Fatal(err)
			}
			if err := audited.WriteObject(parseAndAnalyzeBackendTest(t, "input-audit-units.elisa", source), auditObject, options); err != nil {
				t.Fatal(err)
			}
			a, err := os.ReadFile(plainObject)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(auditObject)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Fatal("diagnostic trace changed native object bytes")
			}
			want := []int{-1}
			if level == OptimizationLevel0 {
				want = []int{0, 1, 2, 3}
			}
			if len(reports) != len(want) {
				t.Fatalf("wrong lowering units: %+v", reports)
			}
			semanticReads := 0
			codegenReads := 0
			seenASTTable := false
			for _, unit := range want {
				report, ok := reports[unit]
				if !ok || !report.Lowered || len(report.Uncovered) == 0 {
					t.Fatalf("unit %d missing successful incomplete report: %+v", unit, report)
				}
				semanticReads += len(report.Semantic)
				if len(report.Codegen) == 0 {
					t.Fatalf("unit %d has no AST-table observations", unit)
				}
				for _, read := range report.Codegen {
					if read.Result == nil || read.Table == "" {
						t.Fatalf("unit %d has an observation without table/Result identity: %+v", unit, read)
					}
					seenASTTable = true
					codegenReads++
				}
			}
			if semanticReads == 0 {
				t.Fatal("generic specialization was not observed in actual object pipeline")
			}
			if codegenReads == 0 || !seenASTTable {
				t.Fatal("AST-keyed semantic reads were not observed in actual object pipeline")
			}
			if plain.irInputAudit != nil {
				t.Fatal("ordinary session unexpectedly enabled tracing")
			}
		})
	}
}

// A private codegen overlay may hide a canonical row. The audit must see the
// writes and that hidden read rather than reporting a canonical lookup.
func TestIRInputAuditRecordsExprOverlayEffects(t *testing.T) {
	expr := &ast.Ident{Name: "value"}
	original := &semantic.BuiltinType{Name: "i64"}
	replacement := &semantic.BuiltinType{Name: "u64"}
	result := &semantic.Result{ExprTypes: map[ast.Expr]semantic.Type{expr: original}}
	recorder := &irInputReadRecorder{}
	g := &llvmGenerator{result: result, inputReads: recorder, sharedResult: true}
	g.setResultExprType(expr, replacement)
	if got, ok := g.resultExprTypeOK(expr); !ok || got != replacement {
		t.Fatal("overlay write not visible")
	}
	g.deleteResultExprType(expr)
	if _, ok := g.resultExprTypeOK(expr); ok {
		t.Fatal("overlay delete did not hide canonical row")
	}
	if result.ExprTypes[expr] != original {
		t.Fatal("private effects changed the canonical row")
	}
	reads := recorder.snapshot().Codegen
	want := []string{"overlay-write", "overlay-lookup", "overlay-delete", "overlay-lookup"}
	if len(reads) != len(want) {
		t.Fatalf("wrong event count: %+v", reads)
	}
	for i, op := range want {
		if reads[i].Operation != op || reads[i].Key != expr || reads[i].Result != result {
			t.Fatalf("event %d: %+v", i, reads[i])
		}
	}
	if reads[0].Value != replacement || !reads[1].Present || reads[3].Present {
		t.Fatal("audit lost write value or hidden read")
	}
}

func TestIRInputAuditRecordsDirectExprEffects(t *testing.T) {
	if forceIROverlay {
		t.Skip("serial overlay explicitly enabled")
	}
	expr := &ast.Ident{Name: "value"}
	typ := &semantic.BuiltinType{Name: "i64"}
	result := &semantic.Result{ExprTypes: map[ast.Expr]semantic.Type{}}
	recorder := &irInputReadRecorder{}
	g := &llvmGenerator{result: result, inputReads: recorder}
	g.setResultExprType(expr, typ)
	if got, ok := g.resultExprTypeOK(expr); !ok || got != typ {
		t.Fatal("direct write not visible")
	}
	g.deleteResultExprType(expr)
	if _, ok := result.ExprTypes[expr]; ok {
		t.Fatal("direct delete did not remove row")
	}
	reads := recorder.snapshot().Codegen
	if len(reads) != 3 || reads[0].Operation != "write" || reads[1].Operation != "lookup" || reads[2].Operation != "delete" {
		t.Fatalf("direct effect sequence: %+v", reads)
	}
}

func TestIRInputAuditRecordsIndexProofAuthority(t *testing.T) {
	for _, proved := range []bool{false, true} {
		expr := &ast.IndexExpr{}
		result := &semantic.Result{IndexBoundsProven: map[*ast.IndexExpr]bool{expr: proved}}
		plain := (&functionState{g: &llvmGenerator{result: result}}).indexBoundsProven(expr)
		recorder := &irInputReadRecorder{}
		traced := (&functionState{g: &llvmGenerator{result: result, inputReads: recorder}}).indexBoundsProven(expr)
		if traced != plain || traced != proved {
			t.Fatal("proof observations changed the bounds decision")
		}
		seen := map[string]bool{}
		for _, read := range recorder.snapshot().Codegen {
			if read.Table == "IndexProofSpan" && read.Key == expr && read.Result == result {
				seen[read.Operation] = true
				if read.Operation == "decision" && read.Value != proved {
					t.Fatal("incorrect recorded decision")
				}
			}
		}
		if !seen["owner"] || !seen["decision"] || (!proved && !seen["blocked"]) {
			t.Fatalf("missing index proof authority observations: %v", seen)
		}
	}
}

func TestIRInputAuditIndexedLoweringParity(t *testing.T) {
	const source = `def lookup(xs: array[i64, 3], index: usize) -> i64 can[Abort.Panic]:
    return xs[index] + xs[1]
`
	result := parseAndAnalyzeBackendTest(t, "input-audit-index.elisa", source)
	var report irInputReadReport
	compile := func(traced bool) string {
		var recorder *irInputReadRecorder
		if traced {
			recorder = &irInputReadRecorder{}
		}
		g, err := compileLLVMModuleConfigured(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", false, false, func(g *llvmGenerator) { g.inputReads = recorder })
		if err != nil {
			t.Fatal(err)
		}
		defer g.dispose()
		if err := g.verify(); err != nil {
			t.Fatal(err)
		}
		if traced {
			report = recorder.snapshot()
		}
		return g.printModule()
	}
	plain, traced := compile(false), compile(true)
	if plain != traced {
		t.Fatal("index proof tracing changed LLVM IR")
	}
	decisions := 0
	for _, read := range report.Codegen {
		if read.Table == "IndexProofSpan" && read.Operation == "decision" {
			decisions++
		}
	}
	if decisions < 2 {
		t.Fatalf("expected proof decisions for both indexes, got %d", decisions)
	}
}

func TestIRInputAuditSnapshotsSharedIndexSpans(t *testing.T) {
	pos := lexer.Pos{File: "index.elisa", Line: 2, Col: 3, Offset: 7}
	proven := &ast.IndexExpr{Position: pos}
	result := &semantic.Result{IndexBoundsProven: map[*ast.IndexExpr]bool{proven: true}}
	recorder := &irInputReadRecorder{}
	g := &llvmGenerator{result: result, sharedLookups: newSharedCodegenLookups(result), sharedResult: true, inputReads: recorder}
	state := &functionState{g: g}
	if !state.indexBoundsProven(&ast.IndexExpr{Position: pos}) {
		t.Fatal("lost rebuilt index proof")
	}
	missing := pos
	missing.Offset++
	if state.indexBoundsProven(&ast.IndexExpr{Position: missing}) {
		t.Fatal("accepted a different position")
	}
	found, absent := false, false
	for _, read := range recorder.snapshot().Codegen {
		if read.Table != "IndexProofSpan" || read.Operation != "shared-span" || read.Result != result {
			continue
		}
		if read.Key == pos && read.Present && read.Value == true {
			found = true
		}
		if read.Key == missing && !read.Present && read.Value == false {
			absent = true
		}
	}
	if !found || !absent {
		t.Fatalf("missing positive or negative span query: found=%v absent=%v", found, absent)
	}
}
