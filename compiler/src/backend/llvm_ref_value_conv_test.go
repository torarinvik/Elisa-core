//go:build cgo

package backend

import (
	"regexp"
	"strings"
	"testing"
)

// A numeric conversion method called directly on a reference-to-numeric parameter
// (e.g. off.u64() where off: mutable usize&) must dereference to the pointee VALUE,
// not convert the reference's address. Regression for a codegen bug where the
// coercion only dereferenced when the element type exactly matched the target, so a
// cross-numeric conversion (usize& -> u64) fell through to ptrtoint(address).
func TestGenerateLLVMIRDereferencesRefForNumericConversion(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "ref_value_conv.elisa", `
def read_value(off: mutable usize&) -> u64:
	return off.u64()

def take_address(x: mutable u32&) -> uintptr:
	return x.ref[u32&].uintptr()
`)

	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}

	readBody := funcBody(t, output, "read_value")
	if !strings.Contains(readBody, "load i64") {
		t.Fatalf("expected read_value to load the pointee value, got:\n%s", readBody)
	}
	if strings.Contains(readBody, "ptrtoint") {
		t.Fatalf("read_value must not ptrtoint the reference (that returns the address, not the value), got:\n%s", readBody)
	}

	// The address-of idiom someRef.uintptr() must still take the address (ptrtoint).
	addrBody := funcBody(t, output, "take_address")
	if !strings.Contains(addrBody, "ptrtoint") {
		t.Fatalf("expected take_address to ptrtoint the reference (address-of), got:\n%s", addrBody)
	}
}

func funcBody(t *testing.T, ir, name string) string {
	t.Helper()
	re := regexp.MustCompile(`(?s)define[^@]*@` + regexp.QuoteMeta(name) + `\(.*?\n}`)
	m := re.FindString(ir)
	if m == "" {
		t.Fatalf("could not find definition of @%s in IR:\n%s", name, ir)
	}
	return m
}
