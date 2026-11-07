package semantic

import (
	"strings"
	"testing"
)

func contractPurityErrors(t *testing.T, source string) string {
	t.Helper()
	file := retainedParse(t, source)
	var text []string
	for _, e := range AnalyzeWithOptions(file, AnalyzeOptions{EnforceUnsafePermissions: true}).Errors() {
		text = append(text, e)
	}
	return strings.Join(text, "\n")
}

func TestContractAllowsGhostReadAndAddressArithmetic(t *testing.T) {
	got := contractPurityErrors(t, `ghost def model(v: sview, i: usize) -> u8:
    return v[i].u8()
def viaGhost(v: sview, i: usize) -> u8:
    ensure result == model(v, i)
    return 0
ghost def modelWithContract(v: sview, i: usize) -> u8:
    ensure result == v[i].u8()
    return v[i].u8()
def address(p: u8&, r: u8&) -> bool:
    ensure result == (r == p + 1.usize())
    return false
`)
	if strings.Contains(got, "must be pure") {
		t.Fatalf("pure clause rejected:\n%s", got)
	}
}

func TestContractStillRejectsDirectUncheckedRead(t *testing.T) {
	got := contractPurityErrors(t, `def direct(v: sview, i: usize) -> u8:
    ensure result == v[i].u8()
    return 0
`)
	if !strings.Contains(got, "must be pure") {
		t.Fatalf("direct unchecked read accepted in contract:\n%s", got)
	}
}

func TestRuntimeUncheckedReadStillNeedsGrant(t *testing.T) {
	got := contractPurityErrors(t, `def body(v: sview, i: usize) -> u8:
    return v[i].u8()
`)
	if !strings.Contains(got, "unchecked index") {
		t.Fatalf("runtime unchecked read lost its grant requirement:\n%s", got)
	}
}
