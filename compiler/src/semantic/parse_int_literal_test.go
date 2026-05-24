package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestParseIntLiteralUnsignedHex(t *testing.T) {
	cases := []struct {
		name   string
		value  string
		suffix string
		isHex  bool
		want   uint64
	}{
		{"u64 max", "0xFFFFFFFFFFFFFFFF", "u64", true, ^uint64(0)},
		{"u64 top bit", "0x8000000000000000", "u64", true, uint64(1) << 63},
		{"u32 max", "0xFFFFFFFF", "u32", true, 0xFFFFFFFF},
		{"u64 decimal max", "18446744073709551615", "u64", false, ^uint64(0)},
		{"i64 positive hex", "0x7FFFFFFFFFFFFFFF", "i64", true, 0x7FFFFFFFFFFFFFFF},
		{"plain decimal", "42", "", false, 42},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lit := &ast.IntLit{Value: tc.value, Suffix: tc.suffix, IsHex: tc.isHex}
			got, ok := ParseIntLiteral(lit)
			if !ok {
				t.Fatalf("ParseIntLiteral(%q suffix=%q) failed", tc.value, tc.suffix)
			}
			if uint64(got) != tc.want {
				t.Fatalf("ParseIntLiteral(%q suffix=%q) = %d (%#x), want %#x",
					tc.value, tc.suffix, got, uint64(got), tc.want)
			}
		})
	}
}

func TestParseIntLiteralSignedOverflowStillRejected(t *testing.T) {
	// A value above int64 max with a signed suffix must not silently parse.
	lit := &ast.IntLit{Value: "0xFFFFFFFFFFFFFFFF", Suffix: "i64", IsHex: true}
	if _, ok := ParseIntLiteral(lit); ok {
		t.Fatalf("expected signed parse of u64-max to fail")
	}
}
