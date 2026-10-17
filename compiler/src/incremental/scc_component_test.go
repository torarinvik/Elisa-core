package incremental

import (
	"context"
	"testing"
)

func TestSCCComponentPrunesUnrelatedInventoryEdits(t *testing.T) {
	graph := map[string][]string{"b": {"c"}, "c": {"b", "z"}, "z": {}}
	component, err := SCCComponentPublication("module", "b")
	if err != nil {
		t.Fatal(err)
	}
	parent := Key{Kind: "component-user", Name: "b"}
	evaluators := SCCEvaluators()
	runs := 0
	evaluators[parent.Kind] = func(r *Reader, _ string) (string, error) { runs++; return r.Query(component.Key) }
	owner, err := NewSession(evaluators, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	publish := func() Revision {
		t.Helper()
		plan, err := SCCPublication("module", graph)
		if err != nil {
			t.Fatal(err)
		}
		world, err := owner.Publish(context.Background(), []Publication{plan, component, {Key: parent, MaxDependencies: 1, MaxResultBytes: sccComponentMaxBytes}})
		if err != nil || len(world.Skipped) != 0 {
			t.Fatal(err, world.Skipped)
		}
		return world.Revision
	}
	original, err := publish().Evaluate(context.Background(), parent)
	if err != nil {
		t.Fatal(err)
	}
	graph["a"] = []string{} // Shifts every numeric component ID.
	same, err := publish().Evaluate(context.Background(), parent)
	if err != nil || same != original || runs != 1 {
		t.Fatal("unrelated node invalidated component user", err, runs)
	}
	graph["c"] = []string{"z"} // Split recursive group.
	changed, err := publish().Evaluate(context.Background(), parent)
	if err != nil || changed == original || runs != 2 {
		t.Fatal("cycle split missed", err, runs)
	}
	delete(graph, "b")
	if _, err := publish().Evaluate(context.Background(), parent); err == nil {
		t.Fatal("removed member reused")
	}
	graph["b"] = []string{"c"}
	if _, err := publish().Evaluate(context.Background(), parent); err != nil {
		t.Fatal("restore failed", err)
	}
	graph["c"] = []string{"b", "z"}
	if _, err := publish().Evaluate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	graph["z"] = []string{"a"} // Dependency's outgoing edges do not change group membership.
	before := runs
	if _, err := publish().Evaluate(context.Background(), parent); err != nil || runs != before {
		t.Fatal("transitive topology leaked into direct projection", err)
	}
	graph["z"] = []string{"b"} // Merge dependency into recursive group.
	if _, err := publish().Evaluate(context.Background(), parent); err != nil || runs != before+1 {
		t.Fatal("dependency merge missed", err)
	}
}
