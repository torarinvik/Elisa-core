package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/backend"
)

func TestParseArgsLTOAndTargetCPU(t *testing.T) {
	t.Setenv(backend.TargetCPUEnv, "")
	cases := []struct {
		args []string
		lto  backend.LTOMode
		cpu  string
	}{
		{[]string{"x.elisa"}, backend.LTOOff, ""},
		{[]string{"-flto", "x.elisa"}, backend.LTOFull, ""},
		{[]string{"-flto=full", "x.elisa"}, backend.LTOFull, ""},
		{[]string{"-flto=thin", "x.elisa"}, backend.LTOThin, ""},
		{[]string{"-flto", "-fno-lto", "x.elisa"}, backend.LTOOff, ""},
		{[]string{"-mcpu=native", "x.elisa"}, backend.LTOOff, "native"},
		{[]string{"-mcpu", "apple-m1", "-flto", "x.elisa"}, backend.LTOFull, "apple-m1"},
		{[]string{"-mcpu=host", "x.elisa"}, backend.LTOOff, "native"},
	}
	for _, c := range cases {
		options, err := parseArgs(c.args)
		if err != nil {
			t.Fatalf("parseArgs(%v): %v", c.args, err)
		}
		if options.lto != c.lto || options.targetCPU != c.cpu {
			t.Fatalf("parseArgs(%v): lto=%q cpu=%q, want lto=%q cpu=%q", c.args, options.lto, options.targetCPU, c.lto, c.cpu)
		}
		if c.cpu != "" && backend.RequestedTargetCPU() != c.cpu {
			t.Fatalf("parseArgs(%v) did not publish -mcpu to the backend (got %q)", c.args, backend.RequestedTargetCPU())
		}
	}
	for _, bad := range [][]string{{"-flto=bogus", "x.elisa"}, {"-mcpu=", "x.elisa"}, {"-mcpu"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Fatalf("parseArgs(%v) accepted an invalid LTO/CPU flag", bad)
		}
	}
}

func TestProjectLTOFieldIsValidatedAndShown(t *testing.T) {
	dir := t.TempDir()
	writeLTOFixture(t, dir, `"lto": "thin", "target-cpu": "native"`)
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"project", "view", "--project", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("project view failed: %s%s", stdout.String(), stderr.String())
	}
	for _, want := range []string{"LTO: thin", "Target CPU: native"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("project view missing %q:\n%s", want, stdout.String())
		}
	}
	bad := t.TempDir()
	writeLTOFixture(t, bad, `"lto": "medium"`)
	stdout.Reset()
	stderr.Reset()
	if code := runCLI([]string{"project", "view", "--project", bad}, &stdout, &stderr); code == 0 || !strings.Contains(stderr.String(), `unsupported lto "medium"`) {
		t.Fatalf("expected an unsupported-lto error, got code=%d:\n%s%s", code, stdout.String(), stderr.String())
	}
}

const ltoFixtureSource = `extern printf(format: cstr, ...) -> i32 can[Console, Unsafe.RawExtern]

def main() -> i32 can[Memory.Allocate, Abort.Panic, Console, Unsafe.RawExtern]:
    xs: mutable darray[i64] = []
    for i in 0..<100000:
        xs.push(i.i64() * 3)
    total: mutable i64 = 0
    for x in xs:
        total <- total + x
    printf("%lld\n", total)
    return 0
`

