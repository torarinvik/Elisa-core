//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"reflect"
	"testing"
)

func TestProofBindingPositionSchemaFailClosed(t *testing.T) {
	if !proofBindingPositionSchemaMatches(reflect.TypeFor[lexer.Pos]()) {
		t.Fatal("actual schema not recognized")
	}
	for _, typ := range []reflect.Type{nil, reflect.TypeFor[string](), reflect.TypeFor[struct {
		File                                          string
		Line, Col, Offset, EndLine, EndCol, EndOffset uint
	}](), reflect.TypeFor[struct {
		File                                          string
		Line, Col, Offset, EndLine, EndCol, EndOffset int
		Extra                                         string
	}]()} {
		if proofBindingPositionSchemaMatches(typ) {
			t.Fatal("changed schema admitted", typ)
		}
	}
}
func TestProofBindingPositionLeafFullAdapterParity(t *testing.T) {
	const source = "def first() -> i64:\n    return 0\n\ndef moved() -> i64:\n    in auto:\n        return 0\n"
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	previous := proofBindingFunction(t, state.pristine, "moved")
	oldRegion := previous.Body[0].(*ast.RegionStmt)
	for _, badFilename := range []bool{false, true} {
		file := retainedParse(t, "\n"+source)
		current := proofBindingFunction(t, file, "moved")
		if badFilename {
			proofBindingFunction(t, file, "first").Position.File = oldRegion.Name
		}
		marks := map[string][][2]int{previous.Pos().File: {{1, 1}}}
		candidate := state.prepareProofReportBindings(file, marks)
		reference := state.prepareProofReportBindingsPositionReference(file, marks)
		row := proofContribution{Key: proofContributionKey{Owner: current, Phase: proofPhaseBody}, Origins: map[ast.Node]bool{current: true}}
		for _, text := range []string{"ordinary", oldRegion.Name + " == " + oldRegion.Name, fmt.Sprintf("literal == %q", oldRegion.Name), "__auto_999999 == 0", "__quantifier_999999 == 0"} {
			input := ProofFact{Subject: text, Predicate: text, KnownFacts: []string{text}, ClosedWorldFacts: []string{text}, Missing: text}
			got, gerr := candidate(row, input)
			want, werr := reference(row, input)
			if badFilename && text == oldRegion.Name+" == "+oldRegion.Name && gerr == "" {
				t.Fatal("Pos.File lookalike no longer blocks authorization")
			}
			if gerr != werr || !reflect.DeepEqual(got, want) {
				t.Fatalf("adapter changed badFilename=%v text=%q got=%#v/%s want=%#v/%s", badFilename, text, got, gerr, want, werr)
			}
		}
	}
}
func BenchmarkProofBindingPositionLeafWholeAdapter(b *testing.B) {
	var source = "def first() -> i64:\n    return 0\n\ndef moved() -> i64:\n    in auto:\n        return 0\n"
	for i := 0; i < 512; i++ {
		source += fmt.Sprintf("\ndef other_%d() -> i64:\n    return %d\n", i, i)
	}
	// Parsing is outside timing. This benchmark measures all three eager scans.
	file, why := proofReportBindingSource("benchmark.elisa", []byte(source))
	if why != "" {
		b.Fatal(why)
	}
	state := &RetainedAnalysis{a: &Analyzer{file: file}, pristine: file, src: []byte(source)}
	for _, old := range []bool{true, false} {
		name := "candidate"
		if old {
			name = "frozen_reference"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if old {
					state.prepareProofReportBindingsPositionReference(file, nil)
				} else {
					state.prepareProofReportBindings(file, nil)
				}
			}
		})
	}
}

func BenchmarkProofReportSyntaxRendererIndex(b *testing.B) {
	source := "def first() -> i64:\n    return 0\n\ndef moved() -> i64:\n    in auto:\n        return 0\n"
	for i := 0; i < 512; i++ {
		source += fmt.Sprintf("\ndef other_%d() -> i64:\n    return %d\n", i, i)
	}
	file, why := proofReportBindingSource("benchmark.elisa", []byte(source))
	if why != "" {
		b.Fatal(why)
	}
	first := proofBindingFunction(b, file, "first").Pos()
	moved := proofBindingFunction(b, file, "moved")
	region := moved.Body[0].(*ast.RegionStmt).Pos()
	requested := []proofReportCoordinateRequest{{Pos: first}, {Pos: moved.Pos()}, {Pos: region}}
	requestSet := make(map[proofReportCoordinateRequest]struct{}, len(requested))
	for _, request := range requested {
		requestSet[request] = struct{}{}
	}
	for _, bounded := range []bool{false, true} {
		name := "full"
		if bounded {
			name = "requested"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			var last lexer.Pos
			for i := 0; i < b.N; i++ {
				var render func(lexer.Pos, bool) (lexer.Pos, string)
				if bounded {
					witnesses := captureProofReportCoordinateWitnesses(file)
					render = witnesses.requestedRenderer(nil, requestSet)
				} else {
					render = proofReportSyntaxRenderer(file, nil)
				}
				for _, request := range requested {
					var err string
					last, err = render(request.Pos, request.Shift)
					if err != "" {
						b.Fatal(err)
					}
				}
			}
			_ = last
		})
	}
}
