package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestSpecializationInputObservationsCanonicalAndImmutable(t *testing.T) {
	canonical, private := &ast.FuncDecl{}, &ast.FuncDecl{}
	worker := &Analyzer{parallel: &parallelWorkerState{original: map[*ast.FuncDecl]*ast.FuncDecl{private: canonical}}}
	ctx := &specializationContext{worker: worker, inputReads: &specializationInputTrace{}}
	ctx.observeInputRead(private, "read-declaration")
	ctx.observeInputRead(canonical, "read-declaration")
	ctx.observeInputRead(nil, "summary-query-unresolved-provenance")
	if len(ctx.inputReads.reads) != 2 {
		t.Fatalf("missing canonical dedup or negative query: %+v", ctx.inputReads.reads)
	}
	product := &SpecializedFunction{inputReads: append([]SpecializationInputRead(nil), ctx.inputReads.reads...), inputReadsObserved: true}
	first, observed := product.InputReadObservations()
	if !observed || first[0].Declaration != canonical || first[1].Declaration != nil {
		t.Fatal("incorrect trace")
	}
	first[0].Use = "mutated"
	second, _ := product.InputReadObservations()
	if second[0].Use != "read-declaration" {
		t.Fatal("published observations alias caller memory")
	}
	key, _ := specializationMemoKey(canonical, nil)
	traced := key
	traced.inputAudit = true
	if traced == key {
		t.Fatal("diagnostic memo namespace collided with unobserved product")
	}
}

func TestSpecializationInputObservationsDisabledByDefault(t *testing.T) {
	ctx := &specializationContext{}
	ctx.observeInputRead(nil, "ignored")
	product := &SpecializedFunction{}
	reads, observed := product.InputReadObservations()
	if observed || len(reads) != 0 {
		t.Fatal("unobserved product claims a trace")
	}
}
