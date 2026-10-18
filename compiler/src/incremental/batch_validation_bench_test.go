package incremental

import (
	"context"
	"fmt"
	"testing"
)

func BenchmarkBatchValidationOneBodyEdit(b *testing.B) {
	db := New(map[string]Evaluator{"body": func(r *Reader, name string) (string, error) {
		value, _ := r.Input(Key{"source", name})
		return value, nil
	}})
	keys := make([]Key, 4096)
	changes := make([]Change, len(keys))
	for i := range keys {
		keys[i] = Key{"body", fmt.Sprint(i)}
		changes[i] = Change{Key: Key{"source", keys[i].Name}, Value: "initial"}
	}
	db.Apply(changes...)
	db.EvaluateBatch(context.Background(), keys)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		value := "even"
		if i%2 != 0 {
			value = "odd"
		}
		db.Apply(Change{Key: changes[0].Key, Value: value})
		results := db.EvaluateBatch(context.Background(), keys)
		if results[0].Err != nil || results[0].Value != value {
			b.Fatal("edited result missing")
		}
	}
}

func TestBatchCycleDoesNotPoisonFollowingRoots(t *testing.T) {
	db := New(map[string]Evaluator{"node": func(r *Reader, name string) (string, error) {
		if name == "leaf" {
			return "value", nil
		}
		if _, err := r.Query(Key{"node", "leaf"}); err != nil {
			return "", err
		}
		return r.Query(Key{"node", name})
	}})
	results := db.EvaluateBatch(context.Background(), []Key{{"node", "cycle"}, {"node", "leaf"}, {"node", "cycle"}, {"node", "leaf"}})
	for i, result := range results {
		if i%2 == 0 {
			if _, ok := result.Err.(*CycleError); !ok {
				t.Fatalf("root %d: expected cycle, got %v", i, result.Err)
			}
		} else if result.Err != nil || result.Value != "value" {
			t.Fatalf("root %d poisoned: %+v", i, result)
		}
	}
}
