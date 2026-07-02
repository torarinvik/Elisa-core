package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/easm"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
)

// orbisProcParamLayout is the docs/107 §(a)/§(d) example layout: the OrbisProcParam guest struct
// whose `size`@0 and `mem_param`@64 fields the emulator's linker reads by computed address.
func orbisProcParamLayout() *easm.Layout {
	return &easm.Layout{
		Name: "OrbisProcParam",
		Fields: []easm.LayoutField{
			{Offset: 0, Name: "size", Type: "u64", Width: 8},
			{Offset: 64, Name: "mem_param", Type: "u64", Width: 8},
			{Offset: 4, Name: "magic", Type: "u32", Width: 4},
		},
	}
}

func analyzeOverlaySource(t *testing.T, src string) *Result {
	t.Helper()
	l := lexer.New("overlay.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("overlay.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return AnalyzeWithOptions(file, AnalyzeOptions{OverlayLayouts: []*easm.Layout{orbisProcParamLayout()}})
}

// findOverlayIndex finds the single overlay-rewritten IndexExpr (AsOverlayCall set) in a `return
// EXPR` function body — sufficient for these focused tests (the accessor under test is the return
// value of each function).
func findOverlayIndex(result *Result) *ast.IndexExpr {
	for _, decl := range result.ActiveFile().Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, stmt := range fn.Body {
			ret, ok := stmt.(*ast.ReturnStmt)
			if !ok {
				continue
			}
			if idx, ok := ret.Value.(*ast.IndexExpr); ok && idx.AsOverlayCall != nil {
				return idx
			}
		}
	}
	return nil
}

