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

func TestRetainedEnumStorageDiskABI(t *testing.T) {
	const before = `const enum Choice of u8:
    First
    Second

type Alias = Choice
struct Holder layout(c):
    pad: u8
    choice: Alias
    payload: u32
struct Outer:
    held: Holder

@inline(never)
def echo_holder(value: Holder) -> Holder:
    return value

def probe() -> u64:
    value: Holder = Holder{pad: 17, choice: Choice.Second, payload: 4275878552}
    got: Holder = echo_holder(value)
    return got.pad.u64() + got.choice.u64() + got.payload.u64()
export fn probe() -> u64 = probe

def layout() -> usize:
    return size_of(Holder) + align_of(Holder) + offset_of(Holder, choice) + size_of(Outer)
export fn layout() -> usize = layout
`
	parse := func(source string) *ast.File {
		l := lexer.New("enum-storage.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("enum-storage.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("fixture parse failed")
		}
		return f
	}
	original, state := semantic.AnalyzeRetained(parse(before), parse(before), []byte(before), semantic.AnalyzeOptions{})
	if len(original.Errors()) != 0 {
		t.Fatalf("fixture: %v", original.Errors())
	}
	for _, width := range []int{16, 64, 8} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			data, err := state.MarshalState()
			if err != nil {
				t.Fatal(err)
			}
			state, err = semantic.RestoreAnalysisState(data)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.Replace(before, "Choice of u8:", fmt.Sprintf("Choice of u%d:", width), 1)
			result, err := state.Update(parse(source), parse(source), []byte(source))
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Errors()) != 0 {
				t.Fatalf("updated: %v", result.Errors())
			}
			cold := parseAndAnalyzeBackendTest(t, "enum-storage.elisa", source)
			replayIR, err := GenerateLLVMIR(result)
			if err != nil {
				t.Fatal(err)
			}
			coldIR, err := GenerateLLVMIR(cold)
			if err != nil {
				t.Fatal(err)
			}
			if replayIR != coldIR {
				t.Fatal("disk replay IR differs from cold analysis")
			}

			dir := t.TempDir()
			client := fmt.Sprintf(`#include <stddef.h>
#include <stdint.h>
void arena_free(void *owner) { (void)owner; }
typedef struct { uint8_t pad; uint%d_t choice; uint32_t payload; } Holder;
extern uint64_t probe(void);
extern size_t layout(void);
int main(void) {
 if(probe()!=4275878570ull) return 11;
 if(layout()!=2*sizeof(Holder)+_Alignof(Holder)+offsetof(Holder,choice)) return 12;
 return 0;
}`, width)
			clientPath := filepath.Join(dir, "client.c")
			if err := os.WriteFile(clientPath, []byte(client), 0600); err != nil {
				t.Fatal(err)
			}
			// Both replay and cold compilation must agree with platform layout and
			// preserve internal aggregate calls across storage-width changes.
			for name, analysis := range map[string]*semantic.Result{"replay": result, "cold": cold} {
				object := filepath.Join(dir, name+".o")
				session := NewIncrementalObjectSession(4, 32<<20)
				if err := session.WriteObject(analysis, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, Serial: true}); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				executable := filepath.Join(dir, name)
				out, err := exec.CommandContext(ctx, "clang", clientPath, object, "-o", executable).CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("%s link: %v\n%s", name, err, out)
				}
				ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
				out, err = exec.CommandContext(ctx, executable).CombinedOutput()
				cancel()
				if err != nil {
					t.Fatalf("%s ABI: %v\n%s", name, err, out)
				}
			}
		})
	}
}

// A constant referenced by a value contract is read by codegen too (including
// debug assertions). Replayed expression facts must match a new analysis.
func TestRetainedConstContractDiskIR(t *testing.T) {
	const base = `const LIMIT: i64 = 1

def answer() -> i64:
    ensure result == LIMIT
    return LIMIT

def caller() -> i64:
    ensure result == LIMIT
    return answer()
export fn caller() -> i64 = caller
`
	parse := func(source string) *ast.File {
		l := lexer.New("const-contract.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("const-contract.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("fixture parse failed")
		}
		return f
	}
	_, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	for _, value := range []string{"2", "1", "3"} {
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		source := strings.Replace(base, "LIMIT: i64 = 1", "LIMIT: i64 = "+value, 1)
		got, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := parseAndAnalyzeBackendTest(t, "const-contract.elisa", source)
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
				t.Fatalf("constant %s debug=%v: replay IR differs", value, debug)
			}
		}
	}
}

func TestRetainedExplicitEnumDiskIRAndNative(t *testing.T) {
	const base = `const enum Choice of u16:
    First
    Second

def first() -> i64:
    return Choice.First.i64()
export fn first() -> i64 = first

def second() -> i64:
    return Choice.Second.i64()
export fn second() -> i64 = second

def classify() -> i64:
    match Choice.Second:
        Choice.First:
            return 11
        Choice.Second:
            return 22
export fn classify() -> i64 = classify
`
	parse := func(source string) *ast.File {
		l := lexer.New("explicit-enum.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("explicit-enum.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatal("parse fixture")
		}
		return f
	}
	_, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	for _, change := range []struct {
		storage, value string
		first, second  int
	}{{"u16", "300", 300, 301}, {"u16", "(250 + 50)", 300, 301}, {"i16", "-12", -12, -11}, {"u16", "", 0, 1}} {
		source := strings.Replace(base, "Choice of u16", "Choice of "+change.storage, 1)
		if change.value != "" {
			source = strings.Replace(source, "    First\n", "    First = "+change.value+"\n", 1)
		}
		data, err := state.MarshalState()
		if err != nil {
			t.Fatal(err)
		}
		state, err = semantic.RestoreAnalysisState(data)
		if err != nil {
			t.Fatal(err)
		}
		result, err := state.Update(parse(source), parse(source), []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		cold := parseAndAnalyzeBackendTest(t, "explicit-enum.elisa", source)
		for _, debug := range []bool{false, true} {
			got, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(result, OptimizationLevel0, DefaultPackedLoweringProfile(), "", debug)
			if err != nil {
				t.Fatal(err)
			}
			want, err := GenerateLLVMIRWithOptAndPackedLoweringProfileForTargetDebug(cold, OptimizationLevel0, DefaultPackedLoweringProfile(), "", debug)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("%s %s debug=%v: cold/replay IR differ", change.storage, change.value, debug)
			}
		}
		dir := t.TempDir()
		object := filepath.Join(dir, "enum.o")
		if err := NewIncrementalObjectSession(4, 32<<20).WriteObject(result, object, LLVMObjectEmitOptions{OptLevel: OptimizationLevel3, Serial: true}); err != nil {
			t.Fatal(err)
		}
		client := filepath.Join(dir, "client.c")
		text := fmt.Sprintf("#include <stdint.h>\nextern int64_t first(void), second(void), classify(void);\nint main(void) { return first()==%d && second()==%d && classify()==22 ? 0 : 71; }\n", change.first, change.second)
		if err := os.WriteFile(client, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		binary := filepath.Join(dir, "client")
		out, err := exec.CommandContext(ctx, "clang", object, client, "-o", binary).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("link: %v %s", err, out)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		out, err = exec.CommandContext(ctx, binary).CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("enum values/dispatch: %v %s", err, out)
		}
	}
}
