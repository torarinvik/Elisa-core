package main

import (
	"bytes"
	"elisacore/src/backend"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseArgsAcceptsOptimizationShorthands(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		level int
	}{
		{name: "shorthand", args: []string{"-O3", "fixture.elisa"}, level: 3},
		{name: "equals", args: []string{"-O=2", "fixture.elisa"}, level: 2},
		{name: "separate", args: []string{"-O", "0", "fixture.elisa"}, level: 0},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			options, err := parseArgs(test.args)
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			if !options.hasOptLevel {
				t.Fatal("expected optimization flag to be marked as explicitly set")
			}
			if int(options.optLevel) != test.level {
				t.Fatalf("expected opt level O%d, got O%d", test.level, int(options.optLevel))
			}
		})
	}
}
func TestParseArgsAcceptsLinkerFlags(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-L", "/opt/example/lib", "-lLLVM-C", "-link", "-Wl,-rpath,/opt/example/lib", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	expected := []string{"-L/opt/example/lib", "-lLLVM-C", "-Wl,-rpath,/opt/example/lib"}
	if len(options.linkFlags) != len(expected) {
		t.Fatalf("expected %d link flags, got %#v", len(expected), options.linkFlags)
	}
	for i, flag := range expected {
		if options.linkFlags[i] != flag {
			t.Fatalf("expected link flag %d to be %q, got %#v", i, flag, options.linkFlags)
		}
	}
}

func TestParseArgsAcceptsConcurrencyStrictFlag(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-Wconcurrency", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !options.concurrencyStrict {
		t.Fatal("expected -Wconcurrency to enable strict concurrency diagnostics")
	}
	semanticOptions := semanticOptionsForCLI(options)
	if !semanticOptions.EnforceStrictConcurrency {
		t.Fatal("expected CLI semantic options to enforce strict concurrency")
	}
	if semanticOptions.EnforcePerfLints {
		t.Fatal("expected -Wconcurrency not to enable unrelated perf lint enforcement")
	}
}

func TestParseArgsAcceptsUnifiedStrictFlag(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-Wstrict", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !options.strictPolicy || !options.perfStrict || !options.concurrencyStrict {
		t.Fatalf("expected -Wstrict to enable strict/perf/concurrency policy, got strict=%v perf=%v concurrency=%v", options.strictPolicy, options.perfStrict, options.concurrencyStrict)
	}
	semanticOptions := semanticOptionsForCLI(options)
	if !semanticOptions.EnforceUnsafePermissions {
		t.Fatal("expected -Wstrict to enforce unsafe permissions")
	}
	if !semanticOptions.EnforceProgressSafety {
		t.Fatal("expected -Wstrict to enable progress safety analysis")
	}
	if !semanticOptions.EnforcePerfLints {
		t.Fatal("expected -Wstrict to enforce performance lints")
	}
	if !semanticOptions.EnforceStrictConcurrency {
		t.Fatal("expected -Wstrict to enforce strict concurrency")
	}
}

// docs/102: -Wprogress activates the (already-built, dormant) progress-safety obligation checker
// on its own dial, without dragging in the rest of -Wstrict.
func TestParseArgsProgressDialIsIsolated(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-Wprogress", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !options.progressStrict {
		t.Fatal("expected -Wprogress to set progressStrict")
	}
	if options.strictPolicy || options.perfStrict || options.concurrencyStrict {
		t.Fatalf("-Wprogress must be isolated, got strict=%v perf=%v concurrency=%v", options.strictPolicy, options.perfStrict, options.concurrencyStrict)
	}
	semanticOptions := semanticOptionsForCLI(options)
	if !semanticOptions.EnforceProgressSafety {
		t.Fatal("expected -Wprogress to enable progress safety analysis")
	}
	if semanticOptions.EnforcePerfLints || semanticOptions.EnforceStrictConcurrency || semanticOptions.EnforceUnsafePermissions {
		t.Fatal("expected -Wprogress not to enable unrelated lints")
	}
}

