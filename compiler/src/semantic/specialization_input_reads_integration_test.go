//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"strings"
	"testing"
)

func TestSpecializationInputObservationsSurviveRealSharedMemoHit(t *testing.T) {
	// A short-lived view is passed beside a long-lived mutable sink. The
	// ordinary helper's precise store-flow summary must be consulted to show
	// that it does not retain the view. A scalar identity call avoids that path.
	root := analyzeTreeTestSource(t, "specialization_input_reads.elisa", `def inspect_without_retaining(value: sview, sink: mutable darray[sview]&) -> void:
    sink.truncate(0)
    _ = value.len

def first[T](value: T, inspected: mutable darray[sview]&) -> T can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        region short(4096):
            bytes: mutable darray[u8] @short = [65]
            inspect_without_retaining(bytes.as_sview(), inspected)
    return value
`)
	if len(root.Errors()) != 0 {
		t.Fatal(root.Errors())
	}
	fn := root.File.Decls[1].(*ast.FuncDecl)
	helper := root.File.Decls[0].(*ast.FuncDecl)
	canonical := map[*ast.FuncDecl]bool{}
	for _, decl := range root.File.Decls {
		if f, ok := decl.(*ast.FuncDecl); ok {
			canonical[f] = true
		}
	}
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	args := []Type{root.NamedTypes["i64"]}
	plain, err := root.SpecializeFunction(fn, args)
	if err != nil {
		t.Fatal(err)
	}
	if _, observed := plain.InputReadObservations(); observed {
		t.Fatal("ordinary specialization enabled tracing")
	}
	first, err := root.SpecializeFunctionWithInputAudit(fn, args)
	if err != nil {
		t.Fatal(err)
	}
	if first == plain {
		t.Fatal("observed request reused unobserved product")
	}
	reads, observed := first.InputReadObservations()
	if !observed {
		t.Fatal("missing real product trace")
	}
	uses := map[string]bool{}
	for _, read := range reads {
		uses[read.Use] = true
		if read.Declaration != nil && !canonical[read.Declaration] {
			t.Fatalf("private declaration escaped trace: %s", read.Declaration.Name)
		}
	}
	foreignRead := false
	foreignSummary := false
	for _, read := range reads {
		if read.Declaration == helper {
			foreignRead = foreignRead || read.Use == "read-declaration"
			foreignSummary = foreignSummary || strings.Contains(read.Use, "summary")
		}
	}
	summary := false
	for use := range uses {
		summary = summary || strings.Contains(use, "summary") || strings.Contains(use, "origin-query")
	}
	if !uses["copy-declaration"] || !uses["read-declaration"] || !summary || !foreignRead || !foreignSummary {
		t.Fatalf("fixture did not exercise copy/read/summary observations: %+v", reads)
	}
	second, err := first.Result.SpecializeFunctionWithInputAudit(first.Decl, args)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("second request missed shared canonical memo")
	}
	replay, observed := second.InputReadObservations()
	if !observed || !reflect.DeepEqual(reads, replay) {
		t.Fatal("memo hit lost immutable input observations")
	}
	if !reflect.DeepEqual(first.Decl, plain.Decl) {
		t.Fatal("diagnostic specialization changed rewritten syntax")
	}
}
