//go:build cgo

package semantic

import (
	"fmt"
	"strings"
	"testing"
)

const permissionFrameReader = `def read_byte(view: sview, index: usize) -> usize:
    requires index < view.len
    return view[index].usize()
`

func TestSMTViewFrameUnusedUnsafePermissions(t *testing.T) {
	for _, permission := range []string{"PointerArithmetic", "PointerCast", "RawExtern"} {
		for _, index := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/index%d", permission, index), func(t *testing.T) {
				r := analyzeContractStrict(t, "unused_permission.elisa", permissionFrameReader+fmt.Sprintf(`def same(view: sview) -> usize:
    requires view.len > 1
    ensure result == read_byte(view, %d.usize())
    before: usize = read_byte(view, 0.usize())
    can Unsafe.%s:
        counter: mutable usize = 0
        counter <- counter + 1
    return before
`, index, permission))
				rejected := ensureFailuresFor(r.Errors(), "same") != 0
				if rejected != (index != 0) {
					t.Fatalf("rejected %v: %s", rejected, allDiagnostics(r))
				}
				if index == 0 && len(r.Errors()) != 0 {
					t.Fatalf("unexpected errors: %s", allDiagnostics(r))
				}
			})
		}
	}
}

func TestSMTViewFrameUnsafeScopesStillInspectBodies(t *testing.T) {
	effectfulOperators := `extern mutate() -> usize
struct Point:
    value: i64
protocol Eq:
    def __eq__(left: Self, right: Self) -> bool can[Unsafe.RawExtern]
impl Eq for Point:
    def __eq__(left: Point, right: Point) -> bool:
        can Unsafe.RawExtern:
            changed: usize = mutate()
        return true
protocol Neg:
    def __neg__(value: Self) -> Self can[Unsafe.RawExtern]
impl Neg for Point:
    def __neg__(value: Point) -> Point:
        can Unsafe.RawExtern:
            changed: usize = mutate()
        return value

`
	for _, tc := range []struct{ name, prefix, params, body string }{
		{"extern", "extern mutate(data: u8&) -> usize\n", ", data: u8&", "changed: usize = mutate(data)"},
		{"callback", "", ", callback: fn() -> usize", "changed: usize = callback()"},
		{"overload", effectfulOperators, ", point: Point", "changed: bool = point == point"},
		{"local_overload", effectfulOperators, ", point: Point", "copy: Point = point\n        changed: bool = copy == copy"},
		{"inferred_local_overload", effectfulOperators, ", point: Point", "copy = point\n        changed: bool = copy == copy"},
		{"unary_overload", effectfulOperators, ", point: Point", "changed: Point = -point"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := permissionFrameReader + tc.prefix + `def stale(view: sview` + tc.params + `) -> usize:
    requires view.len > 0
    ensure result == read_byte(view, 0.usize())
    before: usize = read_byte(view, 0.usize())
    can Unsafe{PointerArithmetic,PointerCast,RawExtern}:
        ` + tc.body + `
    return before
`
			r := analyzeContractStrict(t, "unsafe_scope_body.elisa", source)
			for _, diagnostic := range r.Errors() {
				if !strings.Contains(diagnostic, `ensure postcondition of "stale"`) {
					t.Fatalf("control has an unrelated error: %s", allDiagnostics(r))
				}
			}
			if ensureFailuresFor(r.Errors(), "stale") == 0 {
				t.Fatalf("opaque body must not identify byte reads: %s", allDiagnostics(r))
			}
		})
	}
}

func TestSMTViewFrameRawViewByteReads(t *testing.T) {
	r := analyzeContractStrict(t, "raw_view_byte.elisa", `def read(view: sview, index: usize) -> i64:
    requires index < view.len
    ensure result >= 0 and result <= 255
    trusted Unsafe.UncheckedIndex:
        return view.data[index].i64()
`)
	if len(r.Errors()) != 0 {
		t.Fatalf("raw view byte read lost its frame: %s", allDiagnostics(r))
	}
}

func TestSMTViewFrameBorrowedHeaderConstruction(t *testing.T) {
	r := analyzeContractStrict(t, "view_header_frame.elisa", permissionFrameReader+`def same(view: sview) -> usize:
    requires view.len > 0
    ensure result == read_byte(view, 0.usize())
    before: usize = read_byte(view, 0.usize())
    trusted Unsafe.PointerCast:
        header: sview = StringView{data: view.data, len: view.len}
    return before
`)
	if len(r.Errors()) != 0 {
		t.Fatalf("borrowed header must not invalidate bytes: %s", allDiagnostics(r))
	}
}
