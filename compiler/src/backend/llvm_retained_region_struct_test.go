//go:build cgo

package backend

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

func TestRetainedRegionStructDiskIRAndNative(t *testing.T) {
	const base = `struct Box[@r]:
    value: i64
    bytes: darray[u8] @r

struct Outer[@r]:
    held: Box[r] @r

@inline(never)
def construct[@r](arena: mutable Arena& @r) -> Box[r] @r:
    return Box{value: 17, bytes: []}

@inline(never)
def relay[@r](value: Box[r] @r) -> Box[r] @r:
    return value

def layout[@r](value: Box[r]&) -> usize:
    return size_of(Box[r]) + align_of(Box[r]) + offset_of(Box[r], bytes)

def nested[@r](value: Outer[r]&) -> usize:
    return size_of(Outer[r])

def main() -> i64 can[Memory.Allocate, Abort.Panic]:
    arena: mutable Arena = zeroed
    value: Box[arena] = construct(&arena)
    forwarded: Box[arena] = relay(value)
    outer: Outer[arena] = Outer{held: forwarded}
    if layout(&forwarded) == 0 or nested(&outer) == 0:
        return 1
    return forwarded.value.i64() + outer.held.bytes.count.i64()
`
	parse := func(source string) *ast.File {
		l := lexer.New("region-record.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("region-record.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatalf("fixture parse: %v %v", l.Errors(), p.Errors())
		}
		return f
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatalf("fixture: %v", initial.Errors())
	}
	for _, source := range []string{strings.Replace(base, "    value: i64\n", "    value: i64\n    probe: u64 = 0\n", 1), strings.Replace(base, "    value: i64\n", "    value: u8\n", 1), base} {
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
			t.Fatalf("replay: %v", got.Errors())
		}
		cold := parseAndAnalyzeBackendTest(t, "region-record.elisa", source)
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
				t.Fatalf("debug=%v: replay differs from cold IR", debug)
			}
		}
		dir := t.TempDir()
		object := filepath.Join(dir, "module.o")
		session := NewIncrementalObjectSession(4, 32<<20)
		if err := session.WriteObject(got, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, Serial: true}); err != nil {
			t.Fatal(err)
		}
		shim := filepath.Join(dir, "arena.c")
		if err := os.WriteFile(shim, []byte("#include <stdlib.h>\nvoid arena_free(void *owner) {(void)owner;}\nvoid *arena_alloc(void *owner, size_t bytes) {(void)owner; return calloc(1,bytes);}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		executable := filepath.Join(dir, "program")
		out, err := exec.CommandContext(ctx, "clang", object, shim, "-o", executable).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("link: %v\n%s", err, out)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		err = exec.CommandContext(ctx, executable).Run()
		cancel()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 17 {
			t.Fatalf("native result: %v, want 17", err)
		}
	}
}
