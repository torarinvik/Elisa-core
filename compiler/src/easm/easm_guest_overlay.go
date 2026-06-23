package easm

import (
	"fmt"
	"regexp"
	"strings"
)

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

// LayoutSize returns a layout's declared byte size. When a layout carries an explicit declared size
// (`layout Name size N:` — increment 2), that size is authoritative and is returned verbatim, so an
// access past it is rejected even if no field reaches that far. When no explicit size is declared
// (l.Size == 0), the size is computed as max(field.Offset + field.Width) over declared fields —
// sound for offset checks, and identical to the pre-increment-2 behavior so existing size-less
// layouts are unaffected. A field with unknown width (Width == 0) contributes only its offset.
func LayoutSize(l *Layout) int64 {
	if l.Size > 0 {
		return l.Size
	}
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

// ---------------------------------------------------------------------------
// docs/107 increment 4 — static size-guard obligation
//
// A field may be tagged `requires size >= N` (LayoutField.RequiresSizeAtLeast), meaning it is only
// present when the struct's runtime `size` field is at least N bytes. This is the declarative form
// of the emulator's hand-written under-size fallback ("if mem_param_size < 48: use defaults"):
// instead of a runtime branch, accessing the field becomes a static OBLIGATION that the access be
// dominated by a guard establishing `size >= N`.
//
// FLOW MODEL. We do NOT walk the program. Mirroring the rest of the overlay checker (which takes
// resolved facts — layout, size, width — as explicit inputs rather than re-deriving them), and the
// known-value/lower-bound style the easm verifier already uses at merges, the caller hands us the
// set of size lower-bounds that provably hold at the access point: each entry is "the struct's
// runtime size is known to be >= K", established by a dominating guard such as
// `if base.size[mem] >= K:`. The front-end (the parallel increment-5 wiring) is responsible for
// turning a dominating guard into a SizeGuardFact; here we only decide whether the known facts
// discharge the field's requirement.
//
// SOUNDNESS. The obligation is discharged iff some known lower-bound K satisfies K >= N: a guard
// proving `size >= K` with K >= N a fortiori proves `size >= N`. A weaker guard (K < N), or no
// guard at all (empty fact set), leaves the requirement undischarged and the access is rejected as
// overlay-field-needs-size-guard. A field with no requirement (RequiresSizeAtLeast == 0) is
// unconditionally present and is accepted regardless of facts.

// SizeGuardFact is a single proven lower-bound on a layout carrier's runtime `size` field, holding
// at an access point because a dominating guard established it (e.g. `if base.size[mem] >= AtLeast:`).
// It is the explicit-input analogue of the easm verifier's known-value facts: the front-end derives
// these from dominators and hands them in, so this checker stays a pure fact-discharge decision.
type SizeGuardFact struct {
	AtLeast int64 // the struct's runtime size is proven to be >= AtLeast
}

// CheckGuestOverlaySizeGuard verifies the static size-guard obligation (docs/107 §(b), increment 4)
// for an overlay access to `fieldName` of layout `l`, given the size-guard facts known to hold at
// the access point. If the field carries no `requires size >= N` tag it is accepted unconditionally
// (regression-safe: size-less fields are unaffected). Otherwise the obligation is discharged iff some
// known fact proves `size >= N`; if none does, it emits overlay-field-needs-size-guard.
//
// An unknown field name is NOT this checker's concern — it is reported by CheckGuestOverlayAccess.
// Here an unknown field simply carries no requirement and is accepted, so the two checks compose
// without double-reporting.
func CheckGuestOverlaySizeGuard(path string, line int, l *Layout, fieldName string, facts []SizeGuardFact) []Issue {
	var field *LayoutField
	for i := range l.Fields {
		if l.Fields[i].Name == fieldName {
			field = &l.Fields[i]
			break
		}
	}
	if field == nil || field.RequiresSizeAtLeast == 0 {
		return nil
	}
	need := field.RequiresSizeAtLeast
	for _, f := range facts {
		if f.AtLeast >= need {
			return nil
		}
	}
	return []Issue{{
		Severity: "error", Code: "overlay-field-needs-size-guard", File: path, Line: line,
		Message: fmt.Sprintf("field %s.%s requires the struct's runtime size >= %d; guard the access with `if base.size[mem] >= %d:` (no dominating size guard proves it here)", l.Name, field.Name, need, need),
	}}
}

// ---------------------------------------------------------------------------
// docs/107 increment 3 — front-end accessor desugar
//
// This is the load-bearing front-end step: parse the overlay accessor surface form, resolve the
// named field against its layout (via CheckGuestOverlayAccessSized), and lower it to the *exact*
// MemoryManager_ReadU<N>/WriteU<N> call the programmer writes by hand today. There is NO codegen
// change on the success path (docs/107 §(e) ABI-neutrality): the desugar output is byte-identical
// to the hand-written read/write.
//
// The accessor surface syntax (docs/107 §(a)) is:
//
//	base.field[mem]            # read  -> MemoryManager_ReadU<N>(mem, base + offset)
//	base.field[mem] = value    # write -> MemoryManager_WriteU<N>(mem, base + offset, value)
//
// where `base` is a `GuestVAddr[L]` carrier, `field` is a declared field of layout L, and `mem`
// names the MemoryManager to read/write through. The access width N is the field's declared width;
// a field whose width does not match a ReadU<N> form is rejected as overlay-field-width-mismatch.
//
// This entry point is additive and opt-in: it is only reached when a caller hands it a parsed
// overlay accessor. The existing compile path and all current tests are unaffected.

// overlayAccessorRE matches the read accessor `base.field[mem]` (whitespace-tolerant) and the
// optional trailing ` = value` write form. base/field/mem are simple identifiers; value (write
// only) is the remaining expression text, passed through verbatim into WriteU<N>.
var overlayAccessorRE = regexp.MustCompile(
	`^\s*([A-Za-z_][A-Za-z0-9_]*)\s*\.\s*([A-Za-z_][A-Za-z0-9_]*)\s*\[\s*([A-Za-z_][A-Za-z0-9_]*)\s*\]\s*(?:=\s*(.+?))?\s*$`,
)

// widthToReadFn / widthToWriteFn map a field's declared byte width to the MemoryManager accessor
// the desugar lowers to. A width with no entry (e.g. an odd field width) is a width mismatch.
var widthToReadFn = map[int]string{1: "MemoryManager_ReadU8", 2: "MemoryManager_ReadU16", 4: "MemoryManager_ReadU32", 8: "MemoryManager_ReadU64"}
var widthToWriteFn = map[int]string{1: "MemoryManager_WriteU8", 2: "MemoryManager_WriteU16", 4: "MemoryManager_WriteU32", 8: "MemoryManager_WriteU64"}

// OverlayDesugar is the lowered, ABI-neutral form of an overlay accessor: the resolved access plus
// the exact MemoryManager call text it desugars to.
type OverlayDesugar struct {
	Access GuestOverlayAccess
	Base   string // carrier identifier
	Mem    string // MemoryManager identifier (the `[mem]` index)
	IsWrite bool
	Value  string // write only: the source expression text
	Call   string // the emitted MemoryManager_ReadU<N>/WriteU<N>(...) call
}

// DesugarGuestOverlayAccessor parses a single overlay accessor expression `src` against a declared
// layout `l` of explicit byte size `size`, and lowers it to the equivalent MemoryManager read/write
// call. On any failure it returns a non-empty Issue slice (overlay-unknown-field /
// overlay-field-width-mismatch / overlay-field-out-of-bounds / overlay-bad-accessor) and a zero
// OverlayDesugar. On success the returned OverlayDesugar.Call is byte-identical to the hand-written
// MemoryManager_Read/Write the programmer would otherwise spell.
//
// `size` is taken as an explicit parameter (NOT a Layout.Size field) so this works against both the
// computed-default size (LayoutSize(l)) and the increment-2 `layout Name size N:` declared size,
// without colliding with the parallel layout-header parser work.
func DesugarGuestOverlayAccessor(path string, line int, l *Layout, size int64, src string) ([]Issue, OverlayDesugar) {
	m := overlayAccessorRE.FindStringSubmatch(src)
	if m == nil {
		return []Issue{{
			Severity: "error", Code: "overlay-bad-accessor", File: path, Line: line,
			Message: fmt.Sprintf("%q is not a guest-overlay accessor; expected `base.field[mem]` or `base.field[mem] = value`", strings.TrimSpace(src)),
		}}, OverlayDesugar{}
	}
	base, fieldName, mem, value := m[1], m[2], m[3], m[4]
	isWrite := value != ""

	// The accessor width is the field's declared width: the whole point of the named accessor is
	// that the programmer does not restate it. Resolve the field first to learn its width, then run
	// it back through the shared checker at that width so the (sound, no-op) width-match check and
	// the bounds check both fire from the one authoritative code path.
	var declWidth int
	for i := range l.Fields {
		if l.Fields[i].Name == fieldName {
			declWidth = l.Fields[i].Width
			break
		}
	}
	// When the field is unknown, declWidth stays 0; CheckGuestOverlayAccessSized then emits
	// overlay-unknown-field. When the field width is non-standard (not 1/2/4/8) we reject as a
	// width mismatch — there is no ReadU<N> of that width to lower to.
	issues, acc := CheckGuestOverlayAccessSized(path, line, l, size, fieldName, declWidth)
	if len(issues) != 0 {
		return issues, OverlayDesugar{}
	}

	readFn, ok := widthToReadFn[acc.Width]
	writeFn := widthToWriteFn[acc.Width]
	if !ok {
		return []Issue{{
			Severity: "error", Code: "overlay-field-width-mismatch", File: path, Line: line,
			Message: fmt.Sprintf("field %s.%s has %d-byte width, which has no MemoryManager_ReadU<N> form (expected 1/2/4/8)", l.Name, fieldName, acc.Width),
		}}, OverlayDesugar{}
	}

	var call string
	if isWrite {
		call = fmt.Sprintf("%s(%s, %s + %d, %s)", writeFn, mem, base, acc.Offset, value)
	} else {
		call = fmt.Sprintf("%s(%s, %s + %d)", readFn, mem, base, acc.Offset)
	}
	return nil, OverlayDesugar{
		Access:  acc,
		Base:    base,
		Mem:     mem,
		IsWrite: isWrite,
		Value:   value,
		Call:    call,
	}
}
