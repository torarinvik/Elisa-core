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

// A concrete receiver must select an impl[T] protocol method and specialize its
// associated-type result, just as the already-supported [R: Read] path does.
func TestUFCSParametricImplMethodDispatchOnConcreteReceiver(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_parametric_impl_concrete.elisa", `
struct Box[T]:
    item: T

protocol Read:
    type Elem
    def read(s: Self&) -> Elem

impl[T] Read for Box[T]:
    type Elem = T
    def read(s: Box[T]&) -> T:
        return s.item

def direct(b: Box[i64]) -> i64:
    return b.read()

def through_bound[R: Read](r: R&) -> R.Elem:
    return r.read()
`)
}

func TestUFCSParametricImplMethodExplicitTypeArgsLabelsAndMutableReceiver(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "ufcs_parametric_impl_explicit_method_args.elisa", `
struct Box[T]:
    item: T

protocol Chooser:
    def choose[U](s: mutable Self&, value: U, first: bool) -> U

impl[T] Chooser for Box[T]:
    def choose[U](s: mutable Box[T]&, value: U, first: bool) -> U:
        if first:
            return value
        return value

def direct(b: mutable Box[i64]&) -> i32:
    return b.choose[i32](first: true, value: 7)
`)
}

func TestUFCSParametricImplMethodRejectsReadOnlyReceiver(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ufcs_parametric_impl_mutable_receiver.elisa", `
struct Box[T]:
    item: T

protocol Touch:
    def touch(s: mutable Self&) -> void

impl[T] Touch for Box[T]:
    def touch(s: mutable Box[T]&) -> void:
        return

def readonly(b: Box[i64]&) -> void:
    b.touch()
`, AnalyzeOptions{})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "expects mutable Box[i64]&") {
		t.Fatalf("expected concrete parametric impl dispatch to preserve the mutable receiver requirement, got:\n%s", joined)
	}
}

func TestUFCSParametricImplMethodEnforcesImplTypeParamBound(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ufcs_parametric_impl_bound.elisa", `
protocol IsMarked:
    def mark(s: Self) -> void

struct Marked:
    value: i64

impl IsMarked for Marked:
    def mark(s: Marked) -> void:
        return

struct Box[T]:
    item: T

protocol ReadValue:
    def read(s: Self&) -> i64

impl[T: IsMarked] ReadValue for Box[T]:
    def read(s: Box[T]&) -> i64:
        return 1

def allowed(b: Box[Marked]&) -> i64:
    return b.read()

def rejected(b: Box[i64]&) -> i64:
    return b.read()
`, AnalyzeOptions{})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "does not satisfy impl bound IsMarked") {
		t.Fatalf("expected a concrete receiver that violates the impl type-param bound to be rejected, got:\n%s", joined)
	}
}

func TestUFCSParametricImplMethodRemainsAmbiguousAcrossProtocols(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ufcs_parametric_impl_ambiguous.elisa", `
struct Box[T]:
    item: T

protocol First:
    def ping(s: Self) -> i64

protocol Second:
    def ping(s: Self) -> i64

impl[T] First for Box[T]:
    def ping(s: Box[T]) -> i64:
        return 1

impl[T] Second for Box[T]:
    def ping(s: Box[T]) -> i64:
        return 2

def ambiguous(b: Box[i64]) -> i64:
    return b.ping()
`, AnalyzeOptions{})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, `method "ping" on Box[i64] is ambiguous across multiple protocol impls`) {
		t.Fatalf("expected a concrete parametric-impl call to remain ambiguous, got:\n%s", joined)
	}
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
