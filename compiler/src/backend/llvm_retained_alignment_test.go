//go:build cgo

package backend

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

func TestRetainedAlignmentDiskStorage(t *testing.T) {
	const base = `struct Pair:
    value: i64

global counter: Pair = zeroed

@callconv(c)
extern observe(value: Pair&) -> i64

def check() -> i64:
    local: Pair = zeroed
    return observe(local) + observe(counter)
export fn check() -> i64 = check
`
	parse := func(source string) *ast.File {
		l := lexer.New("align-edit.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("align-edit.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("parse fixture")
		}
		return f
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, alignment := range []int{64, 128, 0, 16, 0} {
		source := base
		if alignment != 0 {
			source = fmt.Sprintf("@align(%d)\n", alignment) + base
		}
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		got, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Errors()) != 0 {
			t.Fatal(got.Errors())
		}
		cold := parseAndAnalyzeBackendTest(t, "align-edit.elisa", source)
		for _, debug := range []bool{false, true} {
			replayIR, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(got, OptimizationLevel0, DefaultPackedLoweringProfile(), "", debug)
			if err != nil {
				t.Fatal(err)
			}
			coldIR, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(cold, OptimizationLevel0, DefaultPackedLoweringProfile(), "", debug)
			if err != nil {
				t.Fatal(err)
			}
			if replayIR != coldIR {
				t.Fatalf("alignment=%d debug=%v: stale IR", alignment, debug)
			}
		}
		required := alignment
		if required == 0 {
			required = 8
		}
		client := fmt.Sprintf(`#include <stdint.h>
extern int64_t check(void);
int64_t observe(const int64_t *p) { return ((uintptr_t)p %% %d) || *p != 0 ? 1 : 0; }
int main(void) {return (int)check();}
`, required)
		dir := t.TempDir()
		clientPath := filepath.Join(dir, "client.c")
		if err := os.WriteFile(clientPath, []byte(client), 0600); err != nil {
			t.Fatal(err)
		}
		for label, result := range map[string]*semantic.Result{"replay": got, "cold": cold} {
			object := filepath.Join(dir, label+".o")
			if err := NewIncrementalObjectSession(4, 32<<20).WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, Serial: true}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			binary := filepath.Join(dir, label)
			out, err := exec.CommandContext(ctx, "clang", object, clientPath, "-o", binary).CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("link %v: %s", err, out)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
			out, err = exec.CommandContext(ctx, binary).CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("%s alignment %d: %v %s", label, alignment, err, out)
			}
		}
	}
}
