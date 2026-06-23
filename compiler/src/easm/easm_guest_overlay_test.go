package easm

import "testing"

// docs/107 increment-1 prototype: a guest-memory field read (`base.field[mem]` desugaring to
// `MemoryManager_ReadU64(mem, base + offset)`) is checked against a declared layout. This mirrors
// the OrbisProcParam / OrbisKernelMemParam chain in the emulator's core/linker.elisa: size@0,
// mem_param@64, with fields beyond the declared size rejected.

func procParamLayout() *Layout {
	return &Layout{
		Name: "OrbisProcParam",
		Fields: []LayoutField{
			{Offset: 0, Name: "size", Type: "u64", Width: 8},
			{Offset: 64, Name: "mem_param", Type: "u64", Width: 8},
		},
	}
}

func TestOverlayAcceptsInBoundsField(t *testing.T) {
	l := procParamLayout()
	issues, acc := CheckGuestOverlayAccess("linker.elisa", 10, l, "mem_param", 8)
	if len(issues) != 0 {
		t.Fatalf("expected in-bounds read to be accepted, got %#v", issues)
	}
	if acc.Offset != 64 || acc.Width != 8 {
		t.Fatalf("resolved access wrong: %#v", acc)
	}
}

func TestOverlayRejectsUnknownField(t *testing.T) {
	l := procParamLayout()
	issues, _ := CheckGuestOverlayAccess("linker.elisa", 11, l, "bogus", 8)
	if !containsIssue(issues, "overlay-unknown-field") {
		t.Fatalf("expected overlay-unknown-field, got %#v", issues)
	}
}

func TestOverlayRejectsOutOfBoundsField(t *testing.T) {
	// Models the increment-2 `layout OrbisKernelMemParam size 16:` case: the struct is declared 16
	// bytes, but `ext2` sits at offset 40 (reaching to 48) — past the struct end. This is exactly
	// the proc_param/mem_param over-read the check must reject. Using the explicit-size form so the
	// out-of-bounds field cannot grow the size basis it is checked against.
	l := &Layout{Name: "OrbisKernelMemParam", Fields: []LayoutField{
		{Offset: 0, Name: "size", Type: "u64", Width: 8},
		{Offset: 40, Name: "ext2", Type: "u64", Width: 8},
	}}
	issues, _ := CheckGuestOverlayAccessSized("linker.elisa", 12, l, 16, "ext2", 8)
	if !containsIssue(issues, "overlay-field-out-of-bounds") {
		t.Fatalf("expected overlay-field-out-of-bounds, got %#v", issues)
	}
}

func TestOverlayRejectsWidthMismatch(t *testing.T) {
	l := procParamLayout()
	// Read the u64 size field with a 4-byte (ReadU32) accessor.
	issues, _ := CheckGuestOverlayAccess("linker.elisa", 13, l, "size", 4)
	if !containsIssue(issues, "overlay-field-width-mismatch") {
		t.Fatalf("expected overlay-field-width-mismatch, got %#v", issues)
	}
}

// docs/107 increment 2: a `layout Name size N:` header carries an EXPLICIT declared byte size. The
// declared size is authoritative for bounds — an access whose end (offset+width) exceeds it is
// rejected as overlay-field-out-of-bounds, and the out-of-bounds field cannot enlarge the size basis
// it is checked against. A layout WITHOUT `size` falls back to field-derived bounds (LayoutSize),
// so existing size-less layouts are unaffected.

// fooSize16 models `layout Foo size 16:` with a tail field at offset 12 width 8 (reaching 20 > 16).
func fooSize16Layout() *Layout {
	return &Layout{
		Name: "Foo",
		Size: 16,
		Fields: []LayoutField{
			{Offset: 0, Name: "head", Type: "u64", Width: 8},
			{Offset: 12, Name: "tail", Type: "u64", Width: 8},
		},
	}
}

// The declared size parses off the header into Layout.Size; an in-bounds field is accepted.
func TestOverlayDeclaredSizeParsesAndAccepts(t *testing.T) {
	module, issues := Parse("lay.easm", `module lay
target x86_64
layout Foo size 16:
    0 head: u64
`)
	if len(issues) != 0 {
		t.Fatalf("unexpected issues parsing sized layout: %#v", issues)
	}
	if len(module.Layouts) != 1 || module.Layouts[0].Size != 16 {
		t.Fatalf("expected layout Foo with Size 16, got %#v", module.Layouts)
	}
	acc := CheckGuestOverlayAccessReady(t, fooSize16Layout(), "head", 8)
	if acc.Offset != 0 || acc.Width != 8 {
		t.Fatalf("in-bounds head access resolved wrong: %#v", acc)
	}
}

// A field access at offset 12 width 8 (-> 20 > 16) is rejected against the declared size, even
// though that very field is the one reaching past the end.
func TestOverlayDeclaredSizeRejectsOutOfBounds(t *testing.T) {
	l := fooSize16Layout()
	issues, _ := CheckGuestOverlayAccess("lay.elisa", 7, l, "tail", 8)
	if !containsIssue(issues, "overlay-field-out-of-bounds") {
		t.Fatalf("expected overlay-field-out-of-bounds against declared size 16, got %#v", issues)
	}
}

