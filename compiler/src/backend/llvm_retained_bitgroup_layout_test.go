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

// The C boundary stays scalar: inline groups intentionally prohibit exporting
// Packet itself. Internal non-inlined echoes exercise aggregate ABI lowering
// as Packet grows from 12 to 24 bytes across the 16-byte threshold.
func TestRetainedInlineBitGroupDiskABIThreshold(t *testing.T) {
	const base = `const enum Mode of u4:
    Off = 0
    Fast = 3

struct Packet layout(c):
    prefix: mutable u32
    flags: bitset:
        f0
        f1
        f2
        f3
        f4
        f5
        f6
        f7
    bits: bitfield:
        tag: u3
        mode: u4
        active: u1
    state: bitfield:
        tag: u3
        mode: Mode
        active: u1
    suffix: mutable u32

type Alias = Packet
struct Outer layout(c):
    packet: Alias
struct Box[T]:
    value: T

@inline(never)
def echo_packet(value: Packet) -> Packet:
    return value

@inline(never)
def echo_outer(value: Outer) -> Outer:
    return value

def identity[T](value: T) -> T:
    return value

def generic_alias(value: Box[Alias]) -> bool:
    return identity(value.value.flags).f7 and identity(value.value.bits).mode == 13

def nested_alias(value: Outer) -> bool:
    return identity(value.packet.state).mode == Mode.Fast

def layout_probe() -> usize:
    return size_of(Packet) + align_of(Packet) + offset_of(Packet, prefix) + offset_of(Packet, flags) + offset_of(Packet, bits) + offset_of(Packet, state) + offset_of(Packet, suffix) + size_of(Outer) + offset_of(Outer, packet)
export fn layout_probe() -> usize = layout_probe

def probe() -> bool:
    packet: mutable Packet = zeroed
    packet.prefix <- 123456789
    packet.suffix <- 987654321
    packet.flags.f7 <- true
    packet.bits.tag <- 5
    packet.bits.mode <- 13
    packet.bits.active <- 1
    packet.state.tag <- 6
    packet.state.mode <- Mode.Fast
    packet.state.active <- 1
    copy: Packet = echo_packet(packet)
    outer: Outer = echo_outer(Outer{packet: copy})
    box: Box[Alias] = Box[Alias]{value: outer.packet}
    return copy.prefix == 123456789 and copy.suffix == 987654321 and copy.flags.f7 and copy.bits.tag == 5 and copy.bits.mode == 13 and copy.bits.active == 1 and copy.state.tag == 6 and copy.state.mode == Mode.Fast and copy.state.active == 1 and generic_alias(box) and nested_alias(outer)
export fn probe() -> bool = probe

def wide_probe() -> bool:
    return true
export fn wide_probe() -> bool = wide_probe
`
	parse := func(source string) *ast.File {
		l := lexer.New("retained-bitgroup.elisa", []byte(source))
		p := parser.New(l.Tokenize())
		f := p.ParseFile("retained-bitgroup.elisa")
		if len(l.Errors())+len(p.Errors()) != 0 {
			t.Fatalf("parse fixture: %v %v", l.Errors(), p.Errors())
		}
		return f
	}
	initial, state := semantic.AnalyzeRetained(parse(base), parse(base), []byte(base), semantic.AnalyzeOptions{})
	if len(initial.Errors()) != 0 {
		t.Fatalf("fixture: %v", initial.Errors())
	}
	wider := strings.Replace(base, "    prefix: mutable u32", "    wide_prefix: u64 = 0\n    prefix: mutable u32", 1)
	wider = strings.Replace(wider, "        f7\n", "        f7\n        f8\n", 1)
	wider = strings.Replace(wider, "        tag: u3\n        mode: u4\n        active: u1", "        active: u1\n        mode: u5\n        tag: u3", 1)
	wider = strings.Replace(wider, "def wide_probe() -> bool:\n    return true", `def wide_probe() -> bool:
    packet: mutable Packet = zeroed
    packet.flags.f8 <- true
    packet.bits.mode <- 31
    copy: Packet = echo_packet(packet)
    return copy.flags.f8 and copy.bits.mode == 31`, 1)
	for step, source := range []string{wider, strings.Replace(wider, "Mode of u4", "Mode of u5", 1), base} {
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
		cold := parseAndAnalyzeBackendTest(t, "retained-bitgroup.elisa", source)
		if len(got.Errors()) != 0 || fmt.Sprint(got.Diagnostics) != fmt.Sprint(cold.Diagnostics) {
			t.Fatalf("step %d diagnostics: replay=%v cold=%v", step, got.Diagnostics, cold.Diagnostics)
		}
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
				t.Fatalf("step %d debug=%v: stale bitgroup ABI/layout IR", step, debug)
			}
		}
		prefix, flags, bits, stateWidth, wideDecl, wideCheck := "", "uint8_t", "uint8_t", "uint8_t", "", ""
		if step < 2 {
			prefix, flags, bits = "uint64_t wide_prefix;", "uint16_t", "uint16_t"
			wideDecl, wideCheck = "extern _Bool wide_probe(void);", "if (!wide_probe()) return 12;"
		}
		if step == 1 {
			stateWidth = "uint16_t"
		}
		client := fmt.Sprintf(`#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
// Same owner-tracking runtime support as llvm_merge_functions_test.go.
// These signatures match the emitted arena ABI, and free handles owners that
// have no allocations as well as allocations made by specialized functions.
struct Allocation { void *owner, *memory; struct Allocation *next; };
static struct Allocation *allocations;
void *arena_alloc(void *owner, size_t bytes) {
 struct Allocation *entry=malloc(sizeof(*entry)); if(!entry) abort();
 entry->owner=owner; entry->memory=calloc(1,bytes); if(!entry->memory) abort();
 entry->next=allocations; allocations=entry; return entry->memory;
}
void arena_free(void *owner) {
 struct Allocation **entry=&allocations;
 while(*entry) { if((*entry)->owner==owner) { struct Allocation *old=*entry; *entry=old->next; free(old->memory); free(old); } else { entry=&(*entry)->next; } }
}
typedef struct { %s uint32_t prefix; %s flags; %s bits; %s state; uint32_t suffix; } Packet;
typedef struct { Packet packet; } Outer;
extern size_t layout_probe(void);
extern _Bool probe(void);
%s
int main(void) {
 size_t expected=sizeof(Packet)+_Alignof(Packet)+offsetof(Packet,prefix)+offsetof(Packet,flags)+offsetof(Packet,bits)+offsetof(Packet,state)+offsetof(Packet,suffix)+sizeof(Outer)+offsetof(Outer,packet);
 if(layout_probe()!=expected) return 10;
 if(!probe()) return 11;
 %s
 return 0;
}`, prefix, flags, bits, stateWidth, wideDecl, wideCheck)
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
				t.Fatalf("%s step=%d: %v %s", label, step, err, out)
			}
		}
	}
}
