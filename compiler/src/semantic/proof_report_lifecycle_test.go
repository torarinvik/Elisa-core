//go:build cgo

package semantic

import (
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func assertExactLiveProofReport(t *testing.T, cold, warm *Result) {
	t.Helper()
	if !reflect.DeepEqual(cold.ProofReport, warm.ProofReport) {
		t.Fatalf("complete report differs\ncold=%#v\nwarm=%#v", cold.ProofReport, warm.ProofReport)
	}
	if !reflect.DeepEqual(cold.ElisionSummary, warm.ElisionSummary) {
		t.Fatalf("complete summary differs: cold=%+v warm=%+v", cold.ElisionSummary, warm.ElisionSummary)
	}
	if retainedDiagnostics(cold) != retainedDiagnostics(warm) {
		t.Fatalf("diagnostics differ\ncold=%s\nwarm=%s", retainedDiagnostics(cold), retainedDiagnostics(warm))
	}
}

func TestLiveProofReportRetainedBodyReplacementAcrossDisk(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `law Positive(self: i64) = self > 0

def changed(n: i64) -> i64 is Positive:
    return 5

def other(n: i64) -> i64 is Positive:
    return 7
`
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 || len(initial.ProofReport) < 2 {
		t.Fatalf("fixture has no real obligations: errors=%v report=%#v", initial.Errors(), initial.ProofReport)
	}
	publishedReport := initial.ProofReport
	oldReport := append([]ProofFact(nil), publishedReport...)
	runtimeSource := strings.Replace(source, "return 5", "return n", 1)
	for _, next := range []string{runtimeSource, source, runtimeSource, source} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		warm, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
		assertExactLiveProofReport(t, cold, warm)
		if !reflect.DeepEqual(publishedReport, oldReport) {
			t.Fatal("old published report backing mutated")
		}
		if len(warm.ProofReport) != len(oldReport) {
			t.Fatal("real obligation inventory accumulated/dropped records")
		}
	}
	withoutOther := strings.Replace(source, "\ndef other(n: i64) -> i64 is Positive:\n    return 7\n", "", 1)
	removedResult, err := state.Update(retainedParse(t, withoutOther), retainedParse(t, withoutOther), []byte(withoutOther))
	if err != nil {
		t.Fatal(err)
	}
	assertExactLiveProofReport(t, AnalyzeWithOptions(retainedParse(t, withoutOther), AnalyzeOptions{}), removedResult)
	if len(removedResult.ProofReport) != len(oldReport)-1 {
		t.Fatal("removed function retained its real obligation")
	}
	if !reflect.DeepEqual(publishedReport, oldReport) {
		t.Fatal("published old report backing mutated")
	}
}

func TestLiveProofReportDuplicateObligationsStayDistinct(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `law Positive(self: i64) = self > 0

def pair(x: i64 is Positive, y: i64 is Positive) -> i64:
    return x + y

def caller() -> i64:
    return pair(3, 3)
`
	result := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	calls := 0
	for _, fact := range result.ProofReport {
		if fact.Category == ProofCatCallArgRefinement {
			calls++
		}
	}
	if calls != 2 {
		t.Fatalf("independent actual argument obligations=%d want2: %#v", calls, result.ProofReport)
	}
}

func TestLiveProofReportParallelPublicationEqualsSerial(t *testing.T) {
	const source = `law Positive(self: i64) = self > 0

def first() -> i64 is Positive:
    return 3

def second() -> i64 is Positive:
    return 4

def third() -> i64 is Positive:
    return 5

def fourth() -> i64 is Positive:
    return 6
`
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	serial := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	t.Setenv("ELISA_PARALLEL_BODIES", "4")
	parallel := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if len(serial.ProofReport) != 4 || len(parallel.Errors()) != 0 {
		t.Fatalf("fixture errors=%v records=%#v", parallel.Errors(), serial.ProofReport)
	}
	assertExactLiveProofReport(t, serial, parallel)
}