// TestGuestOverlayReadDesugarsToReadU64 is the docs/107 increment 5a end-to-end test: a real .elisa
// program reads a guest-memory field through a declared layout via `proc_param.mem_param[memory]`,
// and the analyzer lowers it to the byte-identical MemoryManager_ReadU64(memory, proc_param + 64).
func TestGuestOverlayReadDesugarsToReadU64(t *testing.T) {
	result := analyzeOverlaySource(t, `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def load_mem_param(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr) -> u64:
	return proc_param.mem_param[memory]
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	idx := findOverlayIndex(result)
	if idx == nil {
		t.Fatal("expected an IndexExpr rewritten with AsOverlayCall")
	}
	call := idx.AsOverlayCall
	fn, ok := call.Func.(*ast.Ident)
	if !ok || fn.Name != "MemoryManager_ReadU64" {
		t.Fatalf("expected lowering to MemoryManager_ReadU64, got %#v", call.Func)
	}
	if len(call.Args) != 2 {
		t.Fatalf("expected 2 args (mem, base+offset), got %d", len(call.Args))
	}
	if mem, ok := call.Args[0].(*ast.Ident); !ok || mem.Name != "memory" {
		t.Fatalf("expected first arg `memory`, got %#v", call.Args[0])
	}
	bin, ok := call.Args[1].(*ast.BinaryExpr)
	if !ok || bin.Op != lexer.TOKEN_PLUS {
		t.Fatalf("expected `base + offset`, got %#v", call.Args[1])
	}
	off, ok := bin.Right.(*ast.IntLit)
	if !ok || off.Value != "64" {
		t.Fatalf("expected offset literal 64, got %#v", bin.Right)
	}
}

func TestGuestOverlayZeroOffsetReadOmitsAddition(t *testing.T) {
	result := analyzeOverlaySource(t, `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def load_size(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr) -> u64:
	return proc_param.size[memory]
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	idx := findOverlayIndex(result)
	if idx == nil {
		t.Fatal("expected overlay rewrite")
	}
	// offset 0: address is the bare cast base, no `+ 0`.
	if _, ok := idx.AsOverlayCall.Args[1].(*ast.CastExpr); !ok {
		t.Fatalf("expected zero-offset address to be the bare cast base, got %#v", idx.AsOverlayCall.Args[1])
	}
}

func TestGuestOverlayUnknownFieldRejected(t *testing.T) {
	result := analyzeOverlaySourceAllowingErrors(t, `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def load(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr) -> u64:
	return proc_param.nope[memory]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-unknown-field") {
		t.Fatalf("expected overlay-unknown-field diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func TestGuestOverlayWidthMismatchRejected(t *testing.T) {
	// `magic` is a u32 (4-byte) field, which has a ReadU32 form — so width itself is fine; assert a
	// non-power-of-two width is rejected. Use a layout field of odd width.
	odd := &easm.Layout{Name: "Odd", Fields: []easm.LayoutField{{Offset: 0, Name: "weird", Type: "u24", Width: 3}}}
	l := lexer.New("overlay.elisa", []byte(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def load(p: GuestVAddr[Odd], memory: uintptr) -> u64:
	return p.weird[memory]
`))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile("overlay.elisa")
	result := AnalyzeWithOptions(file, AnalyzeOptions{OverlayLayouts: []*easm.Layout{odd}})
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-field-width-mismatch") {
		t.Fatalf("expected overlay-field-width-mismatch diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// findOverlayWrite finds the single overlay-rewritten AssignStmt (AsOverlayCall set) in any function
// body of the result.
func findOverlayWrite(result *Result) *ast.AssignStmt {
	for _, decl := range result.ActiveFile().Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, stmt := range fn.Body {
			if as, ok := stmt.(*ast.AssignStmt); ok && as.AsOverlayCall != nil {
				return as
			}
		}
	}
	return nil
}

// TestGuestOverlayWriteDesugarsToWriteU64 is the docs/107 write-form counterpart: an assignment
// `proc_param.mem_param[memory] <- value` over a declared layout lowers to the byte-identical
// MemoryManager_WriteU64(memory, proc_param + 64, value).
func TestGuestOverlayWriteDesugarsToWriteU64(t *testing.T) {
	result := analyzeOverlaySource(t, `extern MemoryManager_WriteU64(mem: uintptr, addr: uintptr, value: u64) -> void

def store_mem_param(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr, value: u64) -> void:
	proc_param.mem_param[memory] <- value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	as := findOverlayWrite(result)
	if as == nil {
		t.Fatal("expected an AssignStmt rewritten with AsOverlayCall")
	}
	call := as.AsOverlayCall
	fn, ok := call.Func.(*ast.Ident)
	if !ok || fn.Name != "MemoryManager_WriteU64" {
		t.Fatalf("expected lowering to MemoryManager_WriteU64, got %#v", call.Func)
	}
	if len(call.Args) != 3 {
		t.Fatalf("expected 3 args (mem, base+offset, value), got %d", len(call.Args))
	}
	if mem, ok := call.Args[0].(*ast.Ident); !ok || mem.Name != "memory" {
		t.Fatalf("expected first arg `memory`, got %#v", call.Args[0])
	}
	bin, ok := call.Args[1].(*ast.BinaryExpr)
	if !ok || bin.Op != lexer.TOKEN_PLUS {
		t.Fatalf("expected `base + offset`, got %#v", call.Args[1])
	}
	if off, ok := bin.Right.(*ast.IntLit); !ok || off.Value != "64" {
		t.Fatalf("expected offset literal 64, got %#v", bin.Right)
	}
	if v, ok := call.Args[2].(*ast.Ident); !ok || v.Name != "value" {
		t.Fatalf("expected value arg `value`, got %#v", call.Args[2])
	}
}

// TestGuestOverlayWriteUnknownFieldRejected ensures the write path runs the same field resolution as
// the read path (an unknown field is rejected, not silently stored).
func TestGuestOverlayWriteUnknownFieldRejected(t *testing.T) {
	result := analyzeOverlaySourceAllowingErrors(t, `extern MemoryManager_WriteU64(mem: uintptr, addr: uintptr, value: u64) -> void

def store(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr, value: u64) -> void:
	proc_param.nope[memory] <- value
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-unknown-field") {
		t.Fatalf("expected overlay-unknown-field diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// analyzeBareSource analyzes a program with NO OverlayLayouts option supplied — the docs/107
// increment 5 path, where the layout must be declared in-source with a `layout` declaration.
func analyzeBareSource(src string) *Result {
	l := lexer.New("overlay.elisa", []byte(src))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile("overlay.elisa")
	return AnalyzeWithOptions(file, AnalyzeOptions{})
}

// TestGuestOverlayInSourceLayoutReadAndWrite is the docs/107 increment 5 end-to-end test: a program
// declares its own `layout` and both reads and writes its fields through `GuestVAddr[L]` carriers,
// with NO option-supplied layouts. Both accessors desugar to the byte-identical MemoryManager calls.
func TestGuestOverlayInSourceLayoutReadAndWrite(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64
extern MemoryManager_WriteU64(mem: uintptr, addr: uintptr, value: u64) -> void

struct OrbisProcParam layout(guest, size: 80):
	size: u64 at 0
	mem_param: u64 at 64

def load(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr) -> u64:
	return proc_param.mem_param[memory]

def store(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr, value: u64) -> void:
	proc_param.mem_param[memory] <- value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	idx := findOverlayIndex(result)
	if idx == nil {
		t.Fatal("expected the read accessor to desugar against the in-source layout")
	}
	if fn, ok := idx.AsOverlayCall.Func.(*ast.Ident); !ok || fn.Name != "MemoryManager_ReadU64" {
		t.Fatalf("read: expected MemoryManager_ReadU64, got %#v", idx.AsOverlayCall.Func)
	}
	as := findOverlayWrite(result)
	if as == nil {
		t.Fatal("expected the write accessor to desugar against the in-source layout")
	}
	if fn, ok := as.AsOverlayCall.Func.(*ast.Ident); !ok || fn.Name != "MemoryManager_WriteU64" {
		t.Fatalf("write: expected MemoryManager_WriteU64, got %#v", as.AsOverlayCall.Func)
	}
}

// TestGuestOverlayInSourceUnknownFieldRejected: a field not declared in the in-source layout is
// rejected, proving the registered layout (not a permissive fallback) is what resolution consults.
func TestGuestOverlayInSourceUnknownFieldRejected(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisProcParam layout(guest, size: 80):
	size: u64 at 0

def load(proc_param: GuestVAddr[OrbisProcParam], memory: uintptr) -> u64:
	return proc_param.mem_param[memory]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-unknown-field") {
		t.Fatalf("expected overlay-unknown-field, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestGuestOverlayInSourceBadFieldTypeRejected: a layout field whose type has no fixed-width accessor
// is rejected at declaration time.
func TestGuestOverlayInSourceBadFieldTypeRejected(t *testing.T) {
	result := analyzeBareSource(`layout Bad:
	weird: f32 at 0

def f() -> void:
	return
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "no fixed-width accessor") {
		t.Fatalf("expected fixed-width-accessor diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestGuestOverlaySizeGuardRequiredRejectsUnguarded: a field tagged `requires size >= N` accessed
// with NO dominating size guard is rejected (docs/107 increment 4 obligation).
func TestGuestOverlaySizeGuardRequiredRejectsUnguarded(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	return mem_param.ext2[memory]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-field-needs-size-guard") {
		t.Fatalf("expected overlay-field-needs-size-guard, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestGuestOverlaySizeGuardDischargedByDominatingGuard: the same access, dominated by
// `if mem_param.size[memory] >= 48:`, is accepted — the front-end derives the SizeGuardFact.
func TestGuestOverlaySizeGuardDischargedByDominatingGuard(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	if mem_param.size[memory] >= 48:
		return mem_param.ext2[memory]
	return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("guarded access should be accepted, got: %v", errs)
	}
}

// TestGuestOverlaySizeGuardWeakerGuardRejected: a guard proving only `size >= 40` does NOT discharge
// a `requires size >= 48` obligation (the lower bound must reach the requirement).
func TestGuestOverlaySizeGuardWeakerGuardRejected(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	if mem_param.size[memory] >= 40:
		return mem_param.ext2[memory]
	return 0
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-field-needs-size-guard") {
		t.Fatalf("expected weaker guard to be rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestGuestOverlaySizeGuardScopedToBranch: the size-guard fact does not leak past the guarding `if`.
func TestGuestOverlaySizeGuardScopedToBranch(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	if mem_param.size[memory] >= 48:
		return 0
	return mem_param.ext2[memory]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-field-needs-size-guard") {
		t.Fatalf("expected the access outside the guarded branch to be rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestGuestOverlaySizeGuardEarlyReturnDischarges: the guard-clause idiom — `if size < N: return` with
// the access in the fall-through (where the negation `size >= N` dominates) — discharges the
// obligation (docs/107 increment 4, the early-return form).
func TestGuestOverlaySizeGuardEarlyReturnDischarges(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	if mem_param.size[memory] < 48:
		return 0
	return mem_param.ext2[memory]
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("early-return guard should discharge, got: %v", errs)
	}
}

// TestGuestOverlaySizeGuardEarlyReturnLessEqual: `if size <= 47: return` proves `size >= 48` in the
// fall-through (the <= boundary negates to > 47, i.e. >= 48).
func TestGuestOverlaySizeGuardEarlyReturnLessEqual(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	if mem_param.size[memory] <= 47:
		return 0
	return mem_param.ext2[memory]
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("early-return <= guard should discharge, got: %v", errs)
	}
}

// TestGuestOverlaySizeGuardEarlyReturnWeakerRejected: `if size < 40: return` only proves `size >= 40`
// in the fall-through, which does NOT discharge a `requires size >= 48` access.
func TestGuestOverlaySizeGuardEarlyReturnWeakerRejected(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	if mem_param.size[memory] < 40:
		return 0
	return mem_param.ext2[memory]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-field-needs-size-guard") {
		t.Fatalf("expected weaker early-return guard to be rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestGuestOverlaySizeGuardNonExitingGuardRejected: a guard clause whose then-branch does NOT exit
// leaves the negation un-proven in the fall-through (control reaches it on both branches), so the
// access is rejected.
func TestGuestOverlaySizeGuardNonExitingGuardRejected(t *testing.T) {
	result := analyzeBareSource(`extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64
extern note(x: u64) -> void

struct OrbisKernelMemParam layout(guest, size: 48):
	size: u64 at 0
	ext2: u64 at 40 requires size >= 48

def load(mem_param: GuestVAddr[OrbisKernelMemParam], memory: uintptr) -> u64:
	if mem_param.size[memory] < 48:
		note(1)
	return mem_param.ext2[memory]
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-field-needs-size-guard") {
		t.Fatalf("expected non-exiting guard to be rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

func analyzeOverlaySourceAllowingErrors(t *testing.T, src string) *Result {
	t.Helper()
	l := lexer.New("overlay.elisa", []byte(src))
	tokens := l.Tokenize()
	p := parser.New(tokens)
	file := p.ParseFile("overlay.elisa")
	return AnalyzeWithOptions(file, AnalyzeOptions{OverlayLayouts: []*easm.Layout{orbisProcParamLayout()}})
}


// elf64SymTableLayout is the docs/108 example: a 24-byte-stride table of Elf64_Sym entries. `value`
// is the u64 at offset 8 of each entry; the indexed accessor reads entry i at base + i*24 + 8.
func elf64SymTableLayout() *easm.Layout {
	return &easm.Layout{
		Name:   "Elf64Sym",
		Stride: 24,
		Fields: []easm.LayoutField{
			{Offset: 0, Name: "name", Type: "u32", Width: 4},
			{Offset: 8, Name: "value", Type: "u64", Width: 8},
			{Offset: 16, Name: "size", Type: "u64", Width: 8},
		},
	}
}

func analyzeTableSource(t *testing.T, src string, l *easm.Layout) *Result {
	t.Helper()
	lx := lexer.New("table.elisa", []byte(src))
	tokens := lx.Tokenize()
	if errs := lx.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("table.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return AnalyzeWithOptions(file, AnalyzeOptions{OverlayLayouts: []*easm.Layout{l}})
}

// TestGuestOverlayTableReadDesugars is the docs/108 end-to-end test: `table.value[memory, i]` over a
// stride-24 layout lowers to MemoryManager_ReadU64(memory, table + i*24 + 8).
func TestGuestOverlayTableReadDesugars(t *testing.T) {
	result := analyzeTableSource(t, `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def sym_value(table: GuestVAddr[Elf64Sym], memory: uintptr, i: u64) -> u64:
	return table.value[memory, i]
`, elf64SymTableLayout())
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	idx := findOverlayIndex(result)
	if idx == nil {
		t.Fatal("expected an IndexExpr rewritten with AsOverlayCall")
	}
	call := idx.AsOverlayCall
	if fn, ok := call.Func.(*ast.Ident); !ok || fn.Name != "MemoryManager_ReadU64" {
		t.Fatalf("expected lowering to MemoryManager_ReadU64, got %#v", call.Func)
	}
	if mem, ok := call.Args[0].(*ast.Ident); !ok || mem.Name != "memory" {
		t.Fatalf("expected first arg `memory`, got %#v", call.Args[0])
	}
	// Args[1] is `((table.cast[uintptr]) + (i * 24)) + 8`.
	outer, ok := call.Args[1].(*ast.BinaryExpr)
	if !ok || outer.Op != lexer.TOKEN_PLUS {
		t.Fatalf("expected outer `+ offset`, got %#v", call.Args[1])
	}
	if off, ok := outer.Right.(*ast.IntLit); !ok || off.Value != "8" {
		t.Fatalf("expected field offset literal 8, got %#v", outer.Right)
	}
	inner, ok := outer.Left.(*ast.BinaryExpr)
	if !ok || inner.Op != lexer.TOKEN_PLUS {
		t.Fatalf("expected inner `base + i*stride`, got %#v", outer.Left)
	}
	mul, ok := inner.Right.(*ast.BinaryExpr)
	if !ok || mul.Op != lexer.TOKEN_STAR {
		t.Fatalf("expected `i * stride`, got %#v", inner.Right)
	}
	if stride, ok := mul.Right.(*ast.IntLit); !ok || stride.Value != "24" {
		t.Fatalf("expected stride literal 24, got %#v", mul.Right)
	}
}

// TestGuestOverlayTableZeroOffsetField: a field at offset 0 still gets the `+ i*stride` term but no
// trailing `+ 0`.
func TestGuestOverlayTableZeroOffsetField(t *testing.T) {
	result := analyzeTableSource(t, `extern MemoryManager_ReadU32(mem: uintptr, addr: uintptr) -> u32

def sym_name(table: GuestVAddr[Elf64Sym], memory: uintptr, i: u64) -> u32:
	return table.name[memory, i]
`, elf64SymTableLayout())
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	idx := findOverlayIndex(result)
	if idx == nil {
		t.Fatal("expected overlay rewrite")
	}
	// Address is `base + i*stride` with no `+ 0`; outer op is the i*stride add.
	outer, ok := idx.AsOverlayCall.Args[1].(*ast.BinaryExpr)
	if !ok || outer.Op != lexer.TOKEN_PLUS {
		t.Fatalf("expected `base + i*stride`, got %#v", idx.AsOverlayCall.Args[1])
	}
	if mul, ok := outer.Right.(*ast.BinaryExpr); !ok || mul.Op != lexer.TOKEN_STAR {
		t.Fatalf("expected `i * stride` as the right term, got %#v", outer.Right)
	}
}

// TestGuestOverlayTableFieldExceedsStrideRejected: a field whose offset+width runs past the stride
// is rejected — the over-read surface the table overlay guards.
func TestGuestOverlayTableFieldExceedsStrideRejected(t *testing.T) {
	bad := &easm.Layout{Name: "Tiny", Stride: 8, Fields: []easm.LayoutField{{Offset: 4, Name: "wide", Type: "u64", Width: 8}}}
	result := analyzeTableSource(t, `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def load(table: GuestVAddr[Tiny], memory: uintptr, i: u64) -> u64:
	return table.wide[memory, i]
`, bad)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-field-exceeds-stride") {
		t.Fatalf("expected overlay-field-exceeds-stride diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestGuestOverlayTableUnknownFieldRejected: an unknown field over a stride layout is still rejected.
func TestGuestOverlayTableUnknownFieldRejected(t *testing.T) {
	result := analyzeTableSource(t, `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def load(table: GuestVAddr[Elf64Sym], memory: uintptr, i: u64) -> u64:
	return table.nope[memory, i]
`, elf64SymTableLayout())
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-unknown-field") {
		t.Fatalf("expected overlay-unknown-field diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}

// TestTwoOperandIndexWithoutStrideRejected: a two-operand index over a NON-table carrier (or a plain
// value) is a usage error, not silently accepted.
func TestTwoOperandIndexWithoutStrideRejected(t *testing.T) {
	// OrbisProcParam has no stride; a table-form access over it is rejected.
	result := analyzeTableSource(t, `extern MemoryManager_ReadU64(mem: uintptr, addr: uintptr) -> u64

def load(table: GuestVAddr[OrbisProcParam], memory: uintptr, i: u64) -> u64:
	return table.size[memory, i]
`, orbisProcParamLayout())
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "overlay-table-index-invalid") {
		t.Fatalf("expected overlay-table-index-invalid diagnostic, got:\n%s", strings.Join(result.Errors(), "\n"))
	}
}
