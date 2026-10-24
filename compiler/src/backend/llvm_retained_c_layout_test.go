//go:build cgo

package backend

import (
	"context"
	"fmt"
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

func TestRetainedCRecordDiskABIThreshold(t *testing.T) {
	const base = `struct Pair layout(c):
    left: i64
    right: i64

type Alias = Pair
struct Outer:
    pair: Alias

def layout() -> usize:
    return size_of(Alias) + align_of(Pair) + offset_of(Pair, right) + size_of(Outer)
export fn layout() -> usize = layout

def echo(value: Pair) -> Pair:
    return value
export fn echo(value: Pair) -> Pair = echo

@callconv(c)
extern c_echo(value: Pair) -> Pair

def through_c(value: Pair) -> Pair:
    return c_echo(value)
export fn through_c(value: Pair) -> Pair = through_c
`
	parse := func(source string) *ast.File {
		l := lexer.New("c-record-edit.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("c-record-edit.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("parse fixture")
		}
		return f
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatalf("fixture: %v", initial.Errors())
	}
	for _, big := range []bool{true, false, true} {
		source := base
		extra, initializer, change, check := "", "", "", ""
		if big {
			source = strings.Replace(base, "    right: i64\n", "    right: i64\n    extra: i64 = 0\n", 1)
			extra = "int64_t extra;"
			initializer = ", -8123456789LL"
			change = "value.extra += 19;"
			check = " || got.extra != value.extra"
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
			t.Fatalf("replay: %v", got.Errors())
		}
		cold := parseAndAnalyzeBackendTest(t, "c-record-edit.elisa", source)
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
				t.Fatalf("big=%v debug=%v: stale C ABI/layout IR", big, debug)
			}
		}
		client := fmt.Sprintf(`#include <stdint.h>
#include <stddef.h>
typedef struct { int64_t left, right; %s } Pair;
extern Pair echo(Pair), through_c(Pair);
extern size_t layout(void);
Pair c_echo(Pair value) { value.left += 5; value.right -= 7; %s return value; }
int main(void) {
 if(layout()!=2*sizeof(Pair)+_Alignof(Pair)+offsetof(Pair,right)) return 13;
 Pair value={91234567890LL,-71234567890LL%s}, got=echo(value);
 if(got.left!=value.left || got.right!=value.right%s) return 11;
 got=through_c(value); value=c_echo(value);
 if(got.left!=value.left || got.right!=value.right%s) return 12;
 return 0;
}`, extra, change, initializer, check, check)
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
				t.Fatalf("link: %v %s", err, out)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
			out, err = exec.CommandContext(ctx, binary).CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("%s big=%v C ABI: %v %s", label, big, err, out)
			}
		}
	}
}
