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
