package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIEmitsFilteredFactTraceReport(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "fact-trace", "-filter", "kind=eq:widen,class=eq:typestate", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "widen player [typestate]") {
		t.Fatalf("expected filtered fact trace to contain typestate widening, got:\n%s", output)
	}
	if strings.Contains(output, "consume store") {
		t.Fatalf("expected filtered fact trace to omit consume transforms, got:\n%s", output)
	}
}

func TestRunCLIExplainProofReportShowsClassesAndClosedWorld(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("z3"); err != nil {
		t.Skip("z3 not on PATH; explain proof report test skipped")
	}
	fixture := filepath.Join(t.TempDir(), "explain_report.elisa")
	src := `typestate Socket:
	fd: mutable i64
	states: Closed, Open
	transition open_it: Closed -> Open
	transition close_it: Open -> Closed

def round_trip(s: mutable Socket[Closed]&) ensures s => Closed:
	open_it(s)
	close_it(s)

def scoped_decline(n: i64):
	requires n >= 0
	assert n >= 5 by scoped:
		assert(n >= 0)
`
	if err := os.WriteFile(fixture, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-permissive", "--explain", "--explain-hole", "-emit", "semantic", fixture}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	output := stderr.String()
	for _, want := range []string{
		"proven (contract) (typestate)",
		"runtime (scoped)",
		"goal:",
		"known facts:",
		"closed world:",
		"- n >= 0",
		"missing:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected explain proof report to contain %q, got:\n%s", want, output)
		}
	}
}