func TestLiveProofReportProbeDoesNotReplaceCommittedOwner(t *testing.T) {
	// Lifecycle control supplements real source regressions. In particular a
	// nested quiet analysis must not erase an already committed callee report.
	a := &Analyzer{currentScope: NewScope(nil), globalScope: NewScope(nil)}
	fn := &ast.FuncDecl{Name: "owner"}
	finish := a.beginProofContribution(fn, proofPhaseBody, nil, "")
	a.recordProof(fn.Pos(), "real", "predicate", ProofProvenContract)
	finish()
	before := a.proofLifecycle.Segments[proofContributionKey{Owner: fn, Phase: proofPhaseBody}]
	a.currentProofCategory = ProofCatReturnRefinement
	restore := a.beginProofReportProbe()
	nested := a.beginProofContribution(fn, proofPhaseBody, nil, "")
	a.recordProof(fn.Pos(), "speculative", "predicate", ProofRuntime)
	nested()
	restore()
	after := a.proofLifecycle.Segments[proofContributionKey{Owner: fn, Phase: proofPhaseBody}]
	if !reflect.DeepEqual(before, after) || a.currentProofCategory != ProofCatReturnRefinement {
		t.Fatal("probe changed committed authority/category")
	}
}

func TestLiveProofReportSourceRelocationMatchesColdAcrossDisk(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `law Positive(self: i64) = self > 0

def first() -> i64 is Positive:
    return 3

def second() -> i64 is Positive:
    return 4
`
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	next := strings.Replace(source, "    return 3", "    if true:\n        return 3\n    return 3", 1)
	if len(initial.ProofReport) < 2 {
		t.Fatal("fixture lacks real surviving report contribution")
	}
	published := append([]ProofFact(nil), initial.ProofReport...)
	for _, revision := range []string{next, source, next, source} {
		disk, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(disk)
		if err != nil {
			t.Fatal(err)
		}
		got, err := state.Update(retainedParse(t, revision), retainedParse(t, revision), []byte(revision))
		if err != nil {
			t.Fatal(err)
		}
		cold := AnalyzeWithOptions(retainedParse(t, revision), AnalyzeOptions{})
		assertExactLiveProofReport(t, cold, got)
	}
	if !reflect.DeepEqual(initial.ProofReport, published) {
		t.Fatal("relocation mutated old published backing")
	}
}

