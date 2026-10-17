package semantic

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestCapabilityProjectionLargeShallowGraphs(t *testing.T) {
	scalar := &BuiltinType{Name: "i64"}
	makeGraph := func(mutable bool) Type {
		root := &StructType{Fields: map[string]Field{}}
		for i := 0; i < 500; i++ {
			root.Fields[fmt.Sprint(i)] = Field{Type: &OptionalType{Value: scalar}}
		}
		root.Fields["borrow"] = Field{Type: &RefType{Elem: scalar, Mutable: mutable}}
		return root
	}
	session := NewIncrementalAnalysisSession()
	for step, mutable := range []bool{false, false, true, true, false} {
		typ := makeGraph(mutable)
		if _, ok := capabilityTypeProjection(typ); !ok {
			t.Fatal("large shallow graph declined")
		}
		a := capabilityTestAnalyzer(session)
		before := session.Stats()
		want := a.typeContainsMutableBorrowCapability(typ, map[Type]bool{}, map[*StructType][][]Type{}, 0)
		if got := a.hasMutableBorrowCapability(typ); got != want || a.semanticLimitHits != 0 {
			t.Fatalf("step%d parity/cutoff: %v/%v", step, got, want)
		}
		if (step == 1 || step == 3 || step == 4) && session.Stats().CapabilityProductsReused <= before.CapabilityProductsReused {
			t.Fatal("fresh shallow graph not reused")
		}
	}
	// Adding a long branch must decline even when a different mutable branch
	// could short circuit the original unordered traversal before its cutoff.
	root := makeGraph(true).(*StructType)
	deep := Type(scalar)
	for i := 0; i < semanticTraversalDepthLimit+1; i++ {
		deep = &OptionalType{Value: deep}
	}
	root.Fields["deep"] = Field{Type: deep}
	if _, ok := capabilityTypeProjection(root); ok {
		t.Fatal("potential depth diagnostic hidden by shallow sibling")
	}
}

func TestCapabilityDepthGraphConservativeBound(t *testing.T) {
	random := rand.New(rand.NewSource(4418))
	for trial := 0; trial < 500; trial++ {
		g := capabilityDepthGraph{nodes: 2 + random.Intn(7)}
		adjacency := make([][]int, g.nodes)
		for a := 0; a < g.nodes; a++ {
			for b := 0; b < g.nodes; b++ {
				if random.Intn(5) == 0 {
					g.edges = append(g.edges, [2]int{a, b})
					adjacency[a] = append(adjacency[a], b)
				}
			}
		}
		// Exhaust every simple path and closing edge independently of Tarjan.
		actual := 0
		var walk func(int, int, uint64)
		walk = func(node, depth int, path uint64) {
			if depth > actual {
				actual = depth
			}
			bit := uint64(1) << node
			if path&bit != 0 {
				return
			}
			for _, child := range adjacency[node] {
				walk(child, depth+1, path|bit)
			}
		}
		for node := 0; node < g.nodes; node++ {
			walk(node, 0, 0)
		}
		for limit := 0; limit < g.nodes; limit++ {
			if g.withinDepth(limit) && actual > limit {
				t.Fatalf("trial%d admitted depth%d under%d: %v", trial, actual, limit, g.edges)
			}
		}
	}
	// A large DAG with a shared leaf should be judged by path depth, not width.
	wide := capabilityDepthGraph{nodes: 700}
	for i := 1; i < 699; i++ {
		wide.edges = append(wide.edges, [2]int{0, i}, [2]int{i, 699})
	}
	if !wide.withinDepth(3) {
		t.Fatal("wide DAG rejected")
	}
	chain := capabilityDepthGraph{nodes: 300}
	for i := 0; i < 299; i++ {
		chain.edges = append(chain.edges, [2]int{i, i + 1})
	}
	if chain.withinDepth(256) {
		t.Fatal("deep DAG admitted")
	}
	cycle := capabilityDepthGraph{nodes: 300}
	for i := 0; i < 300; i++ {
		cycle.edges = append(cycle.edges, [2]int{i, (i + 1) % 300})
	}
	if cycle.withinDepth(256) {
		t.Fatal("large recursive component admitted")
	}
}

func TestCapabilityProjectionSharedTailUnderLongPrefix(t *testing.T) {
	leaf := &BuiltinType{Name: "i64"}
	tail := Type(leaf)
	for i := 0; i < 128; i++ {
		tail = &OptionalType{Value: tail}
	}
	prefix := tail
	for i := 0; i < 130; i++ {
		prefix = &OptionalType{Value: prefix}
	}
	root := &StructType{Fields: map[string]Field{"a_short": {Type: tail}, "b_long": {Type: prefix}}}
	// Sorted projection first sees the tail through its short route. It must
	// still prove the later, longer route through that already visited tail.
	if _, ok := capabilityTypeProjection(root); ok {
		t.Fatal("shared tail hid a long alternative path")
	}
	// A short recursive component reached by many siblings remains admissible.
	a := &StructType{Fields: map[string]Field{}}
	b := &OptionalType{Value: a}
	a.Fields["back"] = Field{Type: b}
	shallow := &StructType{Fields: map[string]Field{}}
	for i := 0; i < 400; i++ {
		shallow.Fields[fmt.Sprint(i)] = Field{Type: &OptionalType{Value: a}}
	}
	if _, ok := capabilityTypeProjection(shallow); !ok {
		t.Fatal("small shared recursive component declined")
	}
}
