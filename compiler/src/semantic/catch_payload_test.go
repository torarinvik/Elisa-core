package semantic

import (
	"strings"
	"testing"
)

// A catch arm may bind a matched variant's payload (`E.Bad2(x):`) and use it, typed by
// the variant's payload fields.
func TestAnalyzeCatchArmBindsPayload(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "catch_payload_bind.elisa", `
error E:
	Bad1
	Bad2(value: u32)

extern f() -> i64 error[E]
extern logu(x: u32) -> void

def g() -> void:
	catch f():
		n: logu(0)
		E.Bad1: logu(1)
		E.Bad2(x): logu(x)
`)
}

// Binding the wrong number of payload values is a compile error.
func TestAnalyzeCatchArmPayloadArityMismatch(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "catch_payload_arity.elisa", `
error E:
	Bad1
	Bad2(value: u32)

extern f() -> i64 error[E]
extern logu(x: u32) -> void

def g() -> void:
	catch f():
		n: logu(0)
		E.Bad1: logu(1)
		E.Bad2(x, y): logu(x)
`)
	if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "binds 2 payload value(s) but the variant carries 1") {
		t.Fatalf("expected payload arity error; got: %s", got)
	}
}
