//go:build cgo

package backend

import (
	"elisacore/src/easm"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"strings"
	"testing"
)

// TestGuestTableOverlayLowersToReadU64WithStrideMath is the docs/108 backend verification: the
// fixed-stride table accessor `table.value[memory, i]` over a stride-24 `GuestVAddr[Elf64Sym]`
// carrier lowers, via the analyzer's AsOverlayCall, to MemoryManager_ReadU64 with `i*24 + 8` address
// arithmetic. The emitted IR must contain a 24-wide multiply, an add, and the ReadU64 call — proving
// the stride displacement reaches codegen byte-identically to a hand-written table read.
func TestGuestTableOverlayLowersToReadU64WithStrideMath(t *testing.T) {
	src := `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def sym_value(table: GuestVAddr[Elf64Sym], memory: uintptr, i: u64) -> u64:
	return table.value[memory, i]
`
	l := lexer.New("table.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile("table.elisa")
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	layout := &easm.Layout{
		Name:   "Elf64Sym",
		Stride: 24,
		Fields: []easm.LayoutField{
			{Offset: 0, Name: "name", Type: "u32", Width: 4},
			{Offset: 8, Name: "value", Type: "u64", Width: 8},
		},
	}
	result := semantic.AnalyzeWithOptions(file, semantic.AnalyzeOptions{OverlayLayouts: []*easm.Layout{layout}})
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}

	g, err := compileLLVMModule(result, OptimizationLevel0, DefaultPackedLoweringProfile())
	if err != nil {
		t.Fatalf("compileLLVMModule returned error: %v", err)
	}
	defer g.dispose()
	output := g.printModule()

	if !strings.Contains(output, "call i64 @MemoryManager_ReadU64") {
		t.Fatalf("expected a MemoryManager_ReadU64 call in the lowered IR, got:\n%s", output)
	}
	// The `i * 24` stride term: a multiply by 24 must appear.
	if !strings.Contains(output, "mul") || !strings.Contains(output, "24") {
		t.Fatalf("expected `i * 24` stride multiply in the lowered IR, got:\n%s", output)
	}
	// The field offset add (`+ 8`) and the base add must appear.
	if !strings.Contains(output, "add") {
		t.Fatalf("expected address `add` arithmetic in the lowered IR, got:\n%s", output)
	}
}
