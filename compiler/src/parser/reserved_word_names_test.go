package parser

import (
	"strings"
	"testing"
)

// A reserved keyword used as a name gets one clear diagnostic per site and no cascade
// ("expected IDENT, got to", "unexpected token : in expression", ...).
func TestReservedWordAsNameDiagnostic(t *testing.T) {
	for _, word := range []string{"to", "stack", "heap", "tail", "error", "packed"} {
		src := strings.ReplaceAll(`def KW(n: i64) -> i64:
    return n
def f(KW: i64) -> i64:
    return KW
struct S:
    KW: i64
def main() -> i64:
    KW: i64 = 3
    KW = 4
    for KW in xs:
        pass
    return KW
`, "KW", word)
		_, errs := parseSourceFile(t, src)
		if len(errs) == 0 {
			t.Fatalf("%s: expected diagnostics", word)
		}
		want := "`" + word + "` is a reserved keyword and cannot be used as a name"
		for _, e := range errs {
			if !strings.Contains(e, want) {
				t.Fatalf("%s: unexpected cascade diagnostic %q in %v", word, e, errs)
			}
		}
		if len(errs) != 8 {
			t.Fatalf("%s: expected 8 site diagnostics, got %d: %v", word, len(errs), errs)
		}
	}
}
