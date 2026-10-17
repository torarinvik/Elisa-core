//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

const boolViewDigit = `def digit(byte: u8) -> bool:
    return byte >= 48 and byte <= 57
`

func TestSMTBoolViewInitializers(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"exists", `def check() -> bool:
    ensure result == true
    saved: bool = exists index in 0..<2: index == 1
    return saved
`, false},
		{"empty_exists_false_claim", `def check() -> bool:
    ensure result == true
    saved: bool = exists index in 2..<2: true
    return saved
`, true},
		{"callback_shadow", boolViewDigit + `def check(value: u8, digit: fn(u8) -> bool) -> bool:
    ensure result == (value >= 48 and value <= 57)
    saved: bool = digit(value)
    return saved
`, true},
		{"shape", boolViewDigit + `def check(name: sview) -> bool:
    ensure result == (name.len >= 2 and (name[0] == 105 or name[0] == 117) and (forall index in 0..<name.len: index < 1 or digit(name[index])))
    prefix: bool = name.len >= 2 and (name[0] == 105 or name[0] == 117)
    digits: bool = forall index in 0..<name.len: index < 1 or digit(name[index])
    return prefix and digits
`, false},
		{"wrong_prefix_offset", boolViewDigit + `def check(name: sview) -> bool:
    ensure result == (name.len >= 2 and name[0] == 105)
    prefix: bool = name.len >= 2 and name[1] == 105
    return prefix
`, true},
		{"nested_binder_shadow", boolViewDigit + `def check(name: sview) -> bool:
    ensure result == (forall index in 0..<name.len: digit(name[index]))
    index: usize = 17
    digits: bool = forall index in 0..<2: forall index in 0..<name.len: digit(name[index])
    return digits
`, false},
		{"quantifier_is_not_first_byte", boolViewDigit + `def check(name: sview) -> bool:
    requires name.len > 1
    ensure result == digit(name[0])
    index: usize = 0
    digits: bool = forall index in 0..<name.len: digit(name[index])
    return digits
`, true},
		{"saved_byte_predicate", boolViewDigit + `def check(name: sview) -> bool:
    requires name.len > 0
    ensure result == digit(name[0])
    saved: bool = digit(name[0])
    return saved
`, false},
		{"opaque_mutation", boolViewDigit + `extern mutate() -> usize
def check(name: sview) -> bool:
    requires name.len > 0
    ensure result == digit(name[0])
    saved: bool = digit(name[0])
    can Unsafe.RawExtern:
        ignored: usize = mutate()
    return saved
`, true},
		{"nested_narrow_bound", `def check() -> bool:
    ensure result == true
    index: usize = 0
    saved: bool = forall index in 256.usize()..<257.usize(): forall j in 0.i8()..<index: false
    return saved
`, false},
		{"nested_narrow_bound_false_claim", `def check() -> bool:
    ensure result == false
    index: usize = 0
    saved: bool = forall index in 256.usize()..<257.usize(): forall j in 0.i8()..<index: false
    return saved
`, true},
		{"nested_mixed_width_shadow", `def check() -> bool:
    ensure result == true
    index: usize = 0
    saved: bool = forall index in 0..<257: index != 256 or (forall j in 0.i8()..<index: false)
    return saved
`, false},
		{"mixed_width_empty", `def check() -> bool:
    ensure result == true
    end: usize = 256
    saved: bool = forall index in 0.i8()..<end: false
    return saved
`, false},
		{"mixed_width_wrong_claim", `def check() -> bool:
    ensure result == false
    end: usize = 256
    saved: bool = forall index in 0.i8()..<end: false
    return saved
`, true},
		{"nested_outer_endpoint", `def check() -> bool:
    ensure result == false
    saved: bool = forall index in 0..<2: forall index in 0..<index: false
    return saved
`, false},
		{"outer_endpoint", `def check() -> bool:
    ensure result == false
    index: usize = 2
    saved: bool = forall index in 0..<index: false
    return saved
`, false},
		{"outer_endpoint_false_claim", `def check() -> bool:
    ensure result == true
    index: usize = 2
    saved: bool = forall index in 0..<index: false
    return saved
`, true},
		{"mutable_scalar_snapshot", boolViewDigit + `def check(value: u8) -> bool:
    ensure result == digit(0.u8())
    current: mutable u8 = value
    saved: bool = digit(current)
    current <- 0
    return saved
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "bool_view_initializer.elisa", tc.source)
			for _, diagnostic := range r.Errors() {
				if !strings.Contains(diagnostic, `ensure postcondition of "check"`) {
					t.Fatalf("unrelated error: %s", allDiagnostics(r))
				}
			}
			if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != tc.reject {
				t.Fatalf("rejected %v, want %v: %s", rejected, tc.reject, allDiagnostics(r))
			}
		})
	}
}
