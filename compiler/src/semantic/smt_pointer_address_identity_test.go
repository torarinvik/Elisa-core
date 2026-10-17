package semantic

import "testing"

func TestSMTPointerBuiltinElementAddressIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"dynamic_array", `def check(buf: darray[u8]&, start: usize) -> ElementAddress:
    requires start < buf.count
    ensure result.data == &buf[start]
    trusted Unsafe.UncheckedIndex:
        return ElementAddress{data: &buf[start], changed: false}
`, false},
		{"fixed_array", `def check(buf: u8[8]&, start: usize) -> ElementAddress:
    requires start < 8
    ensure result.data == &buf[start]
    trusted Unsafe.UncheckedIndex:
        return ElementAddress{data: &buf[start], changed: false}
`, false},
		{"raw_pointer", `def check(buf: u8&, start: usize) -> ElementAddress:
    ensure result.data == &buf[start]
    trusted Unsafe.UncheckedIndex:
        return ElementAddress{data: &buf[start], changed: false}
`, false},
		{"different_index", `def check(buf: u8&, start: usize) -> ElementAddress:
    ensure result.data == &buf[start + 1]
    trusted Unsafe.UncheckedIndex:
        return ElementAddress{data: &buf[start], changed: false}
`, true},
		{"effectful_index", `def next(index: mutable usize&) -> usize:
    index <- index + 1
    return index
def check(buf: u8&, index: mutable usize) -> bool:
    ensure &buf[next(&index)] == &buf[next(&index)]
    return true
`, true},
		{"captured_index", `def next(index: mutable usize&) -> bool:
    index <- index + 1
    return true
def check(buf: u8&, index: mutable usize) -> ElementAddress:
    ensure result.data == &buf[index]
    trusted Unsafe.UncheckedIndex:
        return ElementAddress{data: &buf[index], changed: next(&index)}
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "element_address.elisa", "struct ElementAddress:\n    data: u8&\n    changed: bool\n"+tc.body)
			rejected := ensureFailuresFor(r.Errors(), "check") != 0
			if rejected != tc.reject {
				t.Fatalf("rejected %v, want %v:\n%s", rejected, tc.reject, allDiagnostics(r))
			}
			if !tc.reject && len(r.Errors()) != 0 {
				t.Fatalf("unexpected errors: %s", allDiagnostics(r))
			}
		})
	}
}
