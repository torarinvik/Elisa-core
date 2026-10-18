package incremental

import (
	"context"
	"fmt"
	"testing"
)

func TestDatabaseTransactionOneRevisionAndInvalidation(t *testing.T) {
	calls := 0
	db := New(map[string]Evaluator{
		"leaf": func(r *Reader, name string) (string, error) {
			calls++
			v, present := r.Input(Key{"input", name})
			return fmt.Sprintf("%t:%s", present, v), nil
		},
		"parent": func(r *Reader, name string) (string, error) { return r.Query(Key{"leaf", name}) },
	})
	key := Key{"input", "x"}
	parent := Key{"parent", "x"}
	leaf := Key{"leaf", "x"}
	db.Apply(Change{Key: key, Value: "old"})
	requireValue(t, db, parent, "true:old")
	before := db.revision
	changes := map[Key]Change{key: {Key: key, Value: "new"}}
	if !db.applyTransaction(changes, nil, []Key{leaf}) || db.revision != before+1 {
		t.Fatal("commit must advance once")
	}
	clear(changes)
	requireValue(t, db, parent, "true:new")
	if calls != 2 {
		t.Fatal("parent reused evicted/stale child", calls)
	}
	before = db.revision
	if db.applyTransaction(map[Key]Change{key: {Key: key, Value: "new"}}, nil, []Key{{"leaf", "missing"}}) || db.revision != before {
		t.Fatal("identical delta advanced revision")
	}
	if !db.applyTransaction(nil, []Key{key}, []Key{leaf}) || db.revision != before+1 {
		t.Fatal("retirement/eviction must advance once")
	}
	requireValue(t, db, parent, "false:")
	// Re-retirement must invalidate a memo that observed an absent input too.
	before = db.revision
	if !db.applyTransaction(nil, []Key{key}, nil) || db.revision != before+1 {
		t.Fatal("negative retirement did not invalidate")
	}
	if _, err := db.Evaluate(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatal("negative reader not re-established", calls)
	}
}
