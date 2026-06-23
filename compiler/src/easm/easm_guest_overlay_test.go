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
