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

func TestRetainedGenericFunctionBinderDiskABI(t *testing.T) {
	for _, name := range []string{"ELISACORE_PARALLEL_IR", "ELISACORE_PARALLEL_OPT", "ELISACORE_IR_IMPORTS", "ELISACORE_NATIVE_FRAGMENTS", "ELISACORE_TEST_NATIVE_WORKERS"} {
		t.Setenv(name, "1")
	}
	a, b := "binder_address_a", ""
	for i := 0; i < 20; i++ {
		candidate := fmt.Sprintf("binder_address_b%d", i)
		if partitionOwner(a, 4) != partitionOwner(candidate, 4) {
			b = candidate
			break
		}
	}
	if b == "" {
		t.Fatal("no distinct address owner")
	}
	base := fmt.Sprintf(`struct Pair layout(c):
    left: i64
    right: i64
    extra: i64
def first[T](value: T) -> T:
    return bounce(value, 0)
def bounce[T](value: T, depth: i64) -> T:
    if depth == 0:
        return value
    return first(value)

def relay[T](value: T) -> T:
    return first[T](value)

def wide(value: Pair) -> Pair:
    return relay[Pair](value)
export fn wide(value: Pair) -> Pair = wide

def small(value: u8) -> u8:
    return relay[u8](value)
export fn small(value: u8) -> u8 = small

def ref(value: u8&) -> u8&:
    return relay[u8&](value)
export fn ref(value: u8&) -> u8& = ref

def %s() -> fn(i64) -> i64:
    return first[i64]
export fn from_a() -> fn(i64) -> i64 = %s

def %s() -> fn(i64) -> i64:
    return first[i64]
export fn from_b() -> fn(i64) -> i64 = %s
`, a, a, b, b)
	renamed := strings.Replace(base, "first[T](value: T) -> T:", "first[Payload](value: Payload) -> Payload:", 1)
	parse := func(source string) *ast.File {
		l := lexer.New("generic-function-edit.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		file := p.ParseFile("generic-function-edit.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("parse fixture")
		}
		return file
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, source := range []string{renamed, base, renamed} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		replay, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if len(replay.Errors()) != 0 {
			t.Fatal(replay.Errors())
		}
		cold := parseAndAnalyzeBackendTest(t, "generic-function-edit.elisa", source)
		for _, debug := range []bool{false, true} {
			got, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(replay, OptimizationLevel0, DefaultPackedLoweringProfile(), "", debug)
			if err != nil {
				t.Fatal(err)
			}
			want, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(cold, OptimizationLevel0, DefaultPackedLoweringProfile(), "", debug)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("debug=%v cold IR mismatch", debug)
			}
		}
		dir := t.TempDir()
		client := filepath.Join(dir, "client.c")
		if err := os.WriteFile(client, []byte(`#include <stdint.h>
#include <stddef.h>
void arena_free(void *p){(void)p;}
typedef struct {int64_t left,right,extra;} Pair;
typedef int64_t(*Fn)(int64_t);
extern Pair wide(Pair);
extern uint8_t small(uint8_t),*ref(uint8_t*);
extern Fn from_a(void),from_b(void);
int main(void){Pair p={91234567890LL,-71234567890LL,0x123456789abcdefLL},q=wide(p);
 if(q.left!=p.left||q.right!=p.right||q.extra!=p.extra)return 11;
 if(small(237)!=237)return 12;
 uint8_t byte=193;if(ref(&byte)!=&byte||*ref(&byte)!=193)return 13;
 Fn a=from_a(),b=from_b();if(a!=b||a(91234567890LL)!=91234567890LL||b(-71234567890LL)!=-71234567890LL)return 14;
 return 0;}
`), 0600); err != nil {
			t.Fatal(err)
		}
		for label, result := range map[string]*semantic.Result{"replay": replay, "cold": cold} {
			session := NewIncrementalObjectSession(4, 32<<20)
			session.minWeight = 0
			object := filepath.Join(dir, label+".o")
			if err := session.WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3}); err != nil {
				t.Fatal(err)
			}
			if session.Stats().SerialFallbacks != 0 {
				t.Fatal("independent IR declined")
			}
			binary := filepath.Join(dir, label)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			out, err := exec.CommandContext(ctx, "clang", object, client, "-o", binary).CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("link %v %s", err, out)
			}
			ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
			out, err = exec.CommandContext(ctx, binary).CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("%s %v %s", label, err, out)
			}
		}
	}
}