func TestRunCLIEmitsInterfaceFactTraceReport(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_interface_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "class=eq:interface", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"func fact_interface_nested", "func fact_interface_rules", "require B:FactBuilder [interface]", "required_interfaces=[B:FactBuilder]"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected interface fact trace to contain %q, got:\n%s", want, output)
		}
	}
}
func TestRunCLIEmitsFactTraceContractSnapshot(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=eq:fact_core_rules,mode=eq:summary", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"contract: version=fact-trace-v2 order=kind,target,class,reason,source summary=mode=eq:summary json=format=eq:json matchers=contains|eq|regex filters=alias|class|detail|effect|format|function|kind|mode|path|reason|region|source|sourcekind|store|target|verb",
		"func fact_core_rules",
		"summary: transforms=22",
		"kinds=[consume:1, ensure:1, invalidate:2, produce:4, rebase:1, recompute:7, refine:5, widen:1]",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected fact trace contract snapshot to contain %q, got:\n%s", want, output)
		}
	}
	if strings.Contains(output, "  transforms:") || strings.Contains(output, "  explanations:") {
		t.Fatalf("expected summary mode to omit detailed transform sections, got:\n%s", output)
	}
}
func TestRunCLIEmitsFactTraceKeyedFilterSelectors(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	coreFixture := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")
	interfaceFixture := filepath.Join(repoRoot, "Code", "test_programs", "fact_interface_rules.elisa")
	cases := []struct {
		name     string
		fixture  string
		filter   string
		contains []string
		omits    []string
	}{
		{name: "kind", fixture: coreFixture, filter: "kind=eq:consume", contains: []string{"consume store [usage]"}, omits: []string{"widen player"}},
		{name: "sourcekind", fixture: coreFixture, filter: "sourcekind=eq:store", contains: []string{"produce frozen [representation,storage,store-deps]", "rebase store [store-deps]"}},
		{name: "target", fixture: coreFixture, filter: "target=contains:alias.value", contains: []string{"recompute alias.value [typestate,shape,optimization]"}, omits: []string{"consume store"}},
		{name: "path", fixture: coreFixture, filter: "path=contains:alias.value", contains: []string{"path_facts=[<return>.value{root=<return>,path=value,steps=result:value};", "alias.value{root=alias,path=value,steps=field:value}", "recompute alias.value [typestate,shape,optimization]"}},
		{name: "alias", fixture: coreFixture, filter: "alias=contains:alias-class#0", contains: []string{"alias-class#0: {alias, first} mutated", "recompute first [typestate,shape,optimization,store-deps]"}},
		{name: "region", fixture: coreFixture, filter: "region=eq:scratch", contains: []string{"region_deps=[scratch[0->destroyed]]", "invalidate scratch [region-deps]"}},
		{name: "store", fixture: coreFixture, filter: "store=eq:store", contains: []string{"handle_store_deps=[frozen<-store]", "rebase store [store-deps]"}},
		{name: "detail", fixture: coreFixture, filter: "detail=eq:store_deps=store", contains: []string{"{operation=freeze,store_deps=store}"}, omits: []string{"rebase store [store-deps]"}},
		{name: "effect", fixture: interfaceFixture, filter: "effect=eq:Console.Write", contains: []string{"require Console.Write [effects]", "required_effects=[Console.Write]"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI([]string{"-emit", "facts", "-filter", tc.filter, tc.fixture}, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
			}
			output := stdout.String()
			for _, want := range tc.contains {
				if !strings.Contains(output, want) {
					t.Fatalf("expected filter %q to contain %q, got:\n%s", tc.filter, want, output)
				}
			}
			for _, omit := range tc.omits {
				if strings.Contains(output, omit) {
					t.Fatalf("expected filter %q to omit %q, got:\n%s", tc.filter, omit, output)
				}
			}
		})
	}
}
func TestRunCLIEmitsFactTraceFilterIntersections(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "kind=eq:recompute,class=eq:store-deps,target=contains:alias.value", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "recompute alias.value [typestate,shape,optimization,store-deps]") {
		t.Fatalf("expected intersected filter to keep alias.value store-deps recompute, got:\n%s", output)
	}
	if strings.Contains(output, "recompute alias.value [typestate,shape,optimization] <- control-flow instruction") {
		t.Fatalf("expected intersected filter to omit non-store-deps recompute, got:\n%s", output)
	}
}
func TestRunCLIEmitsFactTraceSnapshotOnlyFilter(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=eq:fact_core_rules,target=eq:<return>.value,mode=eq:summary", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"func fact_core_rules", "<return>.value{root=<return>,path=value,steps=result:value}", "summary: transforms=0"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected snapshot-only filter to contain %q, got:\n%s", want, output)
		}
	}
}
func TestRunCLIEmitsMixedRequireFactTrace(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_interface_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=eq:fact_interface_rules,kind=eq:require", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"require B:FactBuilder [interface]", "require Console.Write [effects]", "required_effects=[Console.Write]", "required_interfaces=[B:FactBuilder]"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected mixed require trace to contain %q, got:\n%s", want, output)
		}
	}
}
func TestRunCLIEmitsPackedTreeStoreProvenanceFacts(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "compiler_parallel_fixture.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=eq:build_frozen_expr_graph,store=eq:store", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"func build_frozen_expr_graph", "produced=[frozen, left, right, root]", "handle_store_deps=[frozen<-store]", "{operation=freeze,store_deps=store}"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected packed/tree provenance trace to contain %q, got:\n%s", want, output)
		}
	}
}
func TestRunCLIEmitsFactTraceJSONShape(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_interface_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=eq:fact_interface_rules,class=eq:interface,format=eq:json", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	var report struct {
		Version   string   `json:"version"`
		Mode      string   `json:"mode"`
		Format    string   `json:"format"`
		Filters   []string `json:"filters"`
		Matchers  []string `json:"matchers"`
		Functions []struct {
			Name     string `json:"name"`
			Snapshot struct {
				RequiredInterfaces []string `json:"required_interfaces"`
			} `json:"snapshot"`
			Transforms []struct {
				Kind    string   `json:"kind"`
				Classes []string `json:"classes"`
				Target  string   `json:"target"`
			} `json:"transforms"`
		} `json:"functions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON fact trace:\n%s\nerror: %v", stdout.String(), err)
	}
	if report.Version != "fact-trace-v2" || report.Mode != "full" || report.Format != "json" {
		t.Fatalf("unexpected JSON contract: %#v", report)
	}
	hasFilter := false
	for _, filter := range report.Filters {
		if filter == "format" {
			hasFilter = true
			break
		}
	}
	hasMatcher := false
	for _, matcher := range report.Matchers {
		if matcher == "eq" {
			hasMatcher = true
			break
		}
	}
	if !hasFilter || !hasMatcher {
		t.Fatalf("expected JSON contract metadata to include v2 filters and matchers, got %#v", report)
	}
	if len(report.Functions) != 1 || report.Functions[0].Name != "fact_interface_rules" {
		t.Fatalf("expected one filtered function, got %#v", report.Functions)
	}
	if got := report.Functions[0].Snapshot.RequiredInterfaces; len(got) != 1 || got[0] != "B:FactBuilder" {
		t.Fatalf("expected required interface snapshot, got %#v", got)
	}
	if len(report.Functions[0].Transforms) != 1 || report.Functions[0].Transforms[0].Kind != "require" || report.Functions[0].Transforms[0].Target != "B:FactBuilder" {
		t.Fatalf("unexpected JSON transform shape: %#v", report.Functions[0].Transforms)
	}
}
func TestRunCLIEmitsFactTraceJSONStructuredSourcePos(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=eq:fact_core_rules,kind=eq:widen,format=eq:json", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	var report struct {
		Functions []struct {
			Transforms []struct {
				SourcePos struct {
					File   string `json:"file"`
					Line   int    `json:"line"`
					Column int    `json:"column"`
				} `json:"source_pos"`
			} `json:"transforms"`
		} `json:"functions"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("failed to parse JSON fact trace:\n%s\nerror: %v", stdout.String(), err)
	}
	if len(report.Functions) != 1 || len(report.Functions[0].Transforms) != 1 {
		t.Fatalf("expected one widened transform, got %#v", report.Functions)
	}
	if report.Functions[0].Transforms[0].SourcePos.File == "" || report.Functions[0].Transforms[0].SourcePos.Line == 0 || report.Functions[0].Transforms[0].SourcePos.Column == 0 {
		t.Fatalf("expected structured source position, got %#v", report.Functions[0].Transforms[0].SourcePos)
	}
}
func TestRunCLIRejectsMalformedFactTraceFilters(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")
	cases := []string{"kind=", "=eq:widen", "unknown=eq:widen", "kind=widen", "fact_core_rules"}
	for _, filter := range cases {
		t.Run(filter, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI([]string{"-emit", "facts", "-filter", filter, fixturePath}, &stdout, &stderr)
			if exitCode == 0 {
				t.Fatalf("expected filter %q to fail, stdout:\n%s", filter, stdout.String())
			}
			if !strings.Contains(stderr.String(), "fact trace filter") {
				t.Fatalf("expected filter error for %q, got:\n%s", filter, stderr.String())
			}
		})
	}
}
func BenchmarkGenerateFactTraceReportSummary(b *testing.B) {
	repoRoot := repoRootFromMainTest(b)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")
	var stderr bytes.Buffer
	program, ok := loadProgramInput(fixturePath, &stderr)
	if !ok {
		b.Fatalf("load failed:\n%s", stderr.String())
	}
	_, result, ok := analyzeLoadedProgram(program, &stderr)
	if !ok {
		b.Fatalf("analysis failed:\n%s", stderr.String())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := generateFactTraceReport(result, "mode=eq:summary"); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkGenerateFactTraceReportLargeTransformStream(b *testing.B) {
	result := syntheticFactTraceResultForBenchmark(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := generateFactTraceReport(result, "mode=eq:summary"); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkGenerateFactTraceReportKeyedFilterLargeTransformStream(b *testing.B) {
	result := syntheticFactTraceResultForBenchmark(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := generateFactTraceReport(result, "kind=eq:recompute,class=eq:store-deps,target=eq:node.99,mode=eq:summary"); err != nil {
			b.Fatal(err)
		}
	}
}
func syntheticFactTraceResultForBenchmark(count int) *semantic.Result {
	transforms := make([]semantic.FactTransform, 0, count)
	for i := 0; i < count; i++ {
		classes := []semantic.FactClass{semantic.FactTypestate, semantic.FactShape, semantic.FactOptimization}
		if i%3 == 0 {
			classes = append(classes, semantic.FactStoreDeps)
		}
		transforms = append(transforms, semantic.FactTransform{
			Kind:       semantic.FactTransformRecompute,
			Classes:    classes,
			Target:     fmt.Sprintf("node.%d", i),
			Source:     "synthetic",
			SourceKind: semantic.FactSourceFlowInstr,
			Details:    []semantic.FactTransformDetail{{Name: "mutation", Value: "benchmark"}},
			Reason:     "benchmark transform stream",
		})
	}
	analysis := &semantic.FunctionAnalysis{FactTransforms: semantic.CanonicalFactTransforms(transforms)}
	decl := &ast.FuncDecl{Name: "bench_facts"}
	return &semantic.Result{
		GlobalScope: &semantic.Scope{Symbols: map[string]*semantic.Symbol{
			"bench_facts": {Kind: semantic.SymbolFunc, Type: &semantic.FuncType{Return: &semantic.BuiltinType{Name: "void"}}, Node: decl},
		}},
		FunctionAnalyses: map[*ast.FuncDecl]*semantic.FunctionAnalysis{decl: analysis},
	}
}
