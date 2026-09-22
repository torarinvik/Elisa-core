package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS-NEGATIVE: a numeric/bool reference auto-reads as its referent value only when it is
// PROVEN non-null. A `T&?` (an optional parameter, a dict lookup `d[k]`) used to read through the
// possibly-null pointer in every value context below -- a segfault at -O0 and a garbage value at
// -O2, with no Unsafe grant anywhere. Each context must now reject the unproven reference.
func TestNullableScalarRefHasNoImplicitValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"decl", "def f(r: i64&?) -> i64:\n    v: i64 = r\n    v\n", `variable "v" expects i64, got i64&?`},
		{"return", "def f(r: i64&?) -> i64:\n    return r\n", "return type expects i64, got i64&?"},
		{"argument", "def take(x: i64) -> i64:\n    x\n\ndef f(r: i64&?) -> i64:\n    take(r)\n", `argument 1 to "take" expects i64, got i64&?`},
		{"assign", "def f(r: i64&?) -> i64:\n    x: mutable i64 = 0\n    x <- r\n    x\n", "cannot assign i64&? to i64"},
		// `r + 1` must not fall through to pointer arithmetic either: that would silently turn
		// value arithmetic into address stepping instead of rejecting it.
		{"plus", "def f(r: i64&?) -> i64:\n    r + 1\n", "operator requires numeric operands"},
		{"minus_right", "def f(r: i64&?) -> i64:\n    1 - r\n", "operator requires numeric operands"},
		{"times", "def f(r: i64&?) -> i64:\n    r * 2\n", "operator requires numeric operands"},
		{"less", "def f(r: i64&?) -> bool:\n    r < 5\n", "comparison requires numeric operands"},
		{"equal", "def f(r: i64&?) -> bool:\n    r == 5\n", "cannot compare i64&? and int"},
		{"logical", "def f(b: bool&?) -> bool:\n    b and true\n", "logical operator requires bool operands"},
		{"darray_literal", "def f(r: i64&?) -> i64 can[Memory.Allocate]:\n    xs: darray[i64] = [r]\n    xs[0]\n", "darray literal element expects i64, got i64&?"},
		{"struct_literal", "struct S:\n    a: i64\n\ndef f(r: i64&?) -> i64:\n    s = S{a: r}\n    s.a\n", `struct literal field "a" expects i64, got i64&?`},
		{"shorthand_conversion", "def f(r: i64&?) -> i32:\n    r.i32()\n", "value conversion requires proven non-null reference, got i64&?"},
		{"same_type_conversion", "def f(r: i64&?) -> i64:\n    r.i64()\n", "value conversion requires proven non-null reference, got i64&?"},
		{"constructor_conversion", "def f(r: i64&?) -> i64:\n    i64(r)\n", "value conversion requires proven non-null reference, got i64&?"},
		{"field_store", "struct S:\n    a: mutable i64\n\ndef f(r: i64&?) -> i64:\n    s: mutable S = S{a: 0}\n    s.a <- r\n    s.a\n", "cannot assign i64&? to i64"},
		{"element_store", "def f(r: i64&?) -> i64 can[Memory.Allocate]:\n    xs: mutable darray[i64] = [0]\n    xs[0] <- r\n    xs[0]\n", "cannot assign i64&? to i64"},
		{"bitwise", "def f(r: i64&?) -> i64:\n    r & 1\n", "operator requires numeric operands"},
		{"shift", "def f(r: i64&?) -> i64:\n    r << 1\n", "operator requires numeric operands"},
		{"inferred_local", "def f(r: i64&?) -> i64:\n    x = r\n    y: i64 = x + 0\n    y\n", "operator requires numeric operands"},
		{"float", "def f(r: f64&?) -> f64:\n    v: f64 = r\n    v\n", `variable "v" expects f64, got f64&?`},
		{"byte", "def f(p: u8&?) -> u8:\n    v: u8 = p\n    v\n", `variable "v" expects u8, got u8&?`},
		// A write through the reference needs the same proof as a read: `r <- v` / `r += v` store
		// through the pointer, and inside `if r == null:` it is proven null, not merely unproven.
		{"write_through", "def f(r: mutable i64&?) -> void:\n    r <- 5\n", "assignment through reference requires proven non-null reference, got mutable i64&?"},
		{"aug_write_through", "def f(r: mutable i64&?) -> void:\n    r += 1\n", "assignment through reference requires proven non-null reference, got mutable i64&?"},
		{"write_in_null_branch", "def f(r: mutable i64&?) -> void:\n    if r == null:\n        r <- 5\n", "assignment through reference requires proven non-null reference"},
		// A generic `T&` parameter is non-null too. The nullable argument used to be passed as the
		// address of its SLOT while T bound to the referent: the callee read the pointer bits as a
		// Node (7 read back as 120), and a null argument passed silently. `&p` passes the slot.
		{"generic_ref_param", "struct Node:\n    a: i32\n\ndef take[T](out: mutable T&) -> void:\n    pass\n\ndef f(p: mutable Node&?) -> void:\n    take(p)\n", `argument 1 to "take" expects mutable Node&, got mutable Node&?`},
		{"generic_void_ref_param", "def take[T](out: mutable T&) -> void:\n    pass\n\ndef f() -> void:\n    p: mutable void&? = null\n    take(p)\n", `argument 1 to "take" expects mutable void&, got mutable void&?`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nullable_scalar_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			found := false
			for _, err := range result.Errors() {
				if strings.Contains(err, tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("an unproven `T&?` has no value to read here; want %q, got: %v", tc.want, result.Errors())
			}
		})
	}
}