// Regression: the SAME layout WITHOUT an explicit size falls back to field-derived bounds, so the
// offset-12 width-8 field (which now grows the inferred size to 20) is in bounds and accepted —
// existing size-less layouts are unaffected by increment 2.
func TestOverlaySizelessFallsBackToFieldDerived(t *testing.T) {
	l := fooSize16Layout()
	l.Size = 0 // drop the explicit size; bounds now come from the fields (LayoutSize).
	issues, acc := CheckGuestOverlayAccess("lay.elisa", 8, l, "tail", 8)
	if len(issues) != 0 {
		t.Fatalf("size-less layout should accept field-derived in-bounds access, got %#v", issues)
	}
	if acc.Offset != 12 || acc.Width != 8 {
		t.Fatalf("field-derived access resolved wrong: %#v", acc)
	}
}

// CheckGuestOverlayAccessReady asserts a clean resolution and returns the access.
func CheckGuestOverlayAccessReady(t *testing.T, l *Layout, field string, w int) GuestOverlayAccess {
	t.Helper()
	issues, acc := CheckGuestOverlayAccess("lay.elisa", 1, l, field, w)
	if len(issues) != 0 {
		t.Fatalf("expected clean access of %s, got %#v", field, issues)
	}
	return acc
}

// --- docs/107 increment 3: front-end accessor desugar -----------------------

// An in-bounds named field access desugars to the right-width read, byte-identical to the
// hand-written MemoryManager_ReadU64 (docs/107 §(d)/(e)).
func TestOverlayDesugarReadLowersToRightWidth(t *testing.T) {
	l := procParamLayout()
	issues, d := DesugarGuestOverlayAccessor("linker.elisa", 20, l, LayoutSize(l), "proc_param.mem_param[memory]")
	if len(issues) != 0 {
		t.Fatalf("expected accessor to desugar, got %#v", issues)
	}
	if d.Access.Offset != 64 || d.Access.Width != 8 {
		t.Fatalf("resolved access wrong: %#v", d.Access)
	}
	want := "MemoryManager_ReadU64(memory, proc_param + 64)"
	if d.Call != want {
		t.Fatalf("desugar mismatch:\n got %q\nwant %q", d.Call, want)
	}
}

// A u32 field lowers to ReadU32 (width-driven function selection).
func TestOverlayDesugarReadU32(t *testing.T) {
	l := &Layout{Name: "L", Fields: []LayoutField{{Offset: 8, Name: "flags", Type: "u32", Width: 4}}}
	issues, d := DesugarGuestOverlayAccessor("x.elisa", 1, l, 16, "p.flags[m]")
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
	if d.Call != "MemoryManager_ReadU32(m, p + 8)" {
		t.Fatalf("got %q", d.Call)
	}
}

// The write counterpart desugars to MemoryManager_WriteU64 with the value passed through verbatim.
func TestOverlayDesugarWrite(t *testing.T) {
	l := procParamLayout()
	issues, d := DesugarGuestOverlayAccessor("linker.elisa", 21, l, LayoutSize(l), "proc_param.mem_param[memory] = new_val")
	if len(issues) != 0 {
		t.Fatalf("expected write accessor to desugar, got %#v", issues)
	}
	if !d.IsWrite {
		t.Fatalf("expected IsWrite=true: %#v", d)
	}
	want := "MemoryManager_WriteU64(memory, proc_param + 64, new_val)"
	if d.Call != want {
		t.Fatalf("desugar mismatch:\n got %q\nwant %q", d.Call, want)
	}
}

// An unknown field is rejected (overlay-unknown-field), not silently lowered.
func TestOverlayDesugarRejectsUnknownField(t *testing.T) {
	l := procParamLayout()
	issues, _ := DesugarGuestOverlayAccessor("linker.elisa", 22, l, LayoutSize(l), "proc_param.bogus[memory]")
	if !containsIssue(issues, "overlay-unknown-field") {
		t.Fatalf("expected overlay-unknown-field, got %#v", issues)
	}
}

// A non-standard field width (no ReadU<N> form) is rejected as a width mismatch.
func TestOverlayDesugarRejectsWidthMismatch(t *testing.T) {
	l := &Layout{Name: "L", Fields: []LayoutField{{Offset: 0, Name: "odd", Type: "u24", Width: 3}}}
	issues, _ := DesugarGuestOverlayAccessor("x.elisa", 2, l, 8, "p.odd[m]")
	if !containsIssue(issues, "overlay-field-width-mismatch") {
		t.Fatalf("expected overlay-field-width-mismatch, got %#v", issues)
	}
}

// A malformed accessor (not the base.field[mem] shape) is rejected, not crash.
func TestOverlayDesugarRejectsBadAccessor(t *testing.T) {
	l := procParamLayout()
	issues, _ := DesugarGuestOverlayAccessor("x.elisa", 3, l, LayoutSize(l), "proc_param + 64")
	if !containsIssue(issues, "overlay-bad-accessor") {
		t.Fatalf("expected overlay-bad-accessor, got %#v", issues)
	}
}
