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

// A SUFFIXLESS literal exceeding i64 max is accepted when the target type is a 64-bit unsigned type
// (u64/usize/uintptr) — the declared type stands in for the `u64` suffix — but is rejected for a
// signed or unknown target, so overflow stays an error there. (docs: suffix-free large literals.)
func TestParseIntLiteralForTypeUnsignedTarget(t *testing.T) {
	big := &ast.IntLit{Value: "0xcbf29ce484222325", IsHex: true} // FNV-1a basis, > i64 max
	for _, name := range []string{"u64", "usize", "uintptr"} {
		if v, ok := ParseIntLiteralForType(big, &BuiltinType{Name: name}); !ok || uint64(v) != 0xcbf29ce484222325 {
			t.Fatalf("ParseIntLiteralForType(big, %s) = (%#x, %v), want (0xcbf29ce484222325, true)", name, uint64(v), ok)
		}
	}
	for _, name := range []string{"i64", "isize", "u32", "int"} {
		if _, ok := ParseIntLiteralForType(big, &BuiltinType{Name: name}); ok {
			t.Fatalf("ParseIntLiteralForType(big, %s) must fail (overflow stays an error in non-u64 contexts)", name)
		}
	}
	if _, ok := ParseIntLiteralForType(big, nil); ok {
		t.Fatalf("ParseIntLiteralForType(big, nil) must fail (no target type to authorize unsigned parse)")
	}
	// A small suffixless literal is unaffected regardless of target.
	if v, ok := ParseIntLiteralForType(&ast.IntLit{Value: "42"}, &BuiltinType{Name: "i64"}); !ok || v != 42 {
		t.Fatalf("small literal must parse identically, got (%d, %v)", v, ok)
	}
}
