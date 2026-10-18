//go:build cgo

package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

func TestIncrementalSessionNativeAndLLVMParity(t *testing.T) {
	for _, integer := range []string{"i64", "int"} {
		for _, audit := range []bool{false, true} {
			name := integer
			if audit {
				name += "/audit"
			}
			t.Run(name, func(t *testing.T) {
				testIncrementalIntegerNativeAndLLVMParity(t, integer, audit)
			})
		}
	}
}

func testIncrementalIntegerNativeAndLLVMParity(t *testing.T, integer string, audit bool) {
	source := strings.ReplaceAll(`def first(value: Integer) -> Integer:
    return value + 1

def second() -> Integer:
    return 20 + 1

def main() -> i64:
    return first(20) + second()
`, "Integer", integer)
	session := semantic.NewIncrementalAnalysisSessionWithOptions(semantic.IncrementalAnalysisSessionOptions{AuditLexicalReads: audit})
	analyze := func(src string, useSession bool) *semantic.Result {
		t.Helper()
		l := lexer.New("incremental_native.elisa", []byte(src))
		tokens := l.Tokenize()
		if len(l.Errors()) != 0 {
			t.Fatal(l.Errors())
		}
		p := parser.New(tokens)
		file := p.ParseFile("incremental_native.elisa")
		if len(p.Errors()) != 0 {
			t.Fatal(p.Errors())
		}
		var result *semantic.Result
		if useSession {
			result = session.Analyze(file, semantic.AnalyzeOptions{})
		} else {
			result = semantic.Analyze(file)
		}
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
		return result
	}
	analyze(source, true)
	before := session.Stats()
	currentSource := "\n\n" + source
	for _, test := range []struct {
		source string
		status int
		reused uint64
	}{{currentSource, 42, 2}, {strings.Replace(currentSource, "20 + 1", "20 + 2", 1), 43, 1}} {
		current := analyze(test.source, true)
		cold := analyze(test.source, false)
		got, err := generateLLVMIRWithDefaultPackedLoweringForTest(current)
		if err != nil {
			t.Fatal(err)
		}
		want, err := generateLLVMIRWithDefaultPackedLoweringForTest(cold)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatal("actual session reuse changed LLVM IR")
		}
		after := session.Stats()
		if after.PrimaryBodiesReused-before.PrimaryBodiesReused != test.reused {
			t.Fatalf("wrong primary reuse count: %+v / %+v", before, after)
		}
		before = after
		// Use the same LLVM toolchain as this compiler's cgo backend.
		config := os.Getenv("LLVM_CONFIG")
		if config == "" {
			if discovered, err := exec.LookPath("llvm-config"); err == nil {
				config = discovered
			}
		}
		clang := ""
		if config != "" {
			if resolved, err := exec.LookPath(config); err == nil {
				config = resolved
			}
			clang = filepath.Join(filepath.Dir(config), "clang")
		} else if discovered, err := exec.LookPath("clang"); err == nil {
			clang = discovered
		}
		if clang == "" {
			t.Log("native qualification unavailable: no clang toolchain; LLVM parity still checked")
			continue
		}
		if _, err := exec.LookPath(clang); err != nil {
			t.Logf("native qualification unavailable: %v; LLVM parity still checked", err)
			continue
		}
		dir := t.TempDir()
		ir := filepath.Join(dir, "main.ll")
		exe := filepath.Join(dir, "main")
		if err := os.WriteFile(ir, []byte(got), 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(clang, "-O0", "-x", "ir", ir, "-o", exe).CombinedOutput(); err != nil {
			t.Fatalf("native link failed: %v\n%s", err, output)
		}
		err = exec.Command(exe).Run()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != test.status {
			t.Fatalf("native output changed: want status %d, got %v", test.status, err)
		}
	}
}

func TestIncrementalLocalInitializerNativeAndLLVMParity(t *testing.T) {
	source := `def work(value: i64) -> i64:
    first: i64 = value + 1
    second: i64 = first * 2
    return second

def main() -> i64:
    return work(20)
`
	session := semantic.NewIncrementalAnalysisSession()
	analyze := func(source string, warm bool) *semantic.Result {
		t.Helper()
		l := lexer.New("incremental_locals.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("incremental_locals.elisa")
		if len(l.Errors()) != 0 || len(p.Errors()) != 0 {
			t.Fatal("invalid test source")
		}
		var result *semantic.Result
		if warm {
			result = session.Analyze(file, semantic.AnalyzeOptions{})
		} else {
			result = semantic.Analyze(file)
		}
		if len(result.Errors()) != 0 {
			t.Fatal(result.Errors())
		}
		return result
	}
	analyze(source, true)
	for _, step := range []struct {
		source string
		status int
		hits   uint64
	}{{source, 42, 2}, {strings.Replace(source, "first * 2", "first * 3", 1), 63, 1}, {strings.Replace(strings.Replace(source, "first * 2", "first * 3", 1), "value + 1", "value + 3", 1), 69, 0}} {
		before := session.Stats()
		warm := analyze(step.source, true)
		cold := analyze(step.source, false)
		if session.Stats().LocalInitializersReused-before.LocalInitializersReused != step.hits {
			t.Fatal("wrong local reuse count")
		}
		got, err := generateLLVMIRWithDefaultPackedLoweringForTest(warm)
		if err != nil {
			t.Fatal(err)
		}
		want, err := generateLLVMIRWithDefaultPackedLoweringForTest(cold)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatal("initializer reuse changed LLVM IR")
		}
		config := os.Getenv("LLVM_CONFIG")
		if config == "" {
			if discovered, err := exec.LookPath("llvm-config"); err == nil {
				config = discovered
			}
		}
		clang := ""
		if config != "" {
			if path, err := exec.LookPath(config); err == nil {
				config = path
			}
			clang = filepath.Join(filepath.Dir(config), "clang")
		} else {
			clang, _ = exec.LookPath("clang")
		}
		if clang == "" {
			t.Log("native toolchain unavailable; LLVM parity checked")
			continue
		}
		dir := t.TempDir()
		ir := filepath.Join(dir, "main.ll")
		exe := filepath.Join(dir, "main")
		if err := os.WriteFile(ir, []byte(got), 0600); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(clang, "-O0", "-x", "ir", ir, "-o", exe).CombinedOutput(); err != nil {
			t.Fatalf("native link: %v %s", err, output)
		}
		err = exec.Command(exe).Run()
		status, ok := err.(*exec.ExitError)
		if !ok || status.ExitCode() != step.status {
			t.Fatalf("native result: expected%d got%v", step.status, err)
		}
	}
}
