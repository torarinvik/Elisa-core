package semantic

import "testing"

func TestSMTPointerIdentityIsStrongerThanNullness(t *testing.T) {
	for _, tc := range []struct {
		name, contract string
		reject         bool
	}{
		{"reflexive", "requires p != null\n    ensure p == p", false},
		{"symmetric", "requires p == q\n    ensure q == p", false},
		{"nonnull", "requires p != null and q != null\n    ensure p == q", true},
		{"same_nullness", "requires (p == null) == (q == null)\n    ensure p == q", true},
		{"both_null", "requires p == null and q == null\n    ensure p == q", false},
		{"equal_nullness", "requires p == q\n    ensure (p == null) == (q == null)", false},
		{"different_nullness", "requires p == null and q != null\n    ensure p != q", false},
		{"disequality", "requires p != q\n    ensure p == q", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "pointer_identity.elisa", "def check(p: void&?, q: void&?) -> bool:\n    "+tc.contract+"\n    return true\n")
			if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != tc.reject {
				t.Fatalf("rejected %v, want %v:\n%s", rejected, tc.reject, allDiagnostics(r))
			}
		})
	}
}

func TestSMTPointerConstructorIdentity(t *testing.T) {
	const types = `struct PointerBox:
    data: void&?
    changed: bool
`
	for _, tc := range []struct {
		name, body string
		reject     bool
	}{
		{"member_reflexive", `def check(box: PointerBox) -> bool:
    requires box.data != null
    ensure box.data == box.data
    return true
`, false},
		{"constructor", `def check(p: void&?) -> PointerBox:
    ensure result.data == p
    return PointerBox{data: p, changed: false}
`, false},
		{"nested_constructor", `def check(p: void&?) -> (known: bool, box: PointerBox):
    ensure result.box.data == p
    return (true, PointerBox{data: p, changed: false})
`, false},
		{"null_constructor", `def check() -> PointerBox:
    ensure result.data == null
    return PointerBox{data: null, changed: false}
`, false},
		{"nonnull_constructor", `def check(p: void&?) -> PointerBox:
    requires p != null
    ensure result.data != null
    return PointerBox{data: p, changed: false}
`, false},
		{"wrong_null_constructor", `def check(p: void&?) -> PointerBox:
    requires p != null
    ensure result.data == null
    return PointerBox{data: p, changed: false}
`, true},
		{"different_pointer", `def check(p: void&?, q: void&?) -> PointerBox:
    requires p != null and q != null
    ensure result.data == q
    return PointerBox{data: p, changed: false}
`, true},
		{"shadowed_parameter", `def check(p: void&?, q: void&?) -> PointerBox:
    requires p != q
    ensure result.data == p
    if true:
        p: void&? = q
        return PointerBox{data: p, changed: false}
    return PointerBox{data: p, changed: false}
`, true},
		{"shadowed_null_fact", `def check(p: void&?, q: void&?) -> PointerBox:
    requires p != null and q == null
    ensure result.data == p
    if true:
        p: void&? = q
        assert p == null
        return PointerBox{data: p, changed: false}
    return PointerBox{data: p, changed: false}
`, true},
		{"captured_before_write", `def change(value: mutable void&?&) -> bool:
    value <- null
    return true
def check(value: mutable void&?) -> PointerBox:
    requires value != null
    ensure result.data == value
    return PointerBox{data: value, changed: change(&value)}
`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "pointer_constructor.elisa", types+tc.body)
			if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != tc.reject {
				t.Fatalf("rejected %v want %v:\n%s", rejected, tc.reject, allDiagnostics(r))
			}
		})
	}
}

func TestSMTPointerProofPreservesOverloadedValueEquality(t *testing.T) {
	for _, tc := range []struct {
		goal   string
		reject bool
	}{{"p == p", true}} {
		r := analyzeContractStrict(t, "pointer_overload.elisa", `struct Point:
    value: i64
protocol Eq:
    def __eq__(left: Self, right: Self) -> bool
impl Eq for Point:
    def __eq__(left: Point, right: Point) -> bool:
        return false
def check(p: Point) -> bool:
    ensure `+tc.goal+`
    return true
`)
		if rejected := ensureFailuresFor(r.Errors(), "check") != 0; rejected != tc.reject {
			t.Fatalf("overloaded equality goal %s: rejected %v want %v:\n%s", tc.goal, rejected, tc.reject, allDiagnostics(r))
		}
	}
}

func TestSMTPointerOffsetConstructorIdentity(t *testing.T) {
	r := analyzeContractStrict(t, "pointer_offset.elisa", `struct BytePointerBox:
    data: u8&
def check(p: u8&, offset: usize) -> BytePointerBox:
    ensure result.data == p + offset
    can Unsafe.PointerArithmetic:
        return BytePointerBox{data: p + offset}
`)
	if len(r.Errors()) != 0 {
		t.Fatalf("pointer offset constructor declined:\n%s", allDiagnostics(r))
	}
}

func TestSMTPointerConditionalOffsetConstructor(t *testing.T) {
	r := analyzeContractStrict(t, "conditional_pointer.elisa", `struct BytePointerBox:
    data: u8&
def check(p: u8&, offset: i64) -> BytePointerBox:
    ensure result.data == p + (offset if offset > 0 else 0).usize()
    can Unsafe.PointerArithmetic:
        return BytePointerBox{data: p + (offset if offset > 0 else 0).usize()}
`)
	if len(r.Errors()) != 0 {
		t.Fatalf("pure conditional pointer declined:\n%s", allDiagnostics(r))
	}
}

func TestSMTPointerConditionalConstructorDoesNotRereadCapturedValue(t *testing.T) {
	for _, expression := range []string{"clear(&value) if flag else false", "false if clear(&value) else false", "false if not flag else clear(&value)"} {
		r := analyzeContractStrict(t, "conditional_capture.elisa", `struct PointerCapture:
    data: void&?
    changed: bool
def clear(value: mutable void&?&) -> bool:
    value <- null
    return true
def check(value: mutable void&?, flag: bool) -> PointerCapture:
    requires value != null and flag
    ensure result.data == value
    return PointerCapture{data: value, changed: `+expression+`}
`)
		if ensureFailuresFor(r.Errors(), "check") == 0 {
			t.Fatalf("conditional effect reread captured pointer:\n%s", allDiagnostics(r))
		}
	}
}
