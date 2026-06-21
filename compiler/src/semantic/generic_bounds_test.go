package semantic

import (
	"strings"
	"testing"
)

// A type conforming to the bound instantiates AND dispatches the bound's instance
// methods through the bare type-parameter receiver (`a.lt(b)` where `a: T`, `T: Comparable`).
func TestBoundedGenericInstanceMethodDispatch(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "bounded_generic_dispatch.elisa", `
struct Point:
    x: i64

protocol Comparable:
    type Item
    def lt(a: Item, b: Item) -> bool

impl Comparable for Point:
    type Item = Point
    def lt(a: Point, b: Point) -> bool:
        return a.x < b.x

def max_of[T: Comparable](a: T, b: T) -> T:
    if a.lt(b):
        return b
    return a

def use() -> Point:
    p: Point = Point{x: 1}
    q: Point = Point{x: 2}
    return max_of[Point](p, q)
`)
}

// A non-conforming type argument is rejected at instantiation via the protocol
// conformance checker.
func TestBoundedGenericNonConformingErrorsAtInstantiation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "bounded_generic_nonconf.elisa", `
struct Bare:
    y: i64

protocol Comparable:
    type Item
    def lt(a: Item, b: Item) -> bool

def max_of[T: Comparable](a: T, b: T) -> T:
    if a.lt(b):
        return b
    return a

def use() -> Bare:
    p: Bare = Bare{y: 1}
    q: Bare = Bare{y: 2}
    return max_of[Bare](p, q)
`, AnalyzeOptions{})
	joined := strings.Join(result.Errors(), "\n")
	if !strings.Contains(joined, "Comparable") {
		t.Fatalf("expected conformance error mentioning Comparable, got:\n%s", joined)
	}
}

// Unbounded generics are unaffected: ordinary type params still instantiate fine.
func TestUnboundedGenericUnaffected(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "unbounded_generic.elisa", `
def id[T](a: T) -> T:
    return a

def use() -> i64:
    return id[i64](7)
`)
}

// A method call on an UNBOUNDED type parameter must NOT resolve through any protocol;
// the bound-method rewrite is gated on a declared bound.
func TestUnboundedTypeParamMethodStillUnresolved(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unbounded_method.elisa", `
def f[T](a: T) -> T:
    return a.lt(a)
`, AnalyzeOptions{})
	if len(result.Errors()) == 0 {
		t.Fatalf("expected an error: a method call on an unbounded type parameter must not resolve")
	}
}
