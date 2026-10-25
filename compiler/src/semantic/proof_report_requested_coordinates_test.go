package semantic

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func proofCoordinateNamedParse(t *testing.T, name, source string) *ast.File {
	t.Helper()
	lex := lexer.New(name, []byte(source))
	parse := parser.New(lex.Tokenize())
	file := parse.ParseFile(name)
	if len(lex.Errors())+len(parse.Errors()) != 0 {
		t.Fatal("coordinate fixture parse", lex.Errors(), parse.Errors())
	}
	return file
}

func compareRequestedCoordinateOracle(t *testing.T, file *ast.File, marks map[string][][2]int, requests []proofReportCoordinateRequest) {
	t.Helper()
	oracle := proofReportSyntaxRenderer(file, marks)
	witnesses := captureProofReportCoordinateWitnesses(file)
	requestSet := make(map[proofReportCoordinateRequest]struct{}, len(requests))
	for _, request := range requests {
		requestSet[request] = struct{}{}
	}
	filtered := witnesses.requestedRenderer(marks, requestSet)
	for i, request := range requests {
		want, whyWant := oracle(request.Pos, request.Shift)
		got, whyGot := filtered(request.Pos, request.Shift)
		if got != want || whyGot != whyWant {
			t.Fatalf("request%d %#v: filtered=(%#v,%q), full=(%#v,%q)", i, request, got, whyGot, want, whyWant)
		}
	}
}

func TestRequestedProofCoordinatesMatchFullOracleAcrossSourceEpochs(t *testing.T) {
	before := "def first(value: i32) -> i32:\n    return value\ndef moved() -> i32:\n    return 7\n"
	after := "def first(value: i32) -> i32:\n    if true:\n        return value\n    return 0\ndef moved() -> i32:\n    return 7\n"
	previous := proofCoordinateNamedParse(t, "one.elisa", before)
	current := proofCoordinateNamedParse(t, "one.elisa", after)
	oldMoved := previous.Decls[1].(*ast.FuncDecl).Pos()
	marks := map[string][][2]int{"one.elisa": {{oldMoved.Line, 2}}}
	var requests []proofReportCoordinateRequest
	// Every witnessed full/start position from both epochs is tested under both
	// shift policies; errors for disappeared/mismatched anchors must match too.
	for _, file := range []*ast.File{previous, current} {
		for _, slab := range captureProofReportCoordinateWitnesses(file).slabs {
			for _, pos := range slab {
				for _, shift := range []bool{false, true} {
					requests = append(requests, proofReportCoordinateRequest{pos, shift})
					start := pos
					start.EndLine, start.EndCol, start.EndOffset = 0, 0, 0
					requests = append(requests, proofReportCoordinateRequest{start, shift})
					stale := pos
					stale.Offset += 123
					stale.EndOffset += 123
					requests = append(requests, proofReportCoordinateRequest{stale, shift})
				}
			}
		}
	}
	combined := previous.Decls[0].Pos()
	combined.EndLine, combined.EndCol, combined.EndOffset = oldMoved.Line, oldMoved.Col, oldMoved.Offset
	requests = append(requests, proofReportCoordinateRequest{combined, true}, proofReportCoordinateRequest{lexer.Pos{}, false}, proofReportCoordinateRequest{lexer.Pos{}, true})
	compareRequestedCoordinateOracle(t, current, marks, requests)
}

func TestRequestedProofCoordinatesPreserveIncludesAndUnusualSpans(t *testing.T) {
	left := proofCoordinateNamedParse(t, "left.elisa", "def first() -> i32:\n    return 1\n")
	right := proofCoordinateNamedParse(t, "right.elisa", "def second() -> i32:\n    return 2\n")
	file := &ast.File{Filename: "root.elisa", Decls: append(left.Decls, right.Decls...)}
	var requests []proofReportCoordinateRequest
	for _, slab := range captureProofReportCoordinateWitnesses(file).slabs {
		for _, p := range slab {
			requests = append(requests, proofReportCoordinateRequest{p, false})
		}
	}
	missing := left.Decls[0].Pos()
	missing.File = "absent-include.elisa"
	endMissing := left.Decls[0].Pos()
	endMissing.EndLine, endMissing.EndCol, endMissing.EndOffset = 99, 7, 987
	composed := left.Decls[0].Pos()
	end := left.Decls[0].(*ast.FuncDecl).Body[0].Pos()
	composed.EndLine, composed.EndCol, composed.EndOffset = end.Line, end.Col, end.Offset
	noEndOffset := composed
	noEndOffset.EndOffset = 0
	partialEnd := composed
	partialEnd.EndCol = 0
	requests = append(requests, proofReportCoordinateRequest{missing, false}, proofReportCoordinateRequest{endMissing, false}, proofReportCoordinateRequest{composed, false}, proofReportCoordinateRequest{noEndOffset, false}, proofReportCoordinateRequest{partialEnd, false})
	compareRequestedCoordinateOracle(t, file, nil, requests)
}

