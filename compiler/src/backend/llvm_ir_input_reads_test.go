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
