//go:build cgo

package backend

import (
	"regexp"
	"strings"
	"testing"
)

// An explicit `.cast[<integer>]` on a pointer/reference must reinterpret the
// ADDRESS (ptrtoint), not dereference the pointee. Regression for a footgun where
// `name.cast[usize]` on an `i8&?` read the first byte at the address (segfaulting
// the avplayer AddSourceEx tests) instead of taking the address; only `.uintptr()`
// happened to work. The value-conversion form `off.u64()` must still deref.
func TestGenerateLLVMIRExplicitIntegerCastReinterpretsPointerAddress(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "explicit_ptr_int_cast.elisa", `
def addr_of_string(s: i8&?) -> usize:
	return s.cast[usize]

def value_of_ref(off: mutable usize&) -> u64:
	return off.u64()
`)

	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel0)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}

	// Explicit `.cast[usize]` on a pointer reinterprets the address (ptrtoint),
	// and must NOT load the pointee.
	castBody := funcBody(t, output, "addr_of_string")
	if !strings.Contains(castBody, "ptrtoint") {
		t.Fatalf("expected addr_of_string to ptrtoint the pointer (address), got:\n%s", castBody)
	}
	if strings.Contains(castBody, "load i8") {
		t.Fatalf("addr_of_string must not deref the pointee for an explicit .cast[usize], got:\n%s", castBody)
	}

	// The value-conversion form `off.u64()` must still dereference (load), not ptrtoint.
	valBody := funcBody(t, output, "value_of_ref")
	if !strings.Contains(valBody, "load i64") {
		t.Fatalf("expected value_of_ref to load the pointee value, got:\n%s", valBody)
	}
	if strings.Contains(valBody, "ptrtoint") {
		t.Fatalf("value_of_ref (off.u64()) must not ptrtoint, got:\n%s", valBody)
	}
}

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
	return (&x).cast[u32&].uintptr()
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