func TestLiveProofReportNamedUsesKeepsHeaderLifetime(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `contract PositiveInput(x: i64):
    requires x > 0

contract BoundedInput(x: i64):
    requires x < 100

def need(value: i64) -> i64:
    uses PositiveInput(value)
    uses BoundedInput(value)
    return value
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	uses := 0
	for _, fact := range result.ProofReport {
		if fact.Predicate == "uses" {
			uses++
		}
	}
	if uses != 2 {
		t.Fatalf("actual uses applications=%d want2", uses)
	}
	next := strings.Replace(source, "return value", "return value + 0", 1)
	warm, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil {
		t.Fatal(err)
	}
	assertExactLiveProofReport(t, AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{}), warm)
}

func TestLiveProofReportProtocolLawImplOwnersAreDistinct(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `protocol Equal:
    def same(self: Self, other: Self) -> bool
    law reflexive(a: Self) = a.same(a)

struct First:
    value: i64

struct Second:
    value: i64

impl Equal for First:
    def same(self: First, other: First) -> bool:
        return self.value == other.value

impl Equal for Second:
    def same(self: Second, other: Second) -> bool:
        return self.value == other.value
`
	result := AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	owners := map[ast.Node]bool{}
	for key, segment := range result.analyzer.proofLifecycle.Segments {
		if key.Phase != proofPhaseProtocolLaw {
			continue
		}
		if _, ok := key.Owner.(*ast.ImplDecl); !ok {
			t.Fatal("law contribution not owned by concrete impl")
		}
		if len(segment.Facts) != 1 || key.Site == nil || !segment.Origins[key.Site] {
			t.Fatal("law origin/decision lost")
		}
		owners[key.Owner] = true
	}
	if len(owners) != 2 {
		t.Fatalf("independent law owners=%d want2", len(owners))
	}
}

func TestLiveProofReportProtocolDefaultPostReplacement(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `protocol Bound:
    def lo(self: Self) -> i64

protocol Clamp is Bound:
    def clamp(self: Self) -> i64:
        ensure result == self.lo()
        return self.lo()
`
	options := AnalyzeOptions{EnableSMT: true, EnforceStrictProofs: true}
	result := AnalyzeWithOptions(retainedParse(t, source), options)
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	a := result.analyzer
	defer a.closeSMT()
	original := append([]ProofFact(nil), result.ProofReport...)
	if len(original) == 0 {
		t.Fatal("actual protocol default produced no obligation")
	}
	for i := 0; i < 2; i++ {
		decls := make([]scopedDecl, 0, len(a.file.Decls))
		for _, decl := range a.file.Decls {
			decls = append(decls, scopedDecl{Decl: decl})
		}
		a.runPostPhase(1, decls, options, nil)
		a.publishLiveProofReport()
		if !reflect.DeepEqual(a.proofReport, original) {
			t.Fatalf("post phase accumulated/changed reports: %#v", a.proofReport)
		}
	}
	for key, segment := range a.proofLifecycle.Segments {
		if key.Phase != proofPhasePostDefault {
			continue
		}
		for origin := range segment.Origins {
			if fn, ok := origin.(*ast.FuncDecl); ok && fn != key.Owner && fn.Name == key.Owner.(*ast.FuncDecl).Name {
				t.Fatal("temporary Self function escaped as report origin")
			}
		}
	}
}

func TestLiveProofReportUnknownContextCannotSnapshotOrUpdate(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = "def value() -> i64:\n    return 1\n"
	_, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	// Supplemental ownership assertion: this is an observability record, not a
	// planted bounds certificate or a replacement for the real source tests.
	state.a.recordProof(state.result.File.Decls[0].Pos(), "unowned", "predicate", ProofRuntime)
	if _, err := state.MarshalState(); err == nil {
		t.Fatal("unknown report context was serialized as trusted authority")
	}
	next := strings.Replace(source, "return 1", "return 2", 1)
	if _, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next)); err == nil || !strings.Contains(err.Error(), "classified source contribution") {
		t.Fatalf("unknown authority admitted: %v", err)
	}
}

func TestLiveProofReportProbeRestorationIsIdempotent(t *testing.T) {
	a := &Analyzer{currentScope: NewScope(nil), globalScope: NewScope(nil)}
	fn := &ast.FuncDecl{Name: "owner"}
	finish := a.beginProofContribution(fn, proofPhaseBody, nil, "")
	restore := a.beginProofReportProbe()
	restore()
	a.recordProof(fn.Pos(), "real after probe", "predicate", ProofProvenContract)
	restore() // the deferred safety restore must not truncate later real facts
	finish()
	segment := a.proofLifecycle.Segments[proofContributionKey{Owner: fn, Phase: proofPhaseBody}]
	if len(segment.Facts) != 1 || segment.Facts[0].Subject != "real after probe" {
		t.Fatal("idempotent probe restore erased real work")
	}
}

func TestLiveProofReportResumableBuildPublishesSameCompleteSequence(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `law Positive(self: i64) = self > 0

def first() -> i64 is Positive:
    return 3

def second() -> i64 is Positive:
    return 4
`
	build, err := BeginAnalysisBuild(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 30; step++ {
		result, _, err := build.Advance(1, 0)
		if err != nil {
			t.Fatal(err)
		}
		if result != nil {
			assertExactLiveProofReport(t, AnalyzeWithOptions(retainedParse(t, source), AnalyzeOptions{}), result)
			return
		}
		data, err := build.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		build, err = RestoreAnalysisBuild(data)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("source build did not finish within declaration/post boundaries")
}

func TestLiveProofReportExternUsesKeepsOriginalDeclarationOwner(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `contract NonNegResult(x: i64):
    requires x >= 0
    ensure result >= 0

extern sqrt_floor(x: i64) -> i64 uses NonNegResult(x)

def caller(n: i64) -> i64:
    requires n >= 0
    return sqrt_floor(n)
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	symbol, ok := result.GlobalScope.Lookup("sqrt_floor")
	if !ok {
		t.Fatal("missing actual extern")
	}
	ext := symbol.Node.(*ast.ExternFuncDecl)
	uses := 0
	for key, segment := range state.a.proofLifecycle.Segments {
		for _, fact := range segment.Facts {
			if fact.Predicate == "uses" {
				uses++
				if key.Owner != ext || key.Phase != proofPhaseUses {
					t.Fatal("temporary extern adapter escaped as report owner")
				}
			}
		}
	}
	if uses != 1 {
		t.Fatalf("actual extern application records=%d want1", uses)
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(source, "return sqrt_floor(n)", "return sqrt_floor(n) + 0", 1)
	warm, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil {
		t.Fatal(err)
	}
	assertExactLiveProofReport(t, AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{}), warm)
}

