//go:build cgo

package semantic

import "testing"

func TestSMTPointerCallBindsIndexedSViewBytes(t *testing.T) {
	result := analyzeContractStrict(t, "indexed_sview_call.elisa", `
def first_byte(view: sview) -> i64:
    requires view.len > 0
    ensure result == view[0].i64()
    return view[0].i64()

def caller(facts: darray[sview], index: usize) -> i64:
    requires index < facts.count
    requires facts[index].len > 0
    ensure result == facts[index][0].i64()
    return first_byte(facts[index])
`)
	if failures := ensureFailuresFor(result.Errors(), "caller"); failures != 0 {
		t.Fatalf("indexed SView call lost its byte projection (%d failures):\n%s", failures, allDiagnostics(result))
	}
	if failures := preconditionFailures(result.Errors()); failures != 0 {
		t.Fatalf("indexed SView call lost its length projection (%d failures):\n%s", failures, allDiagnostics(result))
	}
}
