package semantic

import (
	"strings"
	"testing"
)

// UFCS: `value.method(args)` on a concrete receiver resolves to the conforming impl method.
func TestUFCSConcreteImplMethodDispatch(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_impl_method.elisa", `
struct Point:
    x: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

impl Eq for Point:
    def eq(self: Point, other: Point) -> bool:
        return self.x == other.x

def same(a: Point, b: Point) -> bool:
    return a.eq(b)
`)
}

// UFCS: `value.method(args)` resolves to a protocol DEFAULT method when the impl omits it.
func TestUFCSProtocolDefaultMethodDispatch(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_default_method.elisa", `
struct Point:
    x: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool
    def neq(self: Self, other: Self) -> bool:
        return not Self.eq(self, other)

impl Eq for Point:
    def eq(self: Point, other: Point) -> bool:
        return self.x == other.x

def differ(a: Point, b: Point) -> bool:
    return a.neq(b)
`)
}

// Ergonomic win: a protocol default body can use `self.method(...)` UFCS (Self is the bound
// type param) instead of the qualified `Self.method(self, ...)` form.
func TestUFCSSelfInDefaultBody(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_self_default.elisa", `
struct Point:
    x: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool
    def neq(self: Self, other: Self) -> bool:
        return not self.eq(other)

impl Eq for Point:
    def eq(self: Point, other: Point) -> bool:
        return self.x == other.x

def differ(a: Point, b: Point) -> bool:
    return a.neq(b)
`)
}

// UFCS through a `[T: Protocol]` bound: a bounded generic body can call `t.method(...)`.
func TestUFCSBoundedGenericMethodDispatch(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_bounded_generic.elisa", `
struct Point:
    x: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

impl Eq for Point:
    def eq(self: Point, other: Point) -> bool:
        return self.x == other.x

def same[T: Eq](a: T, b: T) -> bool:
    return a.eq(b)

def use() -> bool:
    p: Point = Point{x: 1}
    q: Point = Point{x: 2}
    return same[Point](p, q)
`)
}

// A non-conforming concrete receiver does NOT resolve the protocol method via UFCS.
func TestUFCSNonConformingReceiverErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ufcs_nonconforming.elisa", `
struct Bare:
    y: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

def same(a: Bare, b: Bare) -> bool:
    return a.eq(b)
`, AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Fatalf("expected an error: a non-conforming receiver must not resolve a protocol method via UFCS")
	}
}

// Existing qualified `Type.method(...)` calls still resolve unchanged alongside the UFCS form.
func TestUFCSQualifiedCallStillResolves(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_qualified_coexist.elisa", `
struct Point:
    x: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

impl Eq for Point:
    def eq(self: Point, other: Point) -> bool:
        return self.x == other.x

def same_qualified(a: Point, b: Point) -> bool:
    return Point.eq(a, b)

def same_ufcs(a: Point, b: Point) -> bool:
    return a.eq(b)
`)
}

// A real struct field still wins over a same-named protocol method (field access, not a call).
func TestUFCSRealFieldNotShadowed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ufcs_field_precedence.elisa", `
struct Point:
    x: i64

protocol Eq:
    def eq(self: Self, other: Self) -> bool

impl Eq for Point:
    def eq(self: Point, other: Point) -> bool:
        return self.x == other.x

def read_x(p: Point) -> i64:
    return p.x
`, AnalyzeOptions{})
	if joined := strings.Join(result.Errors(), "\n"); joined != "" {
		t.Fatalf("expected real field access to resolve cleanly, got:\n%s", joined)
	}
}