func writeLTOFixture(t *testing.T, dir string, extra string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "main.elisa"), []byte(ltoFixtureSource), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := `{"targets": {"default": {"entry": "main.elisa", "emit": "obj", "output": "prog", "opt": "O2", ` + extra + `}}}`
	if err := os.WriteFile(filepath.Join(dir, "project.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ltoToolchainOrSkip skips unless clang can do an LTO link with the LLVM version that
// wrote the bitcode (on macOS: a non-Apple clang that passes its own libLTO to ld64).
func ltoToolchainOrSkip(t *testing.T) {
	t.Helper()
	clangPath, err := exec.LookPath("clang")
	if err != nil {
		t.Skip("clang not available")
	}
	if _, err := exec.LookPath("nm"); err != nil {
		t.Skip("nm not available")
	}
	out, _ := exec.Command(clangPath, "--version").CombinedOutput()
	if runtime.GOOS == "darwin" && strings.Contains(string(out), "Apple clang") {
		t.Skip("Apple clang's libLTO cannot read this LLVM's bitcode")
	}
	if runtime.GOOS != "darwin" {
		if _, err := exec.LookPath("ld.lld"); err != nil {
			t.Skip("ld.lld not available for an ELF LTO link")
		}
	}
}

func buildLTOFixture(t *testing.T, lto string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	extra := `"lto": "off"`
	if lto != "" {
		extra = `"lto": "` + lto + `"`
	}
	writeLTOFixture(t, dir, extra)
	var stdout, stderr bytes.Buffer
	if code := runCLI([]string{"build", "--project", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("build (lto=%q) failed:\n%s%s", lto, stdout.String(), stderr.String())
	}
	exe := filepath.Join(dir, "prog")
	run, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("running lto=%q executable: %v\n%s", lto, err, run)
	}
	if strings.TrimSpace(string(run)) != "14999850000" {
		t.Fatalf("lto=%q executable printed %q, want 14999850000", lto, run)
	}
	symbols, err := exec.Command("nm", exe).Output()
	if err != nil {
		t.Fatalf("nm %s: %v", exe, err)
	}
	return exe, string(symbols)
}

// An -flto build links bitcode for the program, the default runtime and the weak profiler
// hooks, so code generation sees one unit: the per-allocation profiler negotiation (a weak C
// stub in its own object without LTO) is inlined away, and the runtime's arena entry points
// stop being exported cross-object calls.
func TestNativeFullLTOBuildRunsAndInlinesAcrossRuntimeObjects(t *testing.T) {
	ltoToolchainOrSkip(t)
	_, plain := buildLTOFixture(t, "")
	_, full := buildLTOFixture(t, "full")

	negotiate := regexp.MustCompile(`(?m) [Tt] _?elisa_profile_allocation_negotiate$`)
	if !negotiate.MatchString(plain) {
		t.Fatalf("baseline: expected the profiler negotiation stub as a separate function:\n%s", plain)
	}
	if negotiate.MatchString(full) {
		t.Fatalf("-flto: elisa_profile_allocation_negotiate survived as a function; it should be inlined:\n%s", full)
	}
	exportedArena := regexp.MustCompile(`(?m) T _?arena_alloc$`)
	if !exportedArena.MatchString(plain) {
		t.Fatalf("baseline: expected arena_alloc exported from the runtime object:\n%s", plain)
	}
	if exportedArena.MatchString(full) {
		t.Fatalf("-flto: arena_alloc is still an exported cross-object symbol:\n%s", full)
	}
}

func TestNativeThinLTOBuildRuns(t *testing.T) {
	ltoToolchainOrSkip(t)
	buildLTOFixture(t, "thin")
}

// `-emit obj -flto` writes LLVM bitcode, like `clang -flto -c`.
func TestEmitObjectFLTOWritesBitcode(t *testing.T) {
	ltoToolchainOrSkip(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "main.elisa")
	if err := os.WriteFile(src, []byte("def main() -> i32:\n    return 7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"-flto", "-flto=thin"} {
		obj := filepath.Join(dir, strings.TrimPrefix(mode, "-")+".o")
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "obj", "-O2", mode, "-o", obj, src}, &stdout, &stderr); code != 0 {
			t.Fatalf("-emit obj %s failed:\n%s%s", mode, stdout.String(), stderr.String())
		}
		data, err := os.ReadFile(obj)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(data, []byte("BC\xc0\xde")) && !bytes.HasPrefix(data, []byte{0xde, 0xc0, 0x17, 0x0b}) {
			t.Fatalf("-emit obj %s did not write LLVM bitcode (header % x)", mode, data[:min(8, len(data))])
		}
		exe := filepath.Join(dir, strings.TrimPrefix(mode, "-"))
		link := exec.Command("clang", mode, "-O2", obj, "-o", exe)
		if out, err := link.CombinedOutput(); err != nil {
			t.Fatalf("clang %s link of the Elisa bitcode object failed: %v\n%s", mode, err, out)
		}
		if err := exec.Command(exe).Run(); err == nil {
			t.Fatalf("expected exit status 7 from %s", exe)
		} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 7 {
			t.Fatalf("expected exit status 7 from %s, got %v", exe, err)
		}
	}
}

func TestLLCCodegenLevelFollowsOptimizationLevel(t *testing.T) {
	if got := llcCodegenOptArg(backend.OptimizationLevel0); got != "-O0" {
		t.Fatalf("-O0 build: llc %s, want -O0", got)
	}
	for _, level := range []backend.OptimizationLevel{backend.OptimizationLevel1, backend.OptimizationLevel2, backend.OptimizationLevel3} {
		if got := llcCodegenOptArg(level); got != "-O2" {
			t.Fatalf("-O%d build: llc %s, want -O2 (LLVMCodeGenLevelDefault, as -emit obj)", int(level), got)
		}
	}
	if _, err := findLLC(); err != nil {
		t.Skip("llc not available")
	}
	ir := filepath.Join(t.TempDir(), "m.ll")
	if err := os.WriteFile(ir, []byte("define i32 @f() {\n  ret i32 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	llc, _ := findLLC()
	k0, ok0 := llcObjectCacheKey(ir, "-O0", "", llc)
	k2, ok2 := llcObjectCacheKey(ir, "-O2", "", llc)
	if !ok0 || !ok2 || k0 == k2 {
		t.Fatalf("llc object cache must key on the codegen level (ok=%v/%v)", ok0, ok2)
	}
}

// The panic path declares printf for its message. C's printf returns a 32-bit int, which is how
// programs declare it; the panic path insisted on `int` (i64), so a program declaring
// `extern printf(...) -> i32` that could panic failed with "conflicting LLVM function
// declaration for printf". Both declared widths must coexist with the panic path.
func TestPanicPrintfCoexistsWithUserPrintfDeclaration(t *testing.T) {
	dir := t.TempDir()
	for _, ret := range []string{"i32", "int"} {
		src := filepath.Join(dir, "printf_"+ret+".elisa")
		program := "extern printf(format: cstr, ...) -> " + ret + " can[Console, Unsafe.RawExtern]\n\n" +
			"def main(argc: i64, argv: mutable void&) -> i64 can[Console, Unsafe.RawExtern, Abort.Panic]:\n" +
			"    if argc > 5:\n        panic(\"too many\")\n" +
			"    _ = printf(\"%lld\\n\", argc)\n    return 0\n"
		if err := os.WriteFile(src, []byte(program), 0o644); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := runCLI([]string{"-emit", "obj", "-O2", "-o", filepath.Join(dir, ret+".o"), src}, &stdout, &stderr); code != 0 {
			t.Fatalf("printf -> %s with a panic path failed to compile:\n%s", ret, stderr.String())
		}
	}
}