func TestRequestedProofCoordinatesPreserveEveryAmbiguityDecision(t *testing.T) {
	base := lexer.Pos{File: "ambiguous.elisa", Line: 1, Col: 1, Offset: 5, EndLine: 1, EndCol: 4, EndOffset: 8}
	for name, other := range map[string]lexer.Pos{
		"same-span-different-offset":     {File: base.File, Line: 1, Col: 1, Offset: 6, EndLine: 1, EndCol: 4, EndOffset: 9},
		"same-start-different-full-span": {File: base.File, Line: 1, Col: 1, Offset: 6, EndLine: 2, EndCol: 8, EndOffset: 25},
		"same-end-different-offset":      {File: base.File, Line: 1, Col: 9, Offset: 15, EndLine: 1, EndCol: 4, EndOffset: 9},
	} {
		t.Run(name, func(t *testing.T) {
			file := &ast.File{Decls: []ast.Decl{&ast.FuncDecl{Name: "a", Position: base}, &ast.FuncDecl{Name: "b", Position: other}}}
			composed := base
			composed.EndLine, composed.EndCol, composed.EndOffset = other.Line, other.Col, other.Offset
			start := base
			start.EndLine, start.EndCol, start.EndOffset = 0, 0, 0
			requests := []proofReportCoordinateRequest{{base, false}, {other, false}, {composed, false}, {start, false}}
			compareRequestedCoordinateOracle(t, file, nil, requests)
		})
	}
}

// Embedding retains the sealed ast.Decl methods. This independently exercises
// Node.Pos authority which does not occur in any stored lexer.Pos field.
type proofCoordinateMethodDecl struct {
	*ast.FuncDecl
	calls *int
}

func (node *proofCoordinateMethodDecl) Pos() lexer.Pos {
	*node.calls = *node.calls + 1
	pos := node.FuncDecl.Position
	pos.Line += 5
	pos.Col += 3
	pos.Offset += 17
	pos.EndLine, pos.EndCol, pos.EndOffset = 0, 0, 0
	return pos
}

func TestRequestedProofCoordinatesCaptureNodePosAndDoNotRescanLoweredSyntax(t *testing.T) {
	calls := 0
	node := &proofCoordinateMethodDecl{&ast.FuncDecl{Name: "method", Position: lexer.Pos{File: "method.elisa", Line: 1, Col: 1, Offset: 0}}, &calls}
	source := &ast.File{Decls: []ast.Decl{node}}
	request := proofReportCoordinateRequest{node.Pos(), false}
	oracle := proofReportSyntaxRenderer(source, nil)
	witnesses := captureProofReportCoordinateWitnesses(source)
	before := calls
	// Simulate source nodes becoming live and rewritten during retained analysis.
	node.FuncDecl.Position.Line = 100
	node.FuncDecl.Position.Offset = 999
	filtered := witnesses.requestedRenderer(nil, map[proofReportCoordinateRequest]struct{}{request: {}})
	got, why := filtered(request.Pos, false)
	want, whyWant := oracle(request.Pos, false)
	if got != want || why != whyWant || why != "" {
		t.Fatal("authored method witness was lost", got, why, want, whyWant)
	}
	if calls != before {
		t.Fatal("final request binding walked the mutated source AST")
	}
	if _, why := proofReportSyntaxRenderer(source, nil)(request.Pos, false); why == "" {
		t.Fatal("fixture did not distinguish authored and mutated graph authority")
	}
}

func TestRequestedProofCoordinatesRejectUncollectedQueries(t *testing.T) {
	pos := lexer.Pos{File: "one.elisa", Line: 1, Col: 1, Offset: 0}
	file := &ast.File{Decls: []ast.Decl{&ast.FuncDecl{Name: "a", Position: pos}}}
	render := captureProofReportCoordinateWitnesses(file).requestedRenderer(nil, map[proofReportCoordinateRequest]struct{}{
		{pos, false}:         {},
		{lexer.Pos{}, false}: {},
	})
	for _, request := range []proofReportCoordinateRequest{{pos, true}, {lexer.Pos{}, true}, {lexer.Pos{File: pos.File, Line: pos.Line, Col: pos.Col, Offset: 19}, false}} {
		if _, why := render(request.Pos, request.Shift); why == "" {
			t.Fatal("late query piggybacked an existing index key", request)
		}
	}
	if got, why := render(lexer.Pos{}, false); got != (lexer.Pos{}) || why != "" {
		t.Fatal("collected zero position stopped being identity")
	}
}

