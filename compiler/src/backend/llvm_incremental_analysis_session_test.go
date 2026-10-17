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
	const source = `def first() -> i64:
    return 20 + 1

def second() -> i64:
    return 20 + 1

def main() -> i64:
    return first() + second()
`
	session := semantic.NewIncrementalAnalysisSession()
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
