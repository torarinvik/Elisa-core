//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestCStrRuntimeBridgeOnlyWeakensToRawByteReference(t *testing.T) {
	cstr := &CStrType{Shape: &WildcardShape{}, SurfaceName: "cstr"}
	rawBytes := &RefType{Elem: &BuiltinType{Name: "u8"}}

	if AssignableTo(cstr, rawBytes) {
		t.Fatal("raw u8 reference must not become cstr without proving NUL termination")
	}
	if !AssignableTo(rawBytes, cstr) {
		t.Fatal("valid cstr should remain usable as a read-only u8 reference")
	}
	if patternRuntimeCompatible(cstr, rawBytes) {
		t.Fatal("generic cstr parameter matching must reject raw u8 references")
	}
	if !patternRuntimeCompatible(rawBytes, cstr) {
		t.Fatal("generic u8-reference parameter matching should accept a cstr")
	}
}

func TestRawReferenceCannotBePassedAsCStrOrScannedBySView(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "raw_reference_is_not_cstr.elisa", `extern consume_cstr(value: cstr) -> i64
extern sview(value: cstr?, start: i64, end: i64) -> sview

def pass_raw_reference(value: u8&) -> i64:
    return consume_cstr(value)

def scan_raw_reference(value: u8&) -> sview:
    return sview(value, 0, -1)
`)
	errors := strings.Join(result.Errors(), "\n")
	if !strings.Contains(errors, `expects cstr`) {
		t.Fatalf("raw byte references must not be promoted to the NUL-terminated cstr invariant, got:\n%s", errors)
	}
	if !strings.Contains(errors, `expects cstr?`) {
		t.Fatalf("strlen-based sview construction must reject a raw unterminated byte reference, got:\n%s", errors)
	}
}
