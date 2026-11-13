package semantic

import (
	"strings"
	"testing"
)

// The implicit single-target arena adopts the caller's storage; an independent
// writable target prevents adoption and must preserve the escaping-view error.
func TestHandlerCaptureCalleeViewLifetime(t *testing.T) {
	const independent = `struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def fill(parser: lmut P, items: mutable darray[Prm]&, bytes: mutable darray[u8]&) -> bool:
    can Memory.Allocate, Abort.Panic:
        bytes.push(65)
        items.push(Prm{name: bytes.as_sview(), has_default: false})
        parser.position <- parser.position + 1
        return true

def handler(parser: lmut P, other: mutable P&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = []
        captures: mutable darray[Prm] = []
        rebind ignored: bool, parser = parser.fill(captures, buf)
        for capture in captures |parser|:
            parser.params <- parser.params.push(capture)

def main() -> i32:
    return 0
`
	const explicit = `struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def fill(parser: lmut P, items: mutable darray[Prm]&, bytes: mutable darray[u8]&) -> bool:
    can Memory.Allocate, Abort.Panic:
        bytes.push(65)
        items.push(Prm{name: bytes.as_sview(), has_default: false})
        parser.position <- parser.position + 1
        return true

def handler[@r](parser: mutable P& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] @r = []
        captures: mutable darray[Prm] @r = []
        rebind ignored: bool, parser = parser.fill(captures, buf)
        for capture in captures |parser|:
            parser.params <- parser.params.push(capture)

def main() -> i32:
    return 0
`
	adopted := strings.Replace(independent, "def handler(parser: lmut P, other: mutable P&)", "def handler(parser: lmut P)", 1)
	for _, tc := range []struct {
		name, source  string
		reject, adopt bool
	}{
		{"independent", independent, true, false},
		{"adopted", adopted, false, true},
		{"explicit", explicit, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, result := parseAndAnalyzeOptimizationFactsTest(t, tc.name+".elisa", tc.source)
			errs := strings.Join(result.Errors(), "\n")
			if tc.reject && !strings.Contains(errs, "stored into longer-lived region") {
				t.Fatalf("expected region escape diagnostic, got: %s", errs)
			}
			if !tc.reject && errs != "" {
				t.Fatalf("safe lifetime must be accepted, got: %s", errs)
			}
			fn := testFuncDeclByName(t, file, "handler")
			if (fn.AmbientGrownContainerRegion != "") != tc.adopt {
				t.Fatalf("unexpected ambient arena adoption: %q", fn.AmbientGrownContainerRegion)
			}
		})
	}
}
