//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersBitsetAndBitfieldGroups(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "bitgroups.elisa", `const enum Mode of u4:
	None = 0
	Fast = 3

struct Header:
	flags: bitset:
		has_read
		has_write
	layout: bitfield:
		tag: u4
		mode: Mode
		active: u1

def build() -> bool:
	header: Header = zeroed
	header.flags.has_read <- true
	header.layout.tag <- 7
	header.layout.mode <- Mode.Fast
	return header.flags.has_read and header.layout.tag == 7 and header.layout.mode == Mode.Fast
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "%Header = type { i8, i16 }") && !strings.Contains(output, "%Header = type { i8, i8 }") {
		t.Fatalf("expected Header to lower packed groups to integer backing fields, got IR:\n%s", output)
	}
	for _, want := range []string{"packed.shift", "packed.mask", "packed.clear", "packed.store"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected bitgroup IR fragment %q, got IR:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRLowersExplicitStructLayoutModes(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "struct_layout_modes.elisa", `struct Header layout(packed):
	tag: u4
	arity: u3
	active: u1

struct CHeader layout(c):
	kind: u32
	flags: u32
	size: usize

global header: Header = zeroed
global c_header: CHeader = zeroed
`)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}
	if !strings.Contains(output, "%Header = type <{ i4, i3, i1 }>") {
		t.Fatalf("expected Header to lower as an LLVM packed struct, got IR:\n%s", output)
	}
	if !strings.Contains(output, "%CHeader = type { i32, i32, i64 }") {
		t.Fatalf("expected CHeader to lower as an unpacked LLVM struct, got IR:\n%s", output)
	}
}
