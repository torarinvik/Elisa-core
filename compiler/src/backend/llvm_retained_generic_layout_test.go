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

func TestRetainedGenericRecordDiskLayouts(t *testing.T) {
	const base = `struct Pair[T]:
    tag: u8
    value: mutable T
    extra: i32

type Alias = Pair[i64]
struct Outer:
    pair: Alias

def layout() -> usize:
    return size_of(Alias) + align_of(Alias) + offset_of(Alias, value) + offset_of(Alias, extra) + size_of(Outer)
export fn layout() -> usize = layout

def echo(value: Alias) -> Alias:
    return value

def fold(value: i64) -> i64:
    pair: Alias = Alias{tag: 7, value: value, extra: 31}
    copied: Alias = echo(pair)
    return copied.value + copied.extra.i64() + copied.tag.i64()
export fn fold(value: i64) -> i64 = fold

def read_value(value: Alias&) -> i64:
    return value.value
export fn read_value(value: Alias&) -> i64 = read_value

def write_value(value: mutable Alias&, payload: i64) -> void:
    value.value <- payload
export fn write_value(value: mutable Alias&, payload: i64) -> void = write_value
`
	parse := func(source string) *ast.File {
		l := lexer.New("generic-layout-edit.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("generic-layout-edit.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("parse fixture")
		}
		return f
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatal(initial.Errors())
	}
	for _, mode := range []string{"packed", "c", "", "packed", ""} {
		source := base
		if mode != "" {
			source = strings.Replace(base, "struct Pair[T]:", "struct Pair[T] layout("+mode+"):", 1)
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
		cold := parseAndAnalyzeBackendTest(t, "generic-layout-edit.elisa", source)
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
				t.Fatalf("mode=%s debug=%v stale IR", mode, debug)
			}
		}
		attribute := ""
		if mode == "packed" {
			attribute = "__attribute__((packed))"
		}
		client := fmt.Sprintf(`#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
// The scalar-only fold fixture creates no arena allocations.
void arena_free(void *owner) {(void)owner;}
typedef struct %s {uint8_t tag;int64_t value;int32_t extra;} Pair;
typedef struct {Pair pair;} Outer;
extern size_t layout(void);
extern int64_t fold(int64_t),read_value(const Pair*);
extern void write_value(Pair*,int64_t);
int main(void) {
 if(layout()!=sizeof(Pair)+_Alignof(Pair)+offsetof(Pair,value)+offsetof(Pair,extra)+sizeof(Outer))return 11;
 if(fold(91234567890LL)!=91234567928LL)return 12;
 unsigned char *storage=malloc(4*sizeof(Pair)+16);if(!storage)return 99;
 Pair *pairs=(Pair*)(storage+(_Alignof(Pair)==1?3:0));
 for(int i=0;i<4;i++){pairs[i].tag=7;pairs[i].extra=31;write_value(&pairs[i],-71234567890LL+i);}
 for(int i=0;i<4;i++)if(read_value(&pairs[i])!=-71234567890LL+i || pairs[i].value!=-71234567890LL+i || pairs[i].tag!=7 || pairs[i].extra!=31)return 13;
 free(storage);return 0;
}`, attribute)
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
			out, err := exec.CommandContext(ctx, "clang", object, clientPath, "-O3", "-o", binary).CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("link %v %s", err, out)
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
