package semantic

import (
	"strings"
	"testing"
)

func TestSMTQuantifiedScalarCallCongruence(t *testing.T) {
	for _, tc := range []struct{ name, contract string }{
		{"integer", "forall candidate in 0..<256: candidate == index implies rotate(candidate) == rotate(other)"},
		{"boolean", "forall candidate in 0..<256: candidate == index implies is_one(candidate) == is_one(other)"},
		{"byte-range", "forall candidate in 0..<256: rotate(candidate) >= 0 and rotate(candidate) <= 255"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeContractStrict(t, "scalar_call_uf.elisa", `def rotate(index: usize) -> u8:
    return ((index + 1) % 256).u8()

def is_one(index: usize) -> bool:
    return index == 1

def valid(index: usize, other: usize) -> i64:
    requires index == other
    ensure `+tc.contract+`
    return 0
`)
			if len(result.Errors()) != 0 {
				t.Fatalf("scalar call congruence declined:\n%s", allDiagnostics(result))
			}
		})
	}
}

func TestSMTQuantifiedViewCallPrefix(t *testing.T) {
	result := analyzeContractStrict(t, "view_call_prefix.elisa", `def byte_at(text: sview, index: usize) -> u8:
    return text[index]

def prefix(text: sview) -> usize:
    requires text.len >= 0
    ensure result <= text.len
    ensure forall prior in 0..<result: byte_at(text, prior) == 0
    index: mutable usize = 0
    while index < text.len:
        decreases text.len - index
        invariant index <= text.len
        invariant forall prior in 0..<index: byte_at(text, prior) == 0
        return index if byte_at(text, index) != 0
        index <- index + 1
    return index
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("view-call prefix invariant declined:\n%s", allDiagnostics(result))
	}
}

func TestSMTViewCallUFDoesNotFreezeAliasedBytes(t *testing.T) {
	result := analyzeContractStrict(t, "view_call_alias.elisa", `def byte_at(text: sview, index: usize) -> u8:
    return text[index]

def wrong(text: sview, storage: mutable darray[u8]&) -> u8:
    requires storage.count > 0 and text.len > 0
    ensure result == byte_at(text, 0)
    before: u8 = byte_at(text, 0)
    storage[0] <- 17
    return before
`)
	if !strings.Contains(allDiagnostics(result), "could not be proven") {
		t.Fatalf("aliased view bytes were frozen by call congruence:\n%s", allDiagnostics(result))
	}
}
