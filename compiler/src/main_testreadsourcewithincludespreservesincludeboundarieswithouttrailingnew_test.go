package main

import (
	"bytes"
	"elisacore/src/backend"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSourceWithIncludesPreservesIncludeBoundariesWithoutTrailingNewlines(t *testing.T) {
	dir := t.TempDir()
	leafPath := filepath.Join(dir, "leaf.elisa")
	midPath := filepath.Join(dir, "mid.elisa")
	rootPath := filepath.Join(dir, "root.elisa")

	if err := os.WriteFile(leafPath, []byte("leaf_line"), 0o644); err != nil {
		t.Fatalf("write leaf fixture: %v", err)
	}
	if err := os.WriteFile(midPath, []byte("# include \"leaf.elisa\"\nmid_line"), 0o644); err != nil {
		t.Fatalf("write mid fixture: %v", err)
	}
	if err := os.WriteFile(rootPath, []byte("root_start\n# include \"mid.elisa\"\nroot_end"), 0o644); err != nil {
		t.Fatalf("write root fixture: %v", err)
	}

	expanded, err := readSourceWithIncludes(rootPath, map[string]bool{})
	if err != nil {
		t.Fatalf("readSourceWithIncludes: %v", err)
	}

	got := string(expanded)
	want := "root_start\nleaf_line\nmid_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}
func TestReadSourceWithIncludesAcceptsBareIncludeDirective(t *testing.T) {
	dir := t.TempDir()
	leafPath := filepath.Join(dir, "leaf.elisa")
	midPath := filepath.Join(dir, "mid.elisa")
	rootPath := filepath.Join(dir, "root.elisa")

	if err := os.WriteFile(leafPath, []byte("leaf_line"), 0o644); err != nil {
		t.Fatalf("write leaf fixture: %v", err)
	}
	if err := os.WriteFile(midPath, []byte("include \"leaf.elisa\"\nmid_line"), 0o644); err != nil {
		t.Fatalf("write mid fixture: %v", err)
	}
	if err := os.WriteFile(rootPath, []byte("root_start\ninclude \"mid.elisa\"\nroot_end"), 0o644); err != nil {
		t.Fatalf("write root fixture: %v", err)
	}

	expanded, err := readSourceWithIncludes(rootPath, map[string]bool{})
	if err != nil {
		t.Fatalf("readSourceWithIncludes: %v", err)
	}

	got := string(expanded)
	want := "root_start\nleaf_line\nmid_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}
func TestReadSourceWithIncludesAcceptsPascalIncludeDirectives(t *testing.T) {
	dir := t.TempDir()
	leafPath := filepath.Join(dir, "leaf.elisa")
	midPath := filepath.Join(dir, "mid.elisa")
	rootPath := filepath.Join(dir, "root.elisa")

	if err := os.WriteFile(leafPath, []byte("leaf_line"), 0o644); err != nil {
		t.Fatalf("write leaf fixture: %v", err)
	}
	if err := os.WriteFile(midPath, []byte("{$I leaf.elisa}\nmid_line"), 0o644); err != nil {
		t.Fatalf("write mid fixture: %v", err)
	}
	if err := os.WriteFile(rootPath, []byte("root_start\n{$INCLUDE 'mid.elisa'}\nroot_end"), 0o644); err != nil {
		t.Fatalf("write root fixture: %v", err)
	}

	expanded, err := readSourceWithIncludes(rootPath, map[string]bool{})
	if err != nil {
		t.Fatalf("readSourceWithIncludes: %v", err)
	}

	got := string(expanded)
	want := "root_start\nleaf_line\nmid_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}

func TestReadSourceWithIncludesPreservesIndentedIncludeContext(t *testing.T) {
	dir := t.TempDir()
	leafPath := filepath.Join(dir, "leaf.elisa")
	rootPath := filepath.Join(dir, "root.elisa")

	if err := os.WriteFile(leafPath, []byte("def foo() -> int:\n    return 7"), 0o644); err != nil {
		t.Fatalf("write leaf fixture: %v", err)
	}
	if err := os.WriteFile(rootPath, []byte("module M:\n    include \"leaf.elisa\"\n\ndef main() -> int:\n    return M::foo()"), 0o644); err != nil {
		t.Fatalf("write root fixture: %v", err)
	}

	expanded, err := readSourceWithIncludes(rootPath, map[string]bool{})
	if err != nil {
		t.Fatalf("readSourceWithIncludes: %v", err)
	}

	got := string(expanded)
	want := "module M:\n    def foo() -> int:\n        return 7\n\ndef main() -> int:\n    return M::foo()"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}

func TestRunCLICompilesIndentedIncludeInsideModule(t *testing.T) {
	dir := t.TempDir()
	leafPath := filepath.Join(dir, "leaf.elisa")
	rootPath := filepath.Join(dir, "root.elisa")

	if err := os.WriteFile(leafPath, []byte("def foo() -> int:\n    return 7"), 0o644); err != nil {
		t.Fatalf("write leaf fixture: %v", err)
	}
	if err := os.WriteFile(rootPath, []byte("module M:\n    include \"leaf.elisa\"\n\ndef main() -> int:\n    return M::foo()"), 0o644); err != nil {
		t.Fatalf("write root fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", rootPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 7") {
		t.Fatalf("expected module-qualified include program to print 7, got stdout:\n%s", stdout.String())
	}
}

func TestRunCLICompilesModuleLocalConstLookup(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "module_const.elisa")
	src := "module M:\n    const ANSWER: int = 7\n\n    def answer() -> int:\n        return ANSWER\n\ndef main() -> int:\n    return M::answer()\n"
	if err := os.WriteFile(rootPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write module const fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", rootPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 7") {
		t.Fatalf("expected module-local const program to print 7, got stdout:\n%s", stdout.String())
	}
}

func TestRunCLICompilesModuleLocalGlobalLookup(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "module_global.elisa")
	src := "module M:\n    global mutable counter: int = 5\n\n    def value() -> int:\n        return counter\n\ndef main() -> int:\n    return M::value()\n"
	if err := os.WriteFile(rootPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write module global fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "interpret", rootPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[ result   ] 5") {
		t.Fatalf("expected module-local global program to print 5, got stdout:\n%s", stdout.String())
	}
}
func TestRunCLIEmitsBitcodeAndObjectForFixtureProgram(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "pointer_alloc.elisa")
	outputDir := t.TempDir()
	bitcodePath := filepath.Join(outputDir, "pointer_alloc.bc")
	objectPath := filepath.Join(outputDir, "pointer_alloc.o")

	tests := []struct {
		name       string
		args       []string
		outputPath string
		check      func(*testing.T, []byte)
	}{
		{
			name:       "bitcode",
			args:       []string{"-emit", "bc", "-o", bitcodePath, fixturePath},
			outputPath: bitcodePath,
			check: func(t *testing.T, data []byte) {
				t.Helper()
				if !looksLikeBitcodeFile(data) {
					t.Fatalf("expected bitcode magic prefix, got % x", data[:min(len(data), 4)])
				}
			},
		},
		{
			name:       "object",
			args:       []string{"-emit", "obj", "-o", objectPath, fixturePath},
			outputPath: objectPath,
			check: func(t *testing.T, data []byte) {
				t.Helper()
				if !looksLikeObjectFile(data) {
					t.Fatalf("expected native object file magic, got % x", data[:min(len(data), 4)])
				}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := runCLI(test.args, &stdout, &stderr)
			if exitCode != 0 {
				t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("expected binary emit mode not to write stdout, got:\n%s", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
			}
			data, err := os.ReadFile(test.outputPath)
			if err != nil {
				t.Fatalf("expected output file %s to exist: %v", test.outputPath, err)
			}
			if len(data) < 4 {
				t.Fatalf("expected non-empty output file, got %d bytes", len(data))
			}
			test.check(t, data)
		})
	}
}
func TestRunCLIEmitsHeaderForExportFixture(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	fixturePath := filepath.Join(repoRoot, "Code", "test_programs", "export_vec2i.elisa")
	outputPath := filepath.Join(t.TempDir(), "export_vec2i.h")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "header", "-o", outputPath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected header emit with -o not to write stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected header output file %s to exist: %v", outputPath, err)
	}
	header := string(data)
	checks := []string{
		"typedef struct Vec2i Vec2i;",
		"struct Vec2i {",
		"int32_t x;",
		"int32_t y;",
		"extern int32_t ctx_seed;",
		"Vec2i vec2i_add(Vec2i arg0, Vec2i arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
}
func TestRunCLIEmitsModuleInterface(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "module_interface_fixture.elisa")
	interfacePath := filepath.Join(fixtureDir, "module_interface_fixture.elisai")
	src := "struct Box[T]:\n    value: T\n\nstruct Expr in owner:\n    next: owner Expr&?\n\nglobal counter: int = 0\n\nstatic interface Builder:\n    type State\n    def state() -> State\n\ndef identity[T](value: T) -> T:\n    return value\n\n@internal\ndef hidden_identity[T](value: T) -> T:\n    return value\n\ndef needs_builder[B: Builder]() -> B.State can[Console.Write]:\n    can Console.Write:\n        signal Console.Write\n    return B.state()\n\nnamespace util:\n    def inc(value: int) -> int:\n        return value + 1\n\n    @internal\n    def hidden_inc(value: int) -> int:\n        return value + 1\n\nnamespace hidden_only:\n    @internal\n    def nope(value: int) -> int:\n        return value\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write interface fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "iface", "-o", interfacePath, fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected interface emit to succeed, stderr:\n%s", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected interface emit with -o not to write stdout, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	data, err := os.ReadFile(interfacePath)
	if err != nil {
		t.Fatalf("expected interface output file %s to exist: %v", interfacePath, err)
	}
	interfaceSource := string(data)
	for _, check := range []string{
		"struct Box[T]:",
		"struct Expr in owner:",
		"next: owner Expr&?",
		"extern counter: int",
		"extern identity[T](value: T) -> T",
		"static interface Builder:",
		"extern needs_builder[B: Builder]() -> B.State can[Console.Write]",
		"namespace util:",
		"extern inc(value: int) -> int",
	} {
		if !strings.Contains(interfaceSource, check) {
			t.Fatalf("expected interface source to contain %q, got:\n%s", check, interfaceSource)
		}
	}
	for _, bad := range []string{"return value", "global counter: int = 0", "hidden_identity", "hidden_inc", "hidden_only"} {
		if strings.Contains(interfaceSource, bad) {
			t.Fatalf("expected interface source to omit %q, got:\n%s", bad, interfaceSource)
		}
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runCLI([]string{"-emit", "ast", interfacePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected generated interface to parse successfully, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected generated interface parse to be warning-free, got:\n%s", stderr.String())
	}
	for _, check := range []string{"extern identity[T](1 params) -> T", "extern needs_builder[B: Builder](0 params) -> B.State can[Console.Write]", "extern counter: int"} {
		if !strings.Contains(stdout.String(), check) {
			t.Fatalf("expected generated interface AST to contain %q, got:\n%s", check, stdout.String())
		}
	}
}
func TestRunCLIEmitsSourceDependenciesJSON(t *testing.T) {
	fixtureDir := t.TempDir()
	leafPath := filepath.Join(fixtureDir, "leaf.elisa")
	midPath := filepath.Join(fixtureDir, "mid.elisa")
	rootPath := filepath.Join(fixtureDir, "root.elisa")
	for path, content := range map[string]string{
		leafPath: "def leaf() -> int:\n    return 1\n",
		midPath:  "# include \"leaf.elisa\"\n\ndef mid() -> int:\n    return leaf()\n",
		rootPath: "# include \"mid.elisa\"\n\ndef main() -> int:\n    return mid()\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "deps-json", rootPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected deps-json emit to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	var report sourceDependencyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("expected deps-json output to decode: %v\n%s", err, stdout.String())
	}
	rootAbs, _ := filepath.Abs(rootPath)
	midAbs, _ := filepath.Abs(midPath)
	leafAbs, _ := filepath.Abs(leafPath)
	if report.Root != rootAbs {
		t.Fatalf("expected root dependency %s, got %s", rootAbs, report.Root)
	}
	want := []string{rootAbs, midAbs, leafAbs}
	if len(report.Files) != len(want) {
		t.Fatalf("expected %d dependencies, got %d (%v)", len(want), len(report.Files), report.Files)
	}
	for i, got := range report.Files {
		if got != want[i] {
			t.Fatalf("dependency %d mismatch: got %s want %s", i, got, want[i])
		}
	}
}
func TestRunCLIEmitsSourceDependenciesJSONForBareInclude(t *testing.T) {
	fixtureDir := t.TempDir()
	leafPath := filepath.Join(fixtureDir, "leaf.elisa")
	midPath := filepath.Join(fixtureDir, "mid.elisa")
	rootPath := filepath.Join(fixtureDir, "root.elisa")
	for path, content := range map[string]string{
		leafPath: "def leaf() -> int:\n    return 1\n",
		midPath:  "include \"leaf.elisa\"\n\ndef mid() -> int:\n    return leaf()\n",
		rootPath: "include \"mid.elisa\"\n\ndef main() -> int:\n    return mid()\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "deps-json", rootPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected deps-json emit to succeed, stderr:\n%s", stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	var report sourceDependencyReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("expected deps-json output to decode: %v\n%s", err, stdout.String())
	}
	rootAbs, _ := filepath.Abs(rootPath)
	midAbs, _ := filepath.Abs(midPath)
	leafAbs, _ := filepath.Abs(leafPath)
	if report.Root != rootAbs {
		t.Fatalf("expected root dependency %s, got %s", rootAbs, report.Root)
	}
	want := []string{rootAbs, midAbs, leafAbs}
	if len(report.Files) != len(want) {
		t.Fatalf("expected %d dependencies, got %d (%v)", len(want), len(report.Files), report.Files)
	}
	for i, got := range report.Files {
		if got != want[i] {
			t.Fatalf("dependency %d mismatch: got %s want %s", i, got, want[i])
		}
	}
}
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
func TestParseArgsRejectsRemovedPackedABI(t *testing.T) {
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
	options, err := parseArgs([]string{"-emit", "packed-info", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitPacked {
		t.Fatalf("expected packed emit mode, got %q", options.emit)
	}
}
func TestParseArgsAcceptsLoweredEmitAlias(t *testing.T) {
	options, err := parseArgs([]string{"-emit", "lower", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitLowered {
		t.Fatalf("expected lowered emit mode, got %q", options.emit)
	}
}
func TestParseArgsAcceptsSemanticEmitAlias(t *testing.T) {
	options, err := parseArgs([]string{"-emit", "sema", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitSemantic {
		t.Fatalf("expected semantic emit mode, got %q", options.emit)
	}
}
func TestParseArgsAcceptsFactTraceEmitAlias(t *testing.T) {
	options, err := parseArgs([]string{"-emit", "fact-trace", "fixture.elisa"})
	if err != nil {
		t.Fatalf("parseArgs returned error: %v", err)
	}
	if options.emit != emitFacts {
		t.Fatalf("expected facts emit mode, got %q", options.emit)
	}
}
func TestRunCLIWritesLoweredGrammarSourceToDefaultPath(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "lowered_fixture.elisa")
	src := "grammar PascalFrontend:\n    expression(state: mutable ParserState&) -> Token:\n        token(TokenKind.IDENT)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write lowered fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "lowered", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected no stdout output, got:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got:\n%s", stderr.String())
	}
	outputPath := filepath.Join(fixtureDir, "lowered_fixture"+loweredExtension)
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read lowered output file: %v", err)
	}
	output := string(data)
	if strings.Contains(output, "grammar PascalFrontend:") {
		t.Fatalf("expected standalone lowered output to omit source grammar declarations, got:\n%s", output)
	}
	for _, want := range []string{
		"def expression(state: mutable ParserState&) -> Token:",
		"state.expect_kind(TokenKind.IDENT)",
		"return (true, __grammar_committed_",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected lowered output to contain %q, got:\n%s", want, output)
		}
	}
}
func TestRunCLIEmitsSemanticReport(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "semantic_fixture.elisa")
	src := "const enum TokenKind of u32:\n    IDENT = 1\n\nstruct Token:\n    kind: TokenKind\n\nstruct ParserState:\n    cursor: mutable usize\n\nimpl mutable ParserState&:\n    def expect_kind(self: mutable ParserState&, kind: TokenKind) -> Token:\n        _ = kind\n        return Token{kind: TokenKind.IDENT}\n\ngrammar PascalFrontend:\n    expression(state: mutable ParserState&) -> Token:\n        token(TokenKind.IDENT)\n"
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
		"=== lowered ===",
		"def expression(state: mutable ParserState&) -> Token:",
		"=== semantic ===",
		"func expression",
		"signature: func(mutable ParserState&) -> Token",
		"func __grammar_try__PascalFrontend__expression",
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
func TestRunCLIEmitsFactTraceForGrammarLoweredPaths(t *testing.T) {
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "grammar_fact_fixture.elisa")
	src := "const enum TokenKind of u32:\n    IDENT = 1\n\nstruct Token:\n    kind: TokenKind\n\nstruct ParserState:\n    cursor: mutable usize\n\nimpl mutable ParserState&:\n    def expect_kind(self: mutable ParserState&, kind: TokenKind) -> Token:\n        _ = kind\n        return Token{kind: TokenKind.IDENT}\n\ngrammar PascalFrontend:\n    expression(state: mutable ParserState&) -> Token:\n        token(TokenKind.IDENT)\n"
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write grammar fact fixture: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "facts", "-filter", "function=eq:__grammar_try__PascalFrontend__expression,mode=eq:summary", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("runCLI returned %d\nstderr:\n%s", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"func __grammar_try__PascalFrontend__expression", "path_facts=[", "state.cursor{root=state,path=cursor,steps=field:cursor}", "alias-class#0"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected grammar fact trace to contain %q, got:\n%s", want, output)
		}
	}
}
func TestRunCLIEmitsFactTraceReport(t *testing.T) {
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
