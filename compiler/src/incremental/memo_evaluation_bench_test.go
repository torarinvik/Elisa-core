package incremental

import (
	"context"
	"testing"
)

func BenchmarkEvaluateCurrentRevisionMemo(b *testing.B) {
	db := New(map[string]Evaluator{"body": func(r *Reader, name string) (string, error) {
		value, _ := r.Input(Key{"source", name})
		return value, nil
	}})
	key := Key{"body", "main"}
	db.Apply(Change{Key: Key{"source", "main"}, Value: "typed"})
	if _, err := db.Evaluate(context.Background(), key); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		value, err := db.Evaluate(context.Background(), key)
		if err != nil || value != "typed" {
			b.Fatal(value, err)
		}
	}
}

func TestMemoEvaluationHonorsCancellationAndNextRevision(t *testing.T) {
	db := New(map[string]Evaluator{"body": func(r *Reader, name string) (string, error) {
		value, _ := r.Input(Key{"source", name})
		return value, nil
	}})
	key := Key{"body", "main"}
	input := Key{"source", "main"}
	db.Apply(Change{Key: input, Value: "old"})
	requireValue(t, db, key, "old")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.Evaluate(ctx, key); err != context.Canceled {
		t.Fatalf("memo suppressed cancellation: %v", err)
	}
	requireValue(t, db, key, "old")
	db.Apply(Change{Key: input, Value: "new"})
	requireValue(t, db, key, "new")
}
