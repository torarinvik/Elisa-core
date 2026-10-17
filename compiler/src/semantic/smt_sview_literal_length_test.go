//go:build cgo

package semantic

import (
	"fmt"
	"testing"
)

func TestSMTViewLiteralArgumentLength(t *testing.T) {
	for _, tc := range []struct {
		literal string
		length  int
	}{
		{`""`, 0}, {`"abc"`, 3}, {`"é😺"`, 6}, {`"a\0b"`, 3},
	} {
		t.Run(tc.literal, func(t *testing.T) {
			source := fmt.Sprintf(`def length(view: sview) -> usize:
    ensure result == view.len
    return view.len

def literal_length() -> usize:
    ensure result == %d
    return length(%s)
`, tc.length, tc.literal)
			result := analyzeContractStrict(t, "view_literal_length.elisa", source)
			if len(result.Errors()) != 0 {
				t.Fatal(result.Errors())
			}
		})
	}
}

func TestSMTViewLiteralArgumentLengthRejectsWrongResult(t *testing.T) {
	result := analyzeContractStrict(t, "wrong_literal_length.elisa", `def length(view: sview) -> usize:
    ensure result == view.len
    return view.len

def wrong() -> usize:
    ensure result == 1
    return length("abc")
`)
	if ensureFailuresFor(result.Errors(), "wrong") == 0 {
		t.Fatalf("wrong literal length proved: %v", result.Errors())
	}
}
