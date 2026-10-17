//go:build cgo

package backend

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestIncrementalOperatorEnvironmentLLVMAndNativeParity(t *testing.T) {
	const program = `def work(value: i64) -> i64:
    return value + 1

def main() -> i64:
    return work(20)
`
	const unrelated = `struct Tag:
    value: int

protocol Other:
    def state() -> int

impl Other for Tag:
    def state() -> int:
        return 1

`
	const applicable = `protocol Add:
    def __add__(left: Self, right: Self) -> Self

impl Add for i64:
    def __add__(left: i64, right: i64) -> i64:
        return left

`
	session := semantic.NewIncrementalAnalysisSession()
	analyze := func(source string, warm bool) *semantic.Result {
		t.Helper()
		l := lexer.New("incremental_operator.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("incremental_operator.elisa")
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
	analyze(program, true)
	config := os.Getenv("LLVM_CONFIG")
	if config == "" {
		config, _ = exec.LookPath("llvm-config")
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
	for _, step := range []struct {
		source string
		hits   uint64
		status int
	}{{unrelated + program, 1, 21}, {applicable + program, 0, 20}, {program, 1, 21}} {
		before := session.Stats()
		warm := analyze(step.source, true)
		cold := analyze(step.source, false)
		if session.Stats().PrimaryBodiesReused-before.PrimaryBodiesReused != step.hits {
			t.Fatal("wrong operator-environment reuse")
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
			t.Fatal("operator environment changed cold/warm LLVM parity")
		}
		if clang == "" {
			t.Log("clang unavailable; LLVM parity checked")
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
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != step.status {
			t.Fatalf("want status%d got%v", step.status, err)
		}
	}
}