func TestLiveProofReportNewUnownedDeclarationExprFactsStillDecline(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `contract Guard(x: i64):
    requires x >= 0

def consume(value: i64) -> i64:
    return value
`
	initial, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(initial.Errors()) != 0 || len(initial.ProofReport) != 0 {
		t.Fatalf("fixture should start with no real report: errors=%v facts=%#v", initial.Errors(), initial.ProofReport)
	}
	next := strings.Replace(source, "    return value", "    uses Guard(value)\n    return value", 1)
	cold := AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{})
	if len(cold.Errors()) != 0 || len(cold.ProofReport) == 0 {
		t.Fatalf("edited source must produce a real declaration fact: %v %#v", cold.Errors(), cold.ProofReport)
	}
	_, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err == nil || !strings.Contains(err.Error(), "program-wide declaration results changed") {
		t.Fatalf("new unowned declaration ExprFacts must retain their independent admission guard: %v", err)
	}
	if result, ok := state.ResultForSource([]byte(source)); !ok || result != initial {
		t.Fatal("unsupported declaration ExprFacts import mutated old revision")
	}
}

func TestLiveProofReportDefaultDefinitionAndCallHaveSeparateLifetimes(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `law Positive(self: i64) = self > 0

def need_pos(value: i64 is Positive) -> i64:
    return value

def consume(value: i64 = need_pos(3)) -> i64:
    return value

def caller() -> i64:
    return consume()
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	header, body := 0, 0
	for key, segment := range state.a.proofLifecycle.Segments {
		fn, ok := key.Owner.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if key.Phase == proofPhaseHeader && fn.Name == "consume" {
			header += len(segment.Facts)
		}
		if key.Phase == proofPhaseBody && fn.Name == "caller" {
			body += len(segment.Facts)
		}
	}
	if header == 0 || body == 0 {
		t.Fatalf("actual default/call obligations missing: header=%d body=%d facts=%#v", header, body, result.ProofReport)
	}
	withoutCall := strings.Replace(source, "return consume()", "return 0", 1)
	for _, next := range []string{withoutCall, source} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		warm, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
		if err != nil {
			t.Fatal(err)
		}
		assertExactLiveProofReport(t, AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{}), warm)
	}
}

func TestLiveProofReportLambdaBelongsToEnclosingSourceFunction(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	const source = `law Positive(self: i64) = self > 0

def need_pos(value: i64 is Positive) -> i64:
    return value

def factory() -> fn(i64) -> i64:
    return fn(value: i64) -> i64:
        return need_pos(value)
`
	result, state := AnalyzeRetained(retainedParse(t, source), retainedParse(t, source), []byte(source), AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatal(result.Errors())
	}
	found := false
	for key, segment := range state.a.proofLifecycle.Segments {
		if len(segment.Facts) == 0 {
			continue
		}
		fn, ok := key.Owner.(*ast.FuncDecl)
		if !ok || fn.Name != "factory" || key.Phase != proofPhaseBody {
			t.Fatalf("lambda report acquired a temporary/global owner: %#v", key)
		}
		found = true
	}
	if !found {
		t.Fatal("actual lambda call produced no refinement obligation")
	}
	data, err := state.MarshalState()
	if err != nil {
		t.Fatal(err)
	}
	state, err = RestoreAnalysisState(data)
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Replace(source, "return need_pos(value)", "return need_pos(3)", 1)
	warm, err := state.Update(retainedParse(t, next), retainedParse(t, next), []byte(next))
	if err != nil {
		t.Fatal(err)
	}
	assertExactLiveProofReport(t, AnalyzeWithOptions(retainedParse(t, next), AnalyzeOptions{}), warm)
}