func TestRequestedProofCoordinateFinalCallbackIncludesNewFactsAndIsAtomic(t *testing.T) {
	oldPos := lexer.Pos{File: "final.elisa", Line: 1, Col: 1, Offset: 1}
	freshPos := oldPos
	freshPos.Offset = 11
	newPos := lexer.Pos{File: "final.elisa", Line: 5, Col: 1, Offset: 51}
	oldOwner := &ast.FuncDecl{Name: "old", Position: oldPos}
	freshOwner := &ast.FuncDecl{Name: "old", Position: freshPos}
	newOwner := &ast.FuncDecl{Name: "new", Position: newPos}
	file := &ast.File{Decls: []ast.Decl{freshOwner, newOwner}}
	oldKey := proofContributionKey{Owner: oldOwner, Phase: proofPhaseBody}
	a := &Analyzer{file: &ast.File{Decls: []ast.Decl{oldOwner}}}
	a.proofLifecycle.Segments = map[proofContributionKey]proofContribution{oldKey: {Key: oldKey, Facts: []ProofFact{{Pos: oldPos, Subject: "old"}}}}
	state := &RetainedAnalysis{a: a, pristine: a.file}
	finish, why := state.prepareProofReportCoordinates(file, nil)
	if why != "" {
		t.Fatal(why)
	}
	// A real analysis may emit an entirely new contribution after setup. Its
	// requests must join the final index rather than fall back to old-row demand.
	newKey := proofContributionKey{Owner: newOwner, Phase: proofPhaseBody}
	a.proofLifecycle.Segments[newKey] = proofContribution{Key: newKey, Facts: []ProofFact{{Pos: newPos, Subject: "new"}, {Subject: "zero"}}}
	if why := finish(a); why != "" {
		t.Fatal(why)
	}
	if a.proofLifecycle.Segments[oldKey].Facts[0].Pos != freshPos || len(a.proofLifecycle.Segments[newKey].Facts) != 2 || a.proofLifecycle.Segments[newKey].Facts[0].Pos != newPos {
		t.Fatal("complete final old/new facts were suppressed or misbound")
	}
	bad := oldPos
	bad.Line = 999
	original := []ProofFact{{Pos: oldPos, Subject: "valid"}, {Pos: bad, Subject: "missing", KnownFacts: []string{"untouched"}}}
	a.proofLifecycle.Segments = map[proofContributionKey]proofContribution{oldKey: {Key: oldKey, Facts: original}}
	finish, why = state.prepareProofReportCoordinates(file, nil)
	if why != "" {
		t.Fatal(why)
	}
	before := append([]ProofFact(nil), original...)
	if why := finish(a); why == "" || !strings.Contains(why, "edited syntax") {
		t.Fatal("missing authored anchor did not decline", why)
	}
	if !reflect.DeepEqual(a.proofLifecycle.Segments[oldKey].Facts, before) || &a.proofLifecycle.Segments[oldKey].Facts[0] != &original[0] {
		t.Fatal("failed final binding partially rewrote live proof facts")
	}
}

func TestRequestedProofCoordinatesSlabsHaveNoSourceLimit(t *testing.T) {
	file := &ast.File{}
	for i := 0; i < 3001; i++ {
		file.Decls = append(file.Decls, &ast.FuncDecl{Name: "row", Position: lexer.Pos{File: "large.elisa", Line: i + 1, Col: 1, Offset: i * 10}})
	}
	witnesses := captureProofReportCoordinateWitnesses(file)
	if len(witnesses.slabs) < 3 {
		t.Fatal("fixture did not cross allocation slabs")
	}
	for _, slab := range witnesses.slabs {
		if len(slab) > proofReportCoordinateWitnessSlabSize || cap(slab) != proofReportCoordinateWitnessSlabSize {
			t.Fatal("witness allocation slab escaped fixed capacity")
		}
	}
	requests := []proofReportCoordinateRequest{{file.Decls[0].Pos(), false}, {file.Decls[len(file.Decls)-1].Pos(), false}}
	compareRequestedCoordinateOracle(t, file, nil, requests)
}

