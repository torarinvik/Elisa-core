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
		{"write_free_helper_index", `def next(index: usize) -> i64:
    return index.i64() + 1
def check(buf: u8&, index: usize) -> bool:
    ensure &buf[next(index).usize()] == &buf[next(index).usize()]
    return true
`, false},
		{"write_free_helper_constructor_field", `def last_byte(view: sview, byte: u8) -> i64:
    ensure result >= -1
    ensure result >= 0 implies result < view.len
    trusted Unsafe.UncheckedIndex:
        return -1 if view.len < 0
        view_len: usize = view.len.usize()
        return -1 if view_len.i64() != view.len
        assert view.len >= 0 and view_len.i64() == view.len
        i: mutable i64 = view.len
        while i > 0 |i|:
            decreases i
            invariant 0 <= i and i <= view.len
            candidate: i64 = i - 1
            if view[candidate] == byte.i64():
                return candidate
            i <- candidate
        return -1
def check(view: sview) -> ElementAddress:
    ensure result.data == view.data + (last_byte(view, 47) + 1).usize()
    trusted Unsafe.PointerArithmetic:
        return ElementAddress{data: view.data + (last_byte(view, 47) + 1).usize(), changed: false}
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

func TestSMTPointerIdentitySViewConstructorProjection(t *testing.T) {
	r := analyzeTreeTestSourceWithSemanticErrors(t, "elisacore_runtime_strings.elisa", `def last_byte(view: sview, byte: u8) -> i64:
    ensure (view.len < 0 or view.len.usize().i64() != view.len) implies result == -1
    ensure result >= -1
    ensure result >= 0 implies result < view.len
    ensure (result >= 0) implies string_view_index(view, result) == byte.i64()
    trusted Unsafe.UncheckedIndex:
        return -1 if view.len < 0
        view_len: usize = view.len.usize()
        return -1 if view_len.i64() != view.len
        assert view.len >= 0 and view_len.i64() == view.len
        i: mutable i64 = view.len
        while i > 0 |i|:
            decreases i
            invariant 0 <= i and i <= view.len
            candidate: i64 = i - 1
            if string_view_index(view, candidate) == byte.i64():
                return candidate
            i <- candidate
        return -1

def filename(view: sview) -> sview:
    ensure result.data == view.data + (last_byte(view, 47) + 1).usize()
    can Unsafe{PointerArithmetic,PointerCast}:
        return StringView{data: view.data + (last_byte(view, 47) + 1).usize(), len: view.len}
`)
	if failures := ensureFailuresFor(r.Errors(), "filename"); failures != 0 {
		t.Fatalf("SView constructor field did not retain pointer identity (%d failures):\n%s", failures, allDiagnostics(r))
	}
}
