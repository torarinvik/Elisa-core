package semantic

import (
	"strings"
	"testing"
)

// P2: a protocol associated type bound to a REFINED type in the impl
// (`type Field = u32 is InRange[0,127]`) lets generic code that calls the protocol method inherit the
// proof. In `dispatch[D: FieldDecoder]`, `table[D.decode(d, w)]` with `table: array[Handler, 128]` is
// proven in bounds because the only impl guarantees the result lies in [0,127]. No runtime check.
func TestRefinedAssociatedTypeProvesGenericIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "assoc_refine.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Handler:
    id: i64
struct Sgpr:
    shift: u32

protocol FieldDecoder:
    type Word
    type Field
    def decode(self: Self, w: Word) -> Field

impl FieldDecoder for Sgpr:
    type Word = u32
    type Field = u32 is InRange[0, 127]
    def decode(self: Sgpr, w: u32) -> u32 is InRange[0, 127]:
        return (w >> self.shift) & 0x7f

def dispatch[D: FieldDecoder](d: D, w: D.Word, table: array[Handler, 128]&) -> Handler:
    return table[D.decode(d, w)]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("a refined associated-type result [0,127] into array[Handler,128] must prove in bounds, got:\n%s", all)
	}
	// Hard elision evidence: the backend skips the bounds guard for every index in IndexBoundsProven.
	proven := 0
	for _, ok := range result.IndexBoundsProven {
		if ok {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("expected the generic table[D.decode(..)] index to be recorded in IndexBoundsProven (codegen elides its guard)")
	}
}

// SOUNDNESS counterpart: the non-refined impl must leave NO index in IndexBoundsProven.
func TestNonRefinedAssociatedTypeNoProvenIndex(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assoc_norefine2.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Handler:
    id: i64
struct Sgpr:
    shift: u32

protocol FieldDecoder:
    type Word
    type Field
    def decode(self: Self, w: Word) -> Field

impl FieldDecoder for Sgpr:
    type Word = u32
    type Field = u32
    def decode(self: Sgpr, w: u32) -> u32:
        return (w >> self.shift)

def dispatch[D: FieldDecoder](d: D, w: D.Word, table: array[Handler, 128]&) -> Handler:
    return table[D.decode(d, w)]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	for _, ok := range result.IndexBoundsProven {
		if ok {
			t.Fatalf("a non-refined associated type must NOT mark any index as proven (no false elision)")
		}
	}
}

// SOUNDNESS / both-polarity: a NON-refined impl binding (`type Field = u32`) means a generic caller
// can prove nothing about the decode result, so the bounds check must REMAIN.
func TestNonRefinedAssociatedTypeKeepsGenericIndexCheck(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assoc_norefine.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Handler:
    id: i64
struct Sgpr:
    shift: u32

protocol FieldDecoder:
    type Word
    type Field
    def decode(self: Self, w: Word) -> Field

impl FieldDecoder for Sgpr:
    type Word = u32
    type Field = u32
    def decode(self: Sgpr, w: u32) -> u32:
        return (w >> self.shift)

def dispatch[D: FieldDecoder](d: D, w: D.Word, table: array[Handler, 128]&) -> Handler:
    return table[D.decode(d, w)]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("a non-refined associated type proves nothing; the bounds check must remain, got:\n%s", allDiagnostics(result))
	}
}

// GAP #2: a REFINED associated-type DEFAULT declared on the protocol (`type Field = u32 is
// InRange[0,127]`) is inherited by an impl that omits the binding — the default type AND its
// refinement flow through, so generic code proving on the associated type still elides the check.
func TestRefinedAssociatedTypeDefaultInheritedByImpl(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptions(t, "assoc_default.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Handler:
    id: i64
struct Sgpr:
    shift: u32

protocol FieldDecoder:
    type Word = u32
    type Field = u32 is InRange[0, 127]
    def decode(self: Self, w: Word) -> Field

impl FieldDecoder for Sgpr:
    def decode(self: Sgpr, w: u32) -> u32 is InRange[0, 127]:
        return (w >> self.shift) & 0x7f

def dispatch[D: FieldDecoder](d: D, w: D.Word, table: array[Handler, 128]&) -> Handler:
    return table[D.decode(d, w)]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
	if all := strings.Join(result.Warnings(), "\n"); strings.Contains(all, "unchecked index requires") {
		t.Fatalf("an inherited refined associated-type default [0,127] into array[Handler,128] must prove in bounds, got:\n%s", all)
	}
	proven := 0
	for _, ok := range result.IndexBoundsProven {
		if ok {
			proven++
		}
	}
	if proven == 0 {
		t.Fatalf("expected the generic table[D.decode(..)] index to be recorded in IndexBoundsProven via the inherited default refinement")
	}
}

// An impl that OVERRIDES an associated-type default must still satisfy the protocol's refinement:
// binding `type Field = u32` (dropping the [0,127] refinement) means a generic caller proves
// nothing, so the bounds check must REMAIN (no regression of the soundness check).
func TestAssociatedTypeDefaultOverrideDropsRefinementKeepsCheck(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assoc_default_override.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Handler:
    id: i64
struct Sgpr:
    shift: u32

protocol FieldDecoder:
    type Word = u32
    type Field = u32 is InRange[0, 127]
    def decode(self: Self, w: Word) -> Field

impl FieldDecoder for Sgpr:
    type Field = u32
    def decode(self: Sgpr, w: u32) -> u32:
        return (w >> self.shift)

def dispatch[D: FieldDecoder](d: D, w: D.Word, table: array[Handler, 128]&) -> Handler:
    return table[D.decode(d, w)]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "unchecked index requires") {
		t.Fatalf("an impl that overrides the default to drop the refinement proves nothing; the bounds check must remain, got:\n%s", allDiagnostics(result))
	}
}

// The impl's decode body return refinement is CHECKED: a body that can produce a value outside
// [0,127] must be rejected (the associated-type refinement is a real contract, not a label).
func TestRefinedAssociatedTypeImplBodyMustProveReturn(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assoc_badbody.elisa", `law InRange(self: u32, lo: u32, hi: u32) = self >= lo and self <= hi
struct Sgpr:
    shift: u32

protocol FieldDecoder:
    type Word
    type Field
    def decode(self: Self, w: Word) -> Field

impl FieldDecoder for Sgpr:
    type Word = u32
    type Field = u32 is InRange[0, 127]
    def decode(self: Sgpr, w: u32) -> u32 is InRange[0, 127]:
        return w & 0x1ff
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	// `w & 0x1ff` lies in [0,511], which does not prove [0,127]; the return refinement must fail.
	if d := allDiagnostics(result); !strings.Contains(d, "InRange") && !strings.Contains(d, "refinement") && !strings.Contains(d, "ensure") {
		t.Fatalf("an impl decode body that can exceed 127 must fail its return refinement, got:\n%s", d)
	}
}