var proofCoordinateBenchmarkSink lexer.Pos

// The saved72ca cache contains532 rows,21548 public report facts,358479 known
// evidence entries and2299 facts with a generated token. Keep the callback
// benchmark at that cardinality so request collection is visible with real payloads.
func benchmarkProofReportCoordinateCallback(b *testing.B) (*RetainedAnalysis, *ast.File, map[proofContributionKey]proofContribution) {
	b.Helper()
	const rows, factCount, generatedFactCount = 532, 21548, 2299
	const factsWithKnown, knownEntryCount = 20926, 358479
	var source strings.Builder
	functions := make([]*ast.FuncDecl, 0, rows)
	regions := make([]*ast.RegionStmt, 0, rows)
	for i := 0; i < rows; i++ {
		if i%5 == 0 {
			fmt.Fprintf(&source, "\ndef fn_%d() -> i64:\n    in auto:\n        return %d\n", i, i)
		} else {
			fmt.Fprintf(&source, "\ndef fn_%d() -> i64:\n    return %d\n", i, i)
		}
	}
	text := source.String()
	file, why := proofReportBindingSource("coordinate-callback-benchmark.elisa", []byte(text))
	if why != "" {
		b.Fatal(why)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		functions = append(functions, fn)
		var region *ast.RegionStmt
		if len(fn.Body) != 0 {
			region, _ = fn.Body[0].(*ast.RegionStmt)
		}
		regions = append(regions, region)
	}
	if len(functions) != rows {
		b.Fatalf("fixture has %d functions, want %d", len(functions), rows)
	}
	segments := make(map[proofContributionKey]proofContribution, rows)
	baseFacts, extraFacts := factCount/rows, factCount%rows
	regionCount, generatedLeft := rows/5+1, generatedFactCount
	factOrdinal, knownOrdinal, knownEntries := 0, 0, 0
	for rowIndex, fn := range functions {
		count := baseFacts
		if rowIndex < extraFacts {
			count++
		}
		generatedHere := 0
		if regions[rowIndex] != nil && generatedLeft > 0 {
			generatedHere = (generatedLeft + regionCount - 1) / regionCount
			if generatedHere > count {
				generatedHere = count
			}
			generatedLeft -= generatedHere
			regionCount--
		}
		facts := make([]ProofFact, count)
		for i := range facts {
			pos, subject := fn.Pos(), "value"
			if i < generatedHere {
				pos, subject = regions[rowIndex].Pos(), regions[rowIndex].Name
			}
			facts[i] = ProofFact{
				Pos:       pos,
				Subject:   subject,
				Predicate: "value > 0",
			}
			if factOrdinal >= factCount-factsWithKnown {
				entries := 17
				if knownOrdinal == 0 {
					entries = 114
				} else if knownOrdinal <= 2640 {
					entries = 18
				}
				facts[i].KnownFacts = make([]string, entries)
				knownEntries += entries
				for j := range facts[i].KnownFacts {
					facts[i].KnownFacts[j] = "known-proof-fact"
				}
				knownOrdinal++
			}
			factOrdinal++
		}
		key := proofContributionKey{Owner: fn, Phase: proofPhaseBody}
		segments[key] = proofContribution{Key: key, Facts: facts, Origins: map[ast.Node]bool{fn: true}}
	}
	if generatedLeft != 0 || knownOrdinal != factsWithKnown || factOrdinal != factCount || knownEntries != knownEntryCount {
		b.Fatalf("fixture census mismatch: generated=%d known-facts=%d facts=%d entries=%d", generatedLeft, knownOrdinal, factOrdinal, knownEntries)
	}
	state := &RetainedAnalysis{a: &Analyzer{}, pristine: file, src: []byte(text)}
	state.a.file = file
	state.a.proofLifecycle.Segments = segments
	return state, file, segments
}

