//go:build cgo

package backend

import (
	"context"
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRetainedGenericStructDiskInstancesAndABI(t *testing.T) {
	const base = `struct Box[T] layout(c):
    value: T

struct Pair layout(c):
    left: i64
    right: i64

type Big = Box[Pair]
type Small = Box[u8]
type RefBox = Box[u8&]
struct Outer:
    box: Big

def echo[T](value: Box[T]) -> Box[T]:
    return value

def get[T](value: Box[T]) -> T:
    return value.value

def echo_big(value: Big) -> Big:
    return echo[Pair](value)
export fn echo_big(value: Big) -> Big = echo_big

def get_small(value: Small) -> u8:
    return get[u8](value)
export fn get_small(value: Small) -> u8 = get_small

def get_ref(value: RefBox) -> u8&:
    return get[u8&](value)
export fn get_ref(value: RefBox) -> u8& = get_ref

def layout() -> usize:
    return size_of(Big) + size_of(Outer) + align_of(Big) + offset_of(Big, value) + size_of(Small) + offset_of(Small, value)
export fn layout() -> usize = layout
`
	parse := func(source string) *ast.File {
		l := lexer.New("generic-record-edit.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("generic-record-edit.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("parse fixture")
		}
		return f
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, mode := range []string{"renamed", "base", "added", "renamed-added", "reordered", "renamed", "base"} {
		source := base
		prefix, suffix := "", ""
		if mode == "added" || mode == "renamed-added" {
			source = strings.Replace(base, "    value: T\n", "    value: T\n    probe: u64 = 0\n", 1)
			suffix = "uint64_t probe;"
		}
		if mode == "reordered" {
			source = strings.Replace(base, "    value: T\n", "    probe: u64 = 0\n    value: T\n", 1)
			prefix = "uint64_t probe;"
		}
		if strings.HasPrefix(mode, "renamed") {
			source = strings.Replace(source, "Box[T] layout(c):", "Box[Payload] layout(c):", 1)
			source = strings.Replace(source, "    value: T\n", "    value: Payload\n", 1)
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
		cold := parseAndAnalyzeBackendTest(t, "generic-record-edit.elisa", source)
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
				t.Fatalf("mode=%s debug=%v stale generic IR", mode, debug)
			}
		}
		assignProbe, checkProbe := "", ""
		if prefix != "" || suffix != "" {
			assignProbe = "big.probe=0xfedcba9876543210ULL;"
			checkProbe = " || copied.probe!=big.probe"
		}
		client := fmt.Sprintf(`#include <stdint.h>
#include <stddef.h>
typedef struct {int64_t left,right;} Pair;
typedef struct {%s Pair value; %s} Big;
typedef struct {%s uint8_t value; %s} Small;
typedef struct {%s uint8_t *value; %s} RefBox;
typedef struct {Big box;} Outer;
extern Big echo_big(Big);
extern uint8_t get_small(Small);
extern uint8_t *get_ref(RefBox);
extern size_t layout(void);
int main(void){
 if(layout()!=sizeof(Big)+sizeof(Outer)+_Alignof(Big)+offsetof(Big,value)+sizeof(Small)+offsetof(Small,value))return 11;
 Big big={0};big.value.left=91234567890LL;big.value.right=-71234567890LL; %s
 Big copied=echo_big(big);if(copied.value.left!=big.value.left || copied.value.right!=big.value.right%s)return 12;
 Small small={0};small.value=237;if(get_small(small)!=237)return 13;
 uint8_t byte=193;RefBox ref={0};ref.value=&byte;uint8_t *got=get_ref(ref);if(got!=&byte || *got!=193)return 14;
 return 0;
}`, prefix, suffix, prefix, suffix, prefix, suffix, assignProbe, checkProbe)
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
				t.Fatalf("%s mode=%s: %v %s", label, mode, err, out)
			}
		}
	}
}
