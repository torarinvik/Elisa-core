package semantic

import (
	"strings"
	"testing"
)

// A callee handed `buffer: mutable darray[u8]&` next to a view can only copy the view's bytes
// into it: a scalar-only holder never carries another value's backing dependency, so relocating
// that backing later does not make `buffer` stale. A holder that CAN keep the view
// (`names: mutable darray[sview]&`) still does.
func TestStorageViewScalarOnlyHolderCarriesNoDependency(t *testing.T) {
	src := `def push_text(buffer: mutable darray[u8]&, text: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<text.len |buffer|:
            buffer.push(text[i])

def keep_name(names: mutable darray[sview]&, text: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(text)

def name_into(storage: mutable darray[u8]&, suffix: sview) -> sview:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<suffix.len |storage|:
            storage.push(suffix[i])
        return storage.as_sview()

def bytes_ok(buffer: mutable darray[u8]&) -> void:
    can Memory.Allocate, Abort.Panic:
        storage: mutable darray[u8] = []
        helper: sview = name_into(&storage, "a")
        push_text(buffer, helper)
        other: sview = name_into(&storage, "b")
        push_text(buffer, other)

def names_kept(names: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        storage: mutable darray[u8] = []
        helper: sview = name_into(&storage, "a")
        keep_name(names, helper)
        other: sview = name_into(&storage, "b")
        keep_name(names, other)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "storage_view_scalar_holder.elisa", src, AnalyzeOptions{})
	var stale []string
	for _, e := range result.Errors() {
		if strings.Contains(e, "storage dependency facts were invalidated") {
			stale = append(stale, e)
		}
	}
	joined := strings.Join(stale, "\n")
	if strings.Contains(joined, `view "buffer"`) {
		t.Errorf("a byte buffer cannot hold a view; it must not go stale:\n%s", joined)
	}
	if !strings.Contains(joined, `view "names"`) {
		t.Errorf("a darray[sview] holder keeps the view and must go stale when its backing relocates, got:\n%s\nall errors:\n%s", joined, strings.Join(result.Errors(), "\n"))
	}
}
