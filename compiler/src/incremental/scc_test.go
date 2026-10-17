package incremental

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func evaluateSCCFixture(t *testing.T, graph map[string][]string) SCCPlan {
	t.Helper()
	p, err := SCCPublication("module", graph)
	if err != nil {
		t.Fatal(err)
	}
	db := New(SCCEvaluators())
	db.Apply(p.Inputs...)
	encoded, err := db.Evaluate(context.Background(), p.Key)
	if err != nil {
		t.Fatal(err)
	}
	var plan SCCPlan
	if err = json.Unmarshal([]byte(encoded), &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}
func TestSCCPlanCyclesAndDependencyOrder(t *testing.T) {
	graph := map[string][]string{"a": {"b"}, "b": {"a", "c"}, "c": {}, "unrelated": {}}
	got := evaluateSCCFixture(t, graph)
	want := SCCPlan{Components: [][]string{{"a", "b"}, {"c"}, {"unrelated"}}, Dependencies: [][]int{{1}, {}, {}}, Order: []int{1, 0, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got%+v want%+v", got, want)
	}
	empty := evaluateSCCFixture(t, map[string][]string{})
	if len(empty.Components) != 0 || len(empty.Order) != 0 {
		t.Fatal("empty graph not empty")
	}
}
func TestSCCRandomGraphsMatchReachability(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		random := rand.New(rand.NewSource(seed))
		const n = 16
		graph := map[string][]string{}
		reach := make([][]bool, n)
		for i := 0; i < n; i++ {
			name := fmt.Sprintf("n%02d", i)
			graph[name] = []string{}
			reach[i] = make([]bool, n)
			reach[i][i] = true
			for j := 0; j < n; j++ {
				if random.Intn(5) == 0 {
					graph[name] = append(graph[name], fmt.Sprintf("n%02d", j))
					reach[i][j] = true
				}
			}
		}
		for k := 0; k < n; k++ {
			for i := 0; i < n; i++ {
				for j := 0; j < n; j++ {
					reach[i][j] = reach[i][j] || (reach[i][k] && reach[k][j])
				}
			}
		}
		plan := evaluateSCCFixture(t, graph)
		components := map[string]int{}
		for c, names := range plan.Components {
			for _, name := range names {
				components[name] = c
			}
		}
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				same := components[fmt.Sprintf("n%02d", i)] == components[fmt.Sprintf("n%02d", j)]
				if same != (reach[i][j] && reach[j][i]) {
					t.Fatalf("seed%d invalid SCC partition", seed)
				}
			}
		}
		position := make([]int, len(plan.Order))
		for i, c := range plan.Order {
			position[c] = i
		}
		for c, deps := range plan.Dependencies {
			for _, dep := range deps {
				if position[dep] >= position[c] {
					t.Fatalf("seed%d nondependency-first order", seed)
				}
			}
		}
	}
}
func TestSCCRetainedPlanMutationAndEqualOutputPruning(t *testing.T) {
	graph := map[string][]string{"a": {"b"}, "b": {"a", "c"}, "c": {"a"}}
	parent := Key{Kind: "effect-group-user", Name: "module"}
	runs := 0
	registry := SCCEvaluators()
	registry[parent.Kind] = func(r *Reader, _ string) (string, error) {
		runs++
		return r.Query(Key{Kind: SCCQueryKind, Name: "module"})
	}
	owner, err := NewSession(registry, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	publish := func() Revision {
		t.Helper()
		p, err := SCCPublication("module", graph)
		if err != nil {
			t.Fatal(err)
		}
		world, err := owner.Publish(context.Background(), []Publication{p, {Key: parent, MaxDependencies: 1, MaxResultBytes: 4096}})
		if err != nil || len(world.Skipped) != 0 {
			t.Fatal(err, world.Skipped)
		}
		return world.Revision
	}
	first := publish()
	original, err := first.Evaluate(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	before := owner.Stats().Queries.Executed
	next := publish()
	if _, err := next.Evaluate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if owner.Stats().Queries.Executed != before || runs != 1 {
		t.Fatal("unchanged graph re-executed")
	}
	// Removing redundant internal edge b->a leaves the same SCC output.
	graph["b"] = []string{"c"}
	next = publish()
	same, err := next.Evaluate(context.Background(), parent)
	if err != nil || same != original || runs != 1 {
		t.Fatal("equal plan output did not prune parent", err)
	}
	// Break the cycle and force a different SCC partition.
	graph["c"] = []string{}
	next = publish()
	different, err := next.Evaluate(context.Background(), parent)
	if err != nil || different == original || runs != 2 {
		t.Fatal("cycle split did not invalidate user", err)
	}
	if _, err := first.Evaluate(context.Background(), parent); err == nil {
		t.Fatal("old graph revision remained live")
	}
	delete(graph, "c")
	graph["b"] = []string{}
	next = publish()
	if _, err := next.Evaluate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
}
func TestSCCRejectsMissingAuthorityAndRecovers(t *testing.T) {
	p, err := SCCPublication("module", map[string][]string{"a": {"b"}, "b": {}})
	if err != nil {
		t.Fatal(err)
	}
	db := New(SCCEvaluators())
	db.Apply(p.Inputs...)
	if _, err := db.Evaluate(context.Background(), p.Key); err != nil {
		t.Fatal(err)
	}
	db.Apply(Change{Key: sccEdges("module", "b"), Remove: true})
	if _, err := db.Evaluate(context.Background(), p.Key); err == nil {
		t.Fatal("missing adjacency reused old SCC")
	}
	db.Apply(p.Inputs...)
	if _, err := db.Evaluate(context.Background(), p.Key); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string][]string{{"a": {"missing"}}, {"\xff": {}}, {"a": make([]string, sccMaxEdges+1)}} {
		if _, err := SCCPublication("module", bad); err == nil {
			t.Fatal("invalid graph admitted")
		}
	}
	if _, err := SCCPublication(strings.Repeat("x", 65537), map[string][]string{}); err == nil {
		t.Fatal("oversized identity admitted")
	}
}
func TestSCCLongChainDoesNotRecurse(t *testing.T) {
	graph := map[string][]string{}
	for i := 0; i < sccMaxNodes; i++ {
		name := fmt.Sprintf("n%04d", i)
		graph[name] = []string{}
		if i > 0 {
			graph[name] = []string{fmt.Sprintf("n%04d", i-1)}
		}
	}
	plan := evaluateSCCFixture(t, graph)
	if len(plan.Components) != sccMaxNodes || len(plan.Order) != sccMaxNodes {
		t.Fatal("long chain truncated")
	}
}