// Frozen pre-index callback: it includes complete full-map construction, row
// replacement and binding, but no final request collection or filtered index.
func prepareProofReportCoordinatesFullReference(r *RetainedAnalysis, file *ast.File, marks map[string][][2]int) func(*Analyzer) string {
	old := make(map[proofContributionKey]proofContribution, len(r.a.proofLifecycle.Segments))
	for key, row := range r.a.proofLifecycle.Segments {
		old[key] = row
	}
	sameFacts := func(left, right []ProofFact) bool {
		return len(left) != 0 && len(left) == len(right) && &left[0] == &right[0]
	}
	render := proofReportSyntaxRenderer(file, marks)
	renderBindings := r.prepareProofReportBindings(file, marks)
	return func(a *Analyzer) string {
		replacements := make(map[proofContributionKey]proofContribution, len(a.proofLifecycle.Segments))
		for key, row := range a.proofLifecycle.Segments {
			baseline, exists := old[key]
			unchanged := exists && sameFacts(baseline.Facts, row.Facts)
			facts := make([]ProofFact, len(row.Facts))
			for i, fact := range row.Facts {
				next, why := render(fact.Pos, unchanged)
				if why != "" {
					return why
				}
				fact, why = renderBindings(row, fact)
				if why != "" {
					return why
				}
				fact.Pos = next
				facts[i] = fact
			}
			row.Facts = facts
			replacements[key] = row
		}
		a.proofLifecycle.Segments = replacements
		return ""
	}
}

func BenchmarkProofReportCoordinateFullCallback532Rows21548Facts(b *testing.B) {
	b.StopTimer()
	state, file, baseline := benchmarkProofReportCoordinateCallback(b)
	for _, candidate := range []bool{false, true} {
		name := "full-reference"
		if candidate {
			name = "requested-index"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				segments := make(map[proofContributionKey]proofContribution, len(baseline))
				for key, row := range baseline {
					segments[key] = row
				}
				analyzer := &Analyzer{file: file}
				analyzer.proofLifecycle.Segments = segments
				b.StartTimer()
				var failure string
				if candidate {
					finish, why := state.prepareProofReportCoordinates(file, nil)
					if why != "" {
						b.Fatal(why)
					}
					failure = finish(analyzer)
				} else {
					failure = prepareProofReportCoordinatesFullReference(state, file, nil)(analyzer)
				}
				if failure != "" {
					b.Fatal(failure)
				}
			}
		})
	}
}

func BenchmarkProofReportRequestedCoordinateIndex(b *testing.B) {
	for _, count := range []int{1000, 20000} {
		file := &ast.File{}
		for i := 0; i < count; i++ {
			file.Decls = append(file.Decls, &ast.FuncDecl{Name: "row", Position: lexer.Pos{File: "bench.elisa", Line: i + 1, Col: 1, Offset: i * 10}})
		}
		request := proofReportCoordinateRequest{file.Decls[count-1].Pos(), false}
		b.Run(fmt.Sprintf("full/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				render := proofReportSyntaxRenderer(file, nil)
				pos, why := render(request.Pos, false)
				if why != "" {
					b.Fatal(why)
				}
				proofCoordinateBenchmarkSink = pos
			}
		})
		b.Run(fmt.Sprintf("witness-plus-filtered/%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				witnesses := captureProofReportCoordinateWitnesses(file)
				render := witnesses.requestedRenderer(nil, map[proofReportCoordinateRequest]struct{}{request: {}})
				pos, why := render(request.Pos, false)
				if why != "" {
					b.Fatal(why)
				}
				proofCoordinateBenchmarkSink = pos
			}
		})
	}
}

func TestRequestedProofCoordinateCollectionPreservesExactShiftPolicy(t *testing.T) {
	pos := lexer.Pos{File: "epoch.elisa", Line: 8, Col: 1, Offset: 81}
	retained := proofContributionKey{Owner: &ast.FuncDecl{Name: "retained"}, Phase: proofPhaseBody}
	reanalyzed := proofContributionKey{Owner: &ast.FuncDecl{Name: "reanalyzed"}, Phase: proofPhaseBody}
	oldRetained := []ProofFact{{Pos: pos, Subject: "same"}}
	oldReanalyzed := []ProofFact{{Pos: pos, Subject: "same"}}
	old := map[proofContributionKey]proofContribution{retained: {Key: retained, Facts: oldRetained}, reanalyzed: {Key: reanalyzed, Facts: oldReanalyzed}}
	current := map[proofContributionKey]proofContribution{retained: old[retained], reanalyzed: {Key: reanalyzed, Facts: append([]ProofFact(nil), oldReanalyzed...)}}
	requests := collectProofReportCoordinateRequests(current, old)
	if len(requests) != 2 || !hasCoordinateRequest(requests, proofReportCoordinateRequest{pos, true}) || !hasCoordinateRequest(requests, proofReportCoordinateRequest{pos, false}) {
		t.Fatal("equal-valued reanalysis was confused with retained source epoch")
	}
}

func hasCoordinateRequest(requests map[proofReportCoordinateRequest]struct{}, request proofReportCoordinateRequest) bool {
	_, present := requests[request]
	return present
}
