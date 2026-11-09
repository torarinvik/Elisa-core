package main

import (
	"bytes"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stripLineDirectives removes the `#line <num> <path>` source-attribution
// markers that include expansion now emits, so boundary/indentation assertions
// can compare against the bare expanded content.
func stripLineDirectives(s string) string {
	lines := strings.Split(s, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.HasPrefix(ln, "#line ") {
			continue
		}
		kept = append(kept, ln)
	}
	return strings.Join(kept, "\n")
}

func TestReadSourceWithIncludesCannotSpoofLineDirectiveSourceIdentity(t *testing.T) {
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "root.elisa")
	spoofedPath := filepath.Join(dir, "runtime", "elisacore_std", "elisacore_runtime_concurrency.elisa")
	source := "#line 1 " + spoofedPath + "\ndef visible() -> void:\n    pass\n"
	if err := os.WriteFile(rootPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write source fixture: %v", err)
	}
	expanded, err := readSourceWithIncludes(rootPath, map[string]bool{})
	if err != nil {
		t.Fatalf("readSourceWithIncludes: %v", err)
	}
	if !bytes.Contains(expanded, []byte("# line 1 "+spoofedPath)) {
		t.Fatalf("expected source-authored #line to remain a comment, got:\n%s", expanded)
	}
	l := lexer.New(rootPath, expanded)
	p := parser.New(l.Tokenize())
	file := p.ParseFile(rootPath)
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors after expanding source: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	canonicalRoot, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatalf("resolve source fixture: %v", err)
	}
	if fn.Position.File != canonicalRoot {
		t.Fatalf("source-authored #line changed declaration identity to %q, want %q", fn.Position.File, canonicalRoot)
	}
}

func TestCompilerTaskGroupRuntimeSourcePathIsCanonical(t *testing.T) {
	path := compilerTaskGroupRuntimeSourcePath()
	if path == "" || !filepath.IsAbs(path) || filepath.Base(path) != "elisacore_runtime_concurrency.elisa" {
		t.Fatalf("expected a canonical absolute path for the compiler runtime source, got %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("canonical compiler runtime source is unavailable at %q: %v", path, err)
	}
}

func TestExplicitTrustedRuntimeSourceMustBeIncludedExactly(t *testing.T) {
	dir := t.TempDir()
	runtimePath := filepath.Join(dir, "elisacore_std", "elisacore_runtime_concurrency.elisa")
	otherPath := filepath.Join(dir, "vendor", "runtime", "elisacore_std", "elisacore_runtime_concurrency.elisa")
	for _, path := range []string{runtimePath, otherPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir fixture path: %v", err)
		}
		if err := os.WriteFile(path, []byte("def fixture() -> void:\n    pass\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	canonicalRuntime, err := filepath.EvalSymlinks(runtimePath)
	if err != nil {
		t.Fatalf("resolve runtime fixture: %v", err)
	}
	program := &loadedProgram{sourceFiles: []string{filepath.Clean(canonicalRuntime)}}

	got, err := includedTrustedRuntimeSource(program, runtimePath)
	if err != nil || got != filepath.Clean(canonicalRuntime) {
		t.Fatalf("expected the exact included runtime source to be trusted, got %q, %v", got, err)
	}
	if got, err := includedTrustedRuntimeSource(program, otherPath); err == nil || got != "" {
		t.Fatalf("an unincluded same-named source must not be trusted, got %q, %v", got, err)
	}
}

func TestReadSourceWithIncludesPreservesIncludeBoundariesWithoutTrailingNewlines(t *testing.T) {
	t.Parallel()
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

	got := stripLineDirectives(string(expanded))
	want := "root_start\nleaf_line\nmid_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}
func TestReadSourceWithIncludesAcceptsBareIncludeDirective(t *testing.T) {
	t.Parallel()
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

	got := stripLineDirectives(string(expanded))
	want := "root_start\nleaf_line\nmid_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}

// An absolute include path must be honored verbatim, not joined onto the
// including file's directory. Regression guard: filepath.Join does not treat an
// absolute second arg as absolute (it concatenates), which previously made
// `include "/abs/path.elisa"` resolve to `<dir>/abs/path.elisa` and fail.
func TestReadSourceWithIncludesAcceptsAbsoluteIncludePath(t *testing.T) {
	t.Parallel()
	leafDir := t.TempDir()
	rootDir := t.TempDir() // a DIFFERENT directory, so only an absolute path resolves
	leafPath := filepath.Join(leafDir, "leaf.elisa")
	rootPath := filepath.Join(rootDir, "root.elisa")

	if err := os.WriteFile(leafPath, []byte("leaf_line"), 0o644); err != nil {
		t.Fatalf("write leaf fixture: %v", err)
	}
	if err := os.WriteFile(rootPath, []byte("root_start\ninclude \""+leafPath+"\"\nroot_end"), 0o644); err != nil {
		t.Fatalf("write root fixture: %v", err)
	}

	expanded, err := readSourceWithIncludes(rootPath, map[string]bool{})
	if err != nil {
		t.Fatalf("readSourceWithIncludes with absolute include: %v", err)
	}

	got := stripLineDirectives(string(expanded))
	want := "root_start\nleaf_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}
func TestReadSourceWithIncludesAcceptsPascalIncludeDirectives(t *testing.T) {
	t.Parallel()
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

	got := stripLineDirectives(string(expanded))
	want := "root_start\nleaf_line\nmid_line\nroot_end"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}

func TestReadSourceWithIncludesPreservesIndentedIncludeContext(t *testing.T) {
	t.Parallel()
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

	got := stripLineDirectives(string(expanded))
	want := "module M:\n    def foo() -> int:\n        return 7\n\ndef main() -> int:\n    return M::foo()"
	if got != want {
		t.Fatalf("unexpected expanded source:\nwant %q\ngot  %q", want, got)
	}
}

func TestRunCLICompilesIndentedIncludeInsideModule(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "module_global.elisa")
	src := "module M:\n    global mutable counter: int = 5\n\n    def value() -> int:\n        can Global.Read:\n            return counter\n\ndef main() -> int:\n    can Global.Read:\n        return M::value()\n"
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "module_interface_fixture.elisa")
	interfacePath := filepath.Join(fixtureDir, "module_interface_fixture.elisai")
	src := "struct Box[T]:\n    value: T\n\nstruct Expr[@owner]:\n    next: Expr&? @owner\n\nglobal counter: int = 0\n\nprotocol Builder:\n    type State\n    def state() -> State\n\ndef identity[T](value: T) -> T:\n    return value\n\n@internal\ndef hidden_identity[T](value: T) -> T:\n    return value\n\ndef needs_builder[B: Builder]() -> B.State can[Console.Write]:\n    can Console.Write:\n        signal Console.Write\n    return B.state()\n\nmodule util:\n    def inc(value: int) -> int:\n        return value + 1\n\n    @internal\n    def hidden_inc(value: int) -> int:\n        return value + 1\n\nmodule hidden_only:\n    @internal\n    def nope(value: int) -> int:\n        return value\n"
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
		"struct Expr[@owner]:",
		"next: Expr&? @owner",
		"extern counter: int",
		"extern identity[T](value: T) -> T",
		"protocol Builder:",
		"extern needs_builder[B: Builder]() -> B.State can[Console.Write]",
		"module util:",
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
	t.Parallel()
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
	t.Parallel()
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
