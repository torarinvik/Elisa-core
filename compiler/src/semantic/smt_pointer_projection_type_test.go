//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestSMTPointerSubstitutedViewProjection(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		reject       bool
	}{
		{"same_view", `def check(view: sview) -> sview:
    ensure result.data == view.data
    return view
`, false},
		{"guarded_same_view", `def check(view: sview, flag: bool) -> sview:
    ensure (flag and result.len > 0) implies result.data == view.data
    return view
`, false},
		{"guarded_wrong_view", `def check(view: sview, other: sview, flag: bool) -> sview:
    requires view.data != other.data
    ensure (flag and result.len > 0) implies result.data == view.data
    return other
`, true},
		{"branch_same_view", `def check(view: sview, other: sview, flag: bool) -> sview:
    ensure (flag and result.len > 0) implies result.data == view.data
    can Unsafe.PointerArithmetic:
        return other if not flag
        return view
`, false},
		{"branch_wrong_view", `def check(view: sview, other: sview, flag: bool) -> sview:
    requires view.data != other.data
    ensure (flag and result.len > 0) implies result.data == view.data
    can Unsafe.PointerArithmetic:
        return view if not flag
        return other
`, true},
		{"branch_float_is_not_reflexive", `struct FloatBox:
    value: f64
def check(box: FloatBox, other: FloatBox, flag: bool) -> FloatBox:
    ensure flag implies result.value == box.value
    can Unsafe.PointerArithmetic:
        return other if not flag
        return box
`, true},
		{"nested_return", `def check(view: sview) -> (found: bool, value: sview):
    ensure result.value.data == view.data
    return (true, view)
`, false},
		{"wrong_reflexive", `def check(view: sview) -> sview:
    ensure result.data != view.data
    return view
`, true},
		{"different_views", `def check(view: sview, other: sview) -> sview:
    requires view.data != other.data
    ensure result.data == view.data
    return other
`, true},
		{"callee_formal_shadow", `def same_data(view: sview, peer: sview) -> bool:
    return view.data == peer.data
def check(left: sview, right: sview) -> bool:
    requires left.data != right.data
    ensure result == true
    can Unsafe.PointerCast:
        view: sview = StringView{data: left.data, len: left.len}
        peer: sview = StringView{data: left.data, len: left.len}
        return same_data(left, right)
`, true},
		{"different_helper_calls", `def same_address(left: sview, right: sview) -> bool:
    return left.data == right.data
def check(a: sview, b: sview, c: sview) -> bool:
    ensure result == (same_address(a, b) == same_address(a, c))
    return true
`, true},
		{"conditional_helper_calls", `def same_address(left: sview, right: sview, flag: bool) -> bool:
    return (left.data if flag else right.data) == (right.data if flag else left.data)
def check(a: sview, b: sview, c: sview, flag: bool) -> bool:
    ensure result == (same_address(a, b, flag) == same_address(a, c, flag))
    return true
`, true},
		{"repeated_helper_call", `def same_address(left: sview, right: sview) -> bool:
    return left.data == right.data
def check(a: sview, b: sview) -> bool:
    ensure result == (same_address(a, b) == same_address(a, b))
    return true
`, false},
		{"content_is_not_address", `def same_address(left: sview, right: sview) -> bool:
    return left.data == right.data
def check(a: sview, b: sview) -> bool:
    requires a == b
    requires a.data != b.data
    ensure result == same_address(a, b)
    return true
`, true},
		{"nullable_field_calls", `struct Box:
    data: void&?
def present(box: Box) -> bool:
    return box.data != null
def check(a: Box, b: Box) -> bool:
    requires a.data == null and b.data != null
    ensure result == (present(a) == present(b))
    return true
`, true},
		{"overloaded_written_field_equality", `struct Point:
    value: i64
struct Holder:
    point: mutable Point
protocol Eq:
    def __eq__(left: Self, right: Self) -> bool
impl Eq for Point:
    def __eq__(left: Point, right: Point) -> bool:
        return false
def check(holder: mutable Holder&, point: Point):
    ensure holder.point == point
    holder.point <- point
`, true},
		{"argument_sensitive_value_equality", `struct Point:
    value: i64
protocol Eq:
    def __eq__(left: Self, right: Self) -> bool
impl Eq for Point:
    def __eq__(left: Point, right: Point) -> bool:
        return left.value == right.value
def check(point: Point) -> Point:
    ensure result == point
    return point
`, false},
		{"overloaded_value_equality", `struct Point:
    value: i64
protocol Eq:
    def __eq__(left: Self, right: Self) -> bool
impl Eq for Point:
    def __eq__(left: Point, right: Point) -> bool:
        return false
def check(point: Point) -> Point:
    ensure result == point
    return point
`, true},
		{"reference_identity_with_value_overload", `struct Point:
    value: i64
protocol Eq:
    def __eq__(left: Self, right: Self) -> bool
impl Eq for Point:
    def __eq__(left: Point, right: Point) -> bool:
        return false
def check(point: Point&) -> Point&:
    ensure result == point
    return point
`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "pointer_projection_type.elisa", tc.source)
			for _, diagnostic := range r.Errors() {
				if !strings.Contains(diagnostic, `ensure postcondition of "check"`) {
					t.Fatalf("unrelated control error: %s", allDiagnostics(r))
				}
			}
			rejected := ensureFailuresFor(r.Errors(), "check") != 0
			if rejected != tc.reject {
				t.Fatalf("rejected %v, want %v: %s", rejected, tc.reject, allDiagnostics(r))
			}
			if !tc.reject && len(r.Errors()) != 0 {
				t.Fatalf("unexpected errors: %s", allDiagnostics(r))
			}
		})
	}
}

func TestSMTConcreteEqualityUsesSelectedNamespace(t *testing.T) {
	source := `protocol Eq:
    def __eq__(left: Self, right: Self) -> bool
module A:
    public:
        struct Point:
            value: i64
        impl Eq for Point:
            def __eq__(left: Point, right: Point) -> bool:
                return left.value == right.value
        def equal(point: Point) -> bool:
            return point == point
module B:
    public:
        struct Point:
            value: i64
        impl Eq for Point:
            def __eq__(left: Point, right: Point) -> bool:
                return false
        def check(point: A::Point) -> bool:
            ensure result == true
            return A::equal(point)
`
	r := analyzeContractStrict(t, "selected_eq_namespace.elisa", source)
	if len(r.Errors()) != 0 {
		t.Fatalf("selected A::Point equality lost: %s", allDiagnostics(r))
	}
}
