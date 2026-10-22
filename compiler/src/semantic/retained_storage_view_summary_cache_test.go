package semantic

import (
	"strings"
	"testing"
)

const retainedStorageViewSummaryBefore = `
struct P:
    storage: darray[u8]
    other: darray[u8]
    pos: i64

def touch(p: mutable P&) -> void:
    p.other.push(1.u8())

def run(p: mutable P&) -> i64:
    v: sview = p.storage.as_sview()
    touch(p)
    return v.len.i64()
`

const retainedStorageViewSummaryAfter = `
struct P:
    storage: darray[u8]
    other: darray[u8]
    pos: i64

def touch(p: mutable P&) -> void:
    p.storage.push(1.u8())

def run(p: mutable P&) -> i64:
    v: sview = p.storage.as_sview()
    touch(p)
    marker: i64 = 0
    return v.len.i64()
`

func TestRetainedUpdateInvalidatesStorageViewCalleeWriteSummaries(t *testing.T) {
	before, retained := AnalyzeRetained(
		retainedParse(t, retainedStorageViewSummaryBefore),
		retainedParse(t, retainedStorageViewSummaryBefore),
		[]byte(retainedStorageViewSummaryBefore),
		AnalyzeOptions{},
	)
	if retained == nil {
		t.Fatal("analysis was not retained")
	}
	if got := retainedDiagnostics(before); strings.Contains(got, `"v" cannot be used`) {
		t.Fatalf("fixture's unwritten field should preserve its view before the update:\n%s", got)
	}
	updated, err := retained.Update(
		retainedParse(t, retainedStorageViewSummaryAfter),
		retainedParse(t, retainedStorageViewSummaryAfter),
		[]byte(retainedStorageViewSummaryAfter),
	)
	if err != nil {
		t.Fatalf("body-only edit fell back: %v", err)
	}
	fresh := AnalyzeWithOptions(retainedParse(t, retainedStorageViewSummaryAfter), AnalyzeOptions{})
	want := retainedDiagnostics(fresh)
	if !strings.Contains(want, `"v" cannot be used`) {
		t.Fatalf("fixture's changed callee should invalidate the field view:\n%s", want)
	}
	if got := retainedDiagnostics(updated); got != want {
		t.Fatalf("retained update reused a stale storage-view summary\nretained:\n%s\nfresh:\n%s", got, want)
	}
}
