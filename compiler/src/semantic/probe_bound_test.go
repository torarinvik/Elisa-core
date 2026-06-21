package semantic

import "testing"

func TestProbeBoundedStaticForm(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "probe_bound_static.elisa", `
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
    if T.lt(a, b):
        return b
    return a

def use() -> Point:
    p: Point = Point{x: 1}
    q: Point = Point{x: 2}
    return max_of[Point](p, q)
`)
	for _, e := range result.Errors() {
		t.Logf("ERR: %v", e)
	}
	if len(result.Errors()) != 0 {
		t.Fatalf("got %d errors", len(result.Errors()))
	}
}
