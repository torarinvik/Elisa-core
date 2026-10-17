package semantic

import (
	"fmt"
	"testing"
)

func TestSMTIntegerCastConstantsInArrayContracts(t *testing.T) {
	for _, typ := range []string{"u8", "u16", "u32", "i8", "i16", "i32"} {
		for _, bodyValue := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s_%d", typ, bodyValue), func(t *testing.T) {
				source := fmt.Sprintf(`def check(rows: darray[%s]&, index: usize) -> bool:
    requires index < rows.count
    ensure result == (rows[index] == 1.%s())
    return rows[index] == %d
`, typ, typ, bodyValue)
				r := analyzeContractStrict(t, "constant_cast.elisa", source)
				if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != (bodyValue != 1) {
					t.Fatalf("body %d: rejected %v:\n%s", bodyValue, rejected, allDiagnostics(r))
				}
			})
		}
	}
}

func TestSMTIntegerCastLiteralBounds(t *testing.T) {
	for _, tc := range []struct{ typ, value string }{
		{"u8", "255"}, {"u16", "65535"}, {"u32", "4294967295"},
		{"i8", "(-128)"}, {"i16", "(-32768)"}, {"i32", "(-2147483648)"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			source := fmt.Sprintf(`def check(rows: darray[%s]&, index: usize) -> bool:
    requires index < rows.count
    ensure result == (rows[index] == %s.%s())
    return rows[index] == %s
`, tc.typ, tc.value, tc.typ, tc.value)
			r := analyzeContractStrict(t, "cast_bound.elisa", source)
			if len(r.Errors()) != 0 {
				t.Fatalf("literal boundary declined:\n%s", allDiagnostics(r))
			}
		})
	}
	r := analyzeContractStrict(t, "cast_wrap.elisa", `def check(value: i64) -> i8:
    ensure result == value
    return value.i8()
`)
	if ensureFailuresFor(r.Errors(), "check") == 0 {
		t.Fatalf("dynamic narrowing treated as identity:\n%s", allDiagnostics(r))
	}
}

func TestSMTIntegerCastDoesNotPreserveOutOfRangeLiterals(t *testing.T) {
	for _, value := range []string{"256", "(-1)"} {
		r := analyzeContractStrict(t, "cast_outside.elisa", fmt.Sprintf(`def check(rows: darray[u8]&, index: usize) -> bool:
    requires index < rows.count
    ensure result == (rows[index].i64() == %s)
    return rows[index] == %s.u8()
`, value, value))
		if ensureFailuresFor(r.Errors(), "check") == 0 {
			t.Fatalf("out-of-range cast %s treated as identity:\n%s", value, allDiagnostics(r))
		}
	}
}
