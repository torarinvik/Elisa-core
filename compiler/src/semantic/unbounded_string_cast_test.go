package semantic

import (
	"strings"
	"testing"
)

// Casting a darray element pointer to an unbounded `static u8&` string is the
// out-of-bounds-read hazard and must be flagged (as a warning).
func TestUnboundedStringFromDArrayElementIsFlagged(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "unbounded_str_darray.elisa", `struct Blob:
    data: mutable darray[u8]

def name_at(self: Blob&, off: usize) -> static u8&:
    return self.data[off].ref[u8&].cast[static u8&]
`)
	joined := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(joined, "out-of-bounds") {
		t.Fatalf("expected unbounded-string-from-buffer warning, got:\n%s", joined)
	}
}

// A string literal coerced to `static u8&` is bounded by its own terminator and
// must NOT be flagged.
func TestStaticStringFromLiteralIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "static_str_literal.elisa", `def label() -> static u8&:
    return "hello"
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for string literal, got:\n%s", strings.Join(errs, "\n"))
	}
}

// Reinterpreting a byte-darray element as a wider struct reads past the indexed
// byte and must be flagged.
func TestStructReinterpretFromByteDArrayIsFlagged(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_reinterpret_darray.elisa", `struct Header:
    a: u32
    b: u32

struct Blob:
    data: mutable darray[u8]

def header_at(self: Blob&, off: usize) -> Header&:
    return self.data[off].ref[u8&].cast[Header&]
`)
	joined := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(joined, "out-of-bounds") {
		t.Fatalf("expected buffer-reinterpret warning for struct overcast, got:\n%s", joined)
	}
}

// Reinterpreting a byte-darray element as a wider scalar (u32) is also flagged.
func TestWideScalarReinterpretFromByteDArrayIsFlagged(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "u32_reinterpret_darray.elisa", `struct Blob:
    data: mutable darray[u8]

def word_at(self: Blob&, off: usize) -> u32&:
    return self.data[off].ref[u8&].cast[u32&]
`)
	joined := strings.Join(result.Warnings(), "\n")
	if !strings.Contains(joined, "out-of-bounds") {
		t.Fatalf("expected buffer-reinterpret warning for wide-scalar overcast, got:\n%s", joined)
	}
}

// Taking a single mutable byte reference into a darray (not a string) is bounded
// by the index and must NOT be flagged.
func TestMutableByteRefFromDArrayIsAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "mutable_byteref_darray.elisa", `struct Blob:
    data: mutable darray[u8]

def first(self: mutable Blob&) -> u8:
    r: mutable u8& = self.data[0].ref[mutable u8&]
    return r
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("expected no diagnostics for single mutable byte reference, got:\n%s", strings.Join(errs, "\n"))
	}
}
