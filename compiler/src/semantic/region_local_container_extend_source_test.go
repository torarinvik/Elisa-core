package semantic

import (
	"strings"
	"testing"
)

// A darray of packed (handle) enum values stores only indices into the enum's tree store, so a
// callee filling a `darray[sview]` cannot hand it views of that darray's own buffer.
func TestPackedHandleDarrayStorageHoldsNoViewableBytes(t *testing.T) {
	if !isNonByteScalarContainerType(&DArrayType{Elem: &EnumType{Name: "Node", Packed: true}}) {
		t.Fatalf("darray[packed enum] must count as a container of non-byte scalars")
	}
}

const extendSourcePrelude = `def push_one(dst: mutable darray[sview]&, v: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        dst.push(v)

`

// `owners.extend(alt)` copies alt's elements by value: owners gains alt's tracked element
// provenance (here: the parameter src), not alt's function-local backing region.
func TestLocalExtendSourceKeepsTrackedElementProvenance(t *testing.T) {
	src := extendSourcePrelude + `def merge(dst: mutable darray[sview]&, src: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for row in 0..<src.count |row, dst, src|:
            owners: mutable darray[sview] = []
            alt: mutable darray[sview] = []
            for v in src |alt|:
                alt.push(v)
            if owners.count == 0:
                owners.extend(alt)
            else:
                a0: sview = alt[0]
                owners.push(a0)
            for owner in owners |dst|:
                push_one(dst, owner)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "extend_source_tracked.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

func expectExtendSourceEscape(t *testing.T, name, body string) {
	t.Helper()
	src := extendSourcePrelude + body + `
def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name, src, AnalyzeOptions{})
	for _, e := range result.Errors() {
		if strings.Contains(e, "into longer-lived region") || strings.Contains(e, "inside a loop may be read") {
			return
		}
	}
	t.Fatalf("expected the element escape to be reported, got: %v", result.Errors())
}

// The extend source's elements view a function-local buffer: copying them still escapes.
func TestLocalExtendSourceStillRejectsLocalViews(t *testing.T) {
	expectExtendSourceEscape(t, "extend_source_local_view.elisa", `def bad(dst: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = []
        buf.push(65)
        owners: mutable darray[sview] = []
        alt: mutable darray[sview] = []
        alt.push(buf.as_sview())
        owners.extend(alt)
        push_one(dst, owners[0])
`)
}

// A loop that grows the source AFTER extending from it: a later iteration copies elements the
// state at the extend has not seen, so the source must not be modelled.
func TestLocalExtendSourceGrownLaterInLoopStaysConservative(t *testing.T) {
	expectExtendSourceEscape(t, "extend_source_loop_carried.elisa", `def bad(dst: mutable darray[sview]&, src: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = []
        buf.push(65)
        owners: mutable darray[sview] = []
        alt: mutable darray[sview] = []
        for row in 0..<src.count |row, owners, alt, buf, src|:
            owners.extend(alt)
            alt.push(buf.as_sview())
            alt.push(src[row])
        for owner in owners |dst|:
            push_one(dst, owner)
`)
}