// POSITIVE: the proven and address-only forms keep compiling. A flow proof (`!= null`, `is x`)
// narrows the reference to non-null, a non-null `T&` still auto-reads, and `.cast[T]` /
// `.uintptr()` read the address, never the referent.
func TestNullableScalarRefProvenAndAddressFormsCompile(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"narrowed_not_null", "def f(r: i64&?) -> i64:\n    if r != null:\n        v: i64 = r\n        return v + 1\n    0\n"},
		{"narrowed_is", "def f(r: i64&?) -> i64:\n    if r is x:\n        v: i64 = x\n        return v + 1\n    0\n"},
		{"narrowed_else", "def f(r: i64&?) -> i64:\n    if r == null:\n        return 0\n    else:\n        return r + 1\n"},
		{"get_unwrap", "def f(r: i64&?) -> i64:\n    x = get r else return 0\n    v: i64 = x\n    v\n"},
		{"nonnull_decl", "def f(r: i64&) -> i64:\n    v: i64 = r\n    v\n"},
		{"nonnull_arith", "def f(r: i64&) -> i64:\n    r + 1\n"},
		{"nonnull_assign", "def f(r: i64&) -> i64:\n    x: mutable i64 = 0\n    x <- r\n    x\n"},
		{"nonnull_conversion", "def f(r: i64&) -> i32:\n    r.i32()\n"},
		{"nonnull_struct_literal", "struct S:\n    a: i64\n\ndef f(r: i64&) -> i64:\n    s = S{a: r}\n    s.a\n"},
		{"address_cast", "def f(r: i64&?) -> i64:\n    r.cast[i64]\n"},
		{"address_uintptr", "def f(r: i64&?) -> uintptr:\n    r.uintptr()\n"},
		{"null_compare", "def f(r: i64&?) -> bool:\n    r == null\n"},
		{"narrowed_write", "def f(r: mutable i64&?) -> void:\n    if r != null:\n        r <- 5\n        r += 1\n"},
		{"narrowed_early_return_write", "def f(r: mutable i64&?) -> void:\n    if r == null:\n        return\n    r <- 5\n"},
		{"get_write", "def f(r: mutable i64&?) -> void:\n    x = get r else return\n    x <- 5\n    x += 1\n"},
		{"nonnull_write", "def f(r: mutable i64&) -> void:\n    r <- 5\n    r += 1\n"},
		{"generic_slot_address", "def take[T](out: mutable T&) -> void:\n    pass\n\ndef f() -> void:\n    p: mutable void&? = null\n    take(&p)\n"},
		{"generic_nonnull_ref", "struct Node:\n    a: i32\n\ndef take[T](out: mutable T&) -> void:\n    pass\n\ndef f(p: mutable Node&) -> void:\n    take(p)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nullable_scalar_ok_"+tc.name+".elisa", tc.src, AnalyzeOptions{})
			if errs := result.Errors(); len(errs) != 0 {
				t.Fatalf("a proven or address-only use must compile, got: %v", errs)
			}
		})
	}
}