// docs/90: the SMT discharge tier is ON by default; `-nosmt` opts out. (A missing
// solver latches off and falls back to the runtime check, so default-on is safe.)
func TestParseArgsEnablesSMTByDefault(t *testing.T) {
	t.Parallel()
	def, err := parseArgs([]string{"fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !def.enableSMT || !semanticOptionsForCLI(def).EnableSMT {
		t.Fatalf("expected SMT discharge ON by default, got enableSMT=%v", def.enableSMT)
	}
	off, err := parseArgs([]string{"-nosmt", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs(-nosmt) returned error: %v", err)
	}
	if off.enableSMT || semanticOptionsForCLI(off).EnableSMT {
		t.Fatalf("expected -nosmt to disable SMT discharge, got enableSMT=%v", off.enableSMT)
	}
}

func TestParseArgsExplainHoleEnablesProofHoleHints(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"--explain-hole", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if !options.explainHole {
		t.Fatal("expected --explain-hole to be recorded in CLI options")
	}
	if !semanticOptionsForCLI(options).EmitProofHoleHints {
		t.Fatal("expected --explain-hole to enable semantic proof-hole hints")
	}
}

func TestRunCLIConcurrencyStrictPromotesRawAtomicDeprecation(t *testing.T) {
	prevSuppress, hadSuppress := os.LookupEnv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	_ = os.Unsetenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
	t.Cleanup(func() {
		if hadSuppress {
			_ = os.Setenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS", prevSuppress)
		} else {
			_ = os.Unsetenv("ELISACORE_SUPPRESS_DEPRECATED_WARNINGS")
		}
	})

	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "raw_atomic.elisa")
	src := `enum MemoryOrder:
    Relaxed
    Acquire
    Release
    AcqRel
    SeqCst

def load[T](slot: atomic[T]&, order: MemoryOrder) -> T:
    return slot.value

def use_raw(slot: mutable atomic[i64]&) -> i64:
    return load(slot, MemoryOrder.Acquire)
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write raw atomic fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	// Raw atomic surface is removed from public code: even a plain semantic emit
	// (no warning flag) must reject it as a hard error.
	if exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr); exitCode == 0 {
		t.Fatalf("expected semantic emit to reject raw atomic surface\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "raw concurrency surface removed: `load` is legacy raw atomic surface") {
		t.Fatalf("expected semantic emit to report raw atomic removal, got:\n%s", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	// The now-redundant -Wconcurrency flag stays accepted and yields the same error.
	if exitCode := runCLI([]string{"-Wconcurrency", "-emit", "semantic", fixturePath}, &stdout, &stderr); exitCode == 0 {
		t.Fatalf("expected -Wconcurrency semantic emit to fail\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "raw concurrency surface removed: `load` is legacy raw atomic surface") {
		t.Fatalf("expected -Wconcurrency raw atomic removal error, got:\n%s", stderr.String())
	}
}

func TestRunCLIUnifiedStrictEnforcesUnsafePermissions(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "strict_unsafe_index.elisa")
	src := `def read_at(xs: darray[u8], i: int) -> u8:
    if i < xs.count:
        return xs[i]
    return 0
`
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write strict unsafe fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-Wstrict", "-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode == 0 {
		t.Fatalf("expected -Wstrict semantic emit to fail for unchecked signed index\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "unchecked index requires") {
		t.Fatalf("expected -Wstrict to enforce unsafe index proof, got:\n%s", stderr.String())
	}
}

func TestParseArgsRejectsRemovedPackedABI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "equals", args: []string{"-packed-abi=word-handle", "fixture.elisa"}},
		{name: "separate", args: []string{"-packed-abi", "row-handle", "fixture.elisa"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, err := parseArgs(test.args)
			if err == nil {
				t.Fatal("expected removed packed ABI flag error, got none")
			}
			if !strings.Contains(err.Error(), "-packed-abi has been removed") {
				t.Fatalf("expected removed packed ABI diagnostic, got %q", err.Error())
			}
		})
	}
}
func TestParseArgsDefaultsPackedLoweringToCanonicalProfile(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.packedProfile.Contract() != backend.PackedLoweringContractCanonicalCompilerGraph {
		t.Fatalf("expected canonical packed lowering profile by default, got %q", options.packedProfile.Contract())
	}
	if options.packedProfile.SelectionKey() != "canonical" {
		t.Fatalf("expected canonical packed lowering selection by default, got %q", options.packedProfile.SelectionKey())
	}
}
func TestResolveProjectTargetRejectsRemovedPackedABI(t *testing.T) {
	t.Parallel()
	projectRoot := t.TempDir()
	project := &resolvedProject{
		root:     projectRoot,
		filePath: filepath.Join(projectRoot, projectFileName),
		config: projectDefinition{
			Targets: map[string]projectTargetDefinition{
				"default": {
					Entry:     "main.elisa",
					PackedABI: "row-handle",
				},
			},
		},
	}

	_, err := resolveProjectTarget(project, projectCLIOptions{})
	if err == nil {
		t.Fatal("expected removed project packed-abi diagnostic, got none")
	}
	if !strings.Contains(err.Error(), "uses removed packed-abi override") {
		t.Fatalf("expected removed project packed-abi diagnostic, got %q", err.Error())
	}
}
func TestParseArgsAcceptsPackedInspectEmitAlias(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-emit", "packed-info", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitPacked {
		t.Fatalf("expected packed emit mode, got %q", options.emit)
	}
}
func TestParseArgsAcceptsLoweredEmitAlias(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-emit", "lower", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitLowered {
		t.Fatalf("expected lowered emit mode, got %q", options.emit)
	}
}
func TestParseArgsAcceptsSemanticEmitAlias(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-emit", "sema", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitSemantic {
		t.Fatalf("expected semantic emit mode, got %q", options.emit)
	}
}
func TestParseArgsAcceptsFactTraceEmitAlias(t *testing.T) {
	t.Parallel()
	options, err := parseArgs([]string{"-emit", "fact-trace", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitFacts {
		t.Fatalf("expected facts emit mode, got %q", options.emit)
	}
}
func TestRunCLIEmitsSemanticReport(t *testing.T) {
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "semantic_fixture.elisa")
	src := "const enum TokenKind of u32:\n    IDENT = 1\n\nstruct Token:\n    kind: TokenKind\n\nstruct ParserState:\n    cursor: mutable usize\n\nimpl mutable ParserState&:\n    def expect_kind(self: mutable ParserState&, kind: TokenKind) -> Token:\n        _ = kind\n        return Token{kind: TokenKind.IDENT}\n\ndef expression(state: mutable ParserState&) -> Token:\n    return state.expect_kind(TokenKind.IDENT)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write semantic fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "semantic", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"=== semantic ===",
		"func expression",
		"signature: fn(mutable ParserState&) -> Token",
		"return_isolation:",
		"fact_snapshot:",
		"fact_exits:",
		"fact_groups:",
		"fact_blocks:",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected semantic report to contain %q, got:\n%s", want, output)
		}
	}
}
func TestRunCLIEmitsFactTraceReport(t *testing.T) {
	t.Parallel()
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "fact_core_rules.elisa")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=contains:fact_core_rules", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"=== facts ===", "contract: version=fact-trace-v2", "func fact_core_rules", "snapshot:", "transforms:", "groups:", "explanations:", "widen player"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected fact trace report to contain %q, got:\n%s", want, output)
		}
	}
}
