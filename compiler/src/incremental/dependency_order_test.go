package incremental

import (
	"context"
	"errors"
	"testing"
)

// A changed selector must invalidate the old path before validation attempts
// to force a declaration that has been deleted. A replacement with the same
// value must still update dependencies without executing its parent again.
func TestSelectorChangeAvoidsDeletedQueryAndPreservesParentMemo(t *testing.T) {
	retired := errors.New("selected declaration has been deleted")
	executions := map[string]int{}
	db := New(map[string]Evaluator{
		"decl": func(r *Reader, name string) (string, error) {
			executions[name]++
			value, present := r.Input(Key{"declaration", name})
			if !present {
				return "", retired
			}
			return value, nil
		},
		"select": func(r *Reader, _ string) (string, error) {
			executions["select"]++
			name, _ := r.Input(Key{"selector", "current"})
			return r.Query(Key{"decl", name})
		},
		"parent": func(r *Reader, _ string) (string, error) {
			executions["parent"]++
			return r.Query(Key{"select", "current"})
		},
	})
	root := Key{"parent", "root"}
	db.Apply(Change{Key: Key{"selector", "current"}, Value: "old"}, Change{Key: Key{"declaration", "old"}, Value: "same"}, Change{Key: Key{"declaration", "new"}, Value: "same"})
	requireValue(t, db, root, "same")
	db.Apply(Change{Key: Key{"selector", "current"}, Value: "new"}, Change{Key: Key{"declaration", "old"}, Remove: true})
	requireValue(t, db, root, "same")
	if executions["old"] != 1 || executions["new"] != 1 || executions["select"] != 2 || executions["parent"] != 1 {
		t.Fatalf("deleted branch forced or unchanged parent recomputed: %v", executions)
	}
	// A genuinely selected deleted declaration still fails; short-circuiting
	// obsolete dependencies must not hide errors on the current dependency path.
	db.Apply(Change{Key: Key{"selector", "current"}, Value: "old"})
	if _, err := db.Evaluate(context.Background(), root); !errors.Is(err, retired) {
		t.Fatalf("current deleted declaration error was hidden: %v", err)
	}
	db.Apply(Change{Key: Key{"declaration", "old"}, Value: "restored"})
	requireValue(t, db, root, "restored")
	if executions["parent"] != 2 {
		t.Fatalf("changed restored result failed to update parent: %v", executions)
	}
}
