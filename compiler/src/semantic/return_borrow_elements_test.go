package semantic

import (
	"strings"
	"testing"
)

const elementsBorrowRegionSource = `struct Scratch[@r]:
    text: mutable sview @r
struct Box[@r]:
    text: mutable sview @r
def retain[@r](value: Scratch[r]& @r, out: mutable Box[r]& @r) -> void:
    out.text <- value.text
def caller[@r](out: mutable Box[r]& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        slots: mutable darray[Scratch[r]] @r = [Scratch[r]{text: "hello"}]
        retain(&slots[0], out)
def main() -> i64:
    return 0
`

func TestElementsBorrowCopiesCallerRegionValue(t *testing.T) {
	src := elementsBorrowRegionSource
	textSource := string(src)
	start := strings.Index(textSource, "def retain[")
	end := strings.Index(textSource, "def caller[")
	callerFirst := textSource[:start] + textSource[end:] + "\n" + textSource[start:end]
	for _, text := range []string{callerFirst, string(src), strings.Replace(string(src), "out: mutable Box[r]& @r) -> void:\n    out.text", "out: mutable Box[r]& @r, flag: bool = true) -> void:\n    out.text", 1)} {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "elements_copy_positive.elisa", text, AnalyzeOptions{})
		if got := strings.Join(result.Errors(), "\n"); got != "" {
			t.Fatal(got)
		}
	}
}
func TestElementsBorrowStillRejectsLocalViewContents(t *testing.T) {
	const src = `struct Scratch:
    text: view[u8]
struct Box:
    text: mutable view[u8]
def retain(value: Scratch&, out: mutable Box&) -> void:
    out.text <- value.text
def caller(out: mutable Box&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: u8[4] = [65, 66, 67, 68]
        slots: mutable darray[Scratch] = [Scratch{text: buf[0:2]}]
        retain(&slots[0], out)
def main() -> i64:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "elements_local_view.elisa", src, AnalyzeOptions{})
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, "call may store a reference to function-local storage") {
		t.Fatalf("local view contents must remain rejected: %s", got)
	}
}
func TestElementsBorrowStillRejectsHeaderInterior(t *testing.T) {
	const src = `struct Holder:
    buf: mutable u8[3]
def fill(h: Holder&, out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        out.push(&h.buf[0])
def caller(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        h: mutable Holder = Holder{buf: [1, 2, 3]}
        fill(&h, out)
def main() -> i64:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "elements_header_interior.elisa", src, AnalyzeOptions{})
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, "call may store a reference to function-local storage") {
		t.Fatalf("interior pointer retention must remain rejected: %s", got)
	}
}
func TestElementsBorrowUnknownCalleeRemainsConservative(t *testing.T) {
	src := elementsBorrowRegionSource
	text := strings.Replace(string(src), "def retain[@r]", "extern retain[@r]", 1)
	text = strings.Replace(text, "    out.text <- value.text\n", "", 1)
	text = strings.Replace(text, "-> void:\ndef caller", "-> void\ndef caller", 1)
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "elements_unknown.elisa", text, AnalyzeOptions{})
	got := strings.Join(result.Errors(), "\n")
	if !strings.Contains(got, "call may store a reference to function-local storage") {
		t.Fatalf("unknown callee must remain conservative: %s", got)
	}
}
