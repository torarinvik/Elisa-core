package easm

import "fmt"

// Typed guest-memory overlay (docs/107). This is the ordinary-Elisa-code counterpart to the EASM
// `N(%base)` field checking of docs/104: it lets a `base + offset` guest-memory read
// (`MemoryManager_ReadU64(mem, proc_param + 64)`) be expressed as a checked, named field access
// resolved against a declared `layout`. It reuses the existing Layout/LayoutField model verbatim;
// the only new datum is a layout's total Size, from which bounds are derived.
//
// This file is a self-contained PROTOTYPE (docs/107 increment 1). It is additive and is NOT wired
// into the main compile path — it exists so the team can adopt the design. The success path of an
// accessor desugars to exactly today's ReadU64 call (ABI-identical); only out-of-bounds / unknown /
// width-mismatched accesses are rejected.

// LayoutSize returns a layout's declared byte size. When a layout carries no explicit `size`
// (Layout has no Size field today), the size is computed as max(field.Offset + field.Width) over
// declared fields — sound for offset checks. A field with unknown width (Width == 0) contributes
// only its offset. This is the default until the `layout Name size N:` parse lands (increment 2).
func LayoutSize(l *Layout) int64 {
	var max int64
	for _, f := range l.Fields {
		end := f.Offset + int64(f.Width)
		if end > max {
			max = end
		}
	}
	return max
}

// GuestOverlayAccess is a structured, resolved guest-memory field access: the desugared form of
// `base.Field[mem]` against a `GuestVAddr[Layout]` carrier. Offset and Width are filled by
// resolution and are exactly what the emitted ReadU<Width*8>(mem, base+Offset) call uses.
type GuestOverlayAccess struct {
	Layout  string
	Field   string
	Offset  int64
	Width   int
	AccessW int // bytes the accessor reads (e.g. ReadU64 -> 8); must equal the field width
}

// CheckGuestOverlayAccess resolves a named field access against a declared layout, deriving the
// layout's byte size from its fields (LayoutSize). See CheckGuestOverlayAccessSized for the
// explicit-size form (increment-2 `layout Name size N:`), which this delegates to.
func CheckGuestOverlayAccess(path string, line int, l *Layout, fieldName string, accessWidth int) ([]Issue, GuestOverlayAccess) {
	return CheckGuestOverlayAccessSized(path, line, l, LayoutSize(l), fieldName, accessWidth)
}

// CheckGuestOverlayAccessSized resolves a named field access against a declared layout of explicit
// byte size `size` and returns the resolved access, or a non-empty Issue slice describing why it is
// rejected. The checks, in order:
//
//	overlay-unknown-field          field name resolves to no declared field
//	overlay-field-out-of-bounds    field end (offset+width) exceeds the declared size
//	overlay-field-width-mismatch   accessor width (ReadU<N>) differs from the field's declared width
//
// `accessWidth` is the byte width the call site reads (8 for ReadU64, 4 for ReadU32, ...). The
// success path returns ([], access) and is the input to an ABI-identical ReadU<N> desugar.
func CheckGuestOverlayAccessSized(path string, line int, l *Layout, size int64, fieldName string, accessWidth int) ([]Issue, GuestOverlayAccess) {
	var field *LayoutField
	for i := range l.Fields {
		if l.Fields[i].Name == fieldName {
			field = &l.Fields[i]
			break
		}
	}
	if field == nil {
		return []Issue{{
			Severity: "error", Code: "overlay-unknown-field", File: path, Line: line,
			Message: fmt.Sprintf("field %q is not declared in layout %s; add the field or correct the access", fieldName, l.Name),
		}}, GuestOverlayAccess{}
	}

	if field.Width != 0 && field.Offset+int64(field.Width) > size {
		return []Issue{{
			Severity: "error", Code: "overlay-field-out-of-bounds", File: path, Line: line,
			Message: fmt.Sprintf("field %s.%s ends at offset %d but layout %s is only %d bytes; the read would run past the struct", l.Name, field.Name, field.Offset+int64(field.Width), l.Name, size),
		}}, GuestOverlayAccess{}
	}

	if field.Width != 0 && accessWidth != field.Width {
		return []Issue{{
			Severity: "error", Code: "overlay-field-width-mismatch", File: path, Line: line,
			Message: fmt.Sprintf("%d-byte read of field %s.%s declared %s (%d bytes); widths must match", accessWidth, l.Name, field.Name, field.Type, field.Width),
		}}, GuestOverlayAccess{}
	}

	return nil, GuestOverlayAccess{
		Layout:  l.Name,
		Field:   field.Name,
		Offset:  field.Offset,
		Width:   field.Width,
		AccessW: accessWidth,
	}
}
