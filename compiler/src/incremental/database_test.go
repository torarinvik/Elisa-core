package incremental

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireValue(t *testing.T, db *Database, key Key, want string) {
	t.Helper()
	got, err := db.Evaluate(context.Background(), key)
	if err != nil || got != want {
		t.Fatalf("%v: got %q, %v; want %q", key, got, err, want)
	}
}

func TestIndependentBodyEditAndMemo(t *testing.T) {
	db := New(map[string]Evaluator{
		"check": func(r *Reader, name string) (string, error) {
			body, _ := r.Input(Key{"body", name})
			signature, _ := r.Input(Key{"interface", name})
			return signature + ":" + body, nil
		},
		"package": func(r *Reader, _ string) (string, error) {
			a, err := r.Query(Key{"check", "a"})
			if err != nil {
				return "", err
			}
			b, err := r.Query(Key{"check", "b"})
			return a + ";" + b, err
		},
	})
	db.Apply(Change{Key: Key{"body", "a"}, Value: "return 1"}, Change{Key: Key{"body", "b"}, Value: "return 2"}, Change{Key: Key{"interface", "a"}, Value: "i64"}, Change{Key: Key{"interface", "b"}, Value: "i64"})
	root := Key{"package", "root"}
	requireValue(t, db, root, "i64:return 1;i64:return 2")
	before := db.Stats()
	requireValue(t, db, root, "i64:return 1;i64:return 2")
	if db.Stats().Executed != before.Executed {
		t.Fatal("no-op reevaluated a query")
	}
	db.Apply(Change{Key: Key{"body", "a"}, Value: "return 3"})
	requireValue(t, db, root, "i64:return 3;i64:return 2")
	if delta := db.Stats().Executed - before.Executed; delta != 2 {
		t.Fatalf("body edit executed %d queries, want changed body + package", delta)
	}
}

func TestUnchangedInterfacePrunesCallerInvalidation(t *testing.T) {
	db := New(map[string]Evaluator{
		"interface": func(r *Reader, name string) (string, error) {
			body, _ := r.Input(Key{"body", name})
			summary, _, _ := strings.Cut(body, "|")
			return summary, nil
		},
		"caller": func(r *Reader, _ string) (string, error) { return r.Query(Key{"interface", "callee"}) },
	})
	key := Key{"caller", "caller"}
	db.Apply(Change{Key: Key{"body", "callee"}, Value: "pure|return 1"})
	requireValue(t, db, key, "pure")
	before := db.Stats()
	db.Apply(Change{Key: Key{"body", "callee"}, Value: "pure|return 2"})
	requireValue(t, db, key, "pure")
	if delta := db.Stats().Executed - before.Executed; delta != 1 {
		t.Fatalf("unchanged interface reran caller: %d", delta)
	}
	before = db.Stats()
	db.Apply(Change{Key: Key{"body", "callee"}, Value: "Global.Write|counter <- 2"})
	requireValue(t, db, key, "Global.Write")
	if delta := db.Stats().Executed - before.Executed; delta != 2 {
		t.Fatalf("changed inferred effect did not rerun caller: %d", delta)
	}
}

func TestNegativeLookupDependencyAndRemoval(t *testing.T) {
	db := New(map[string]Evaluator{"lookup": func(r *Reader, name string) (string, error) {
		value, present := r.Input(Key{"overload-set", name})
		if !present {
			return "diagnostic: unknown function", nil
		}
		return "resolved:" + value, nil
	}})
	key := Key{"lookup", "work"}
	requireValue(t, db, key, "diagnostic: unknown function")
	before := db.Stats()
	requireValue(t, db, key, "diagnostic: unknown function")
	if db.Stats().Executed != before.Executed {
		t.Fatal("diagnostic result was not reusable")
	}
	db.Apply(Change{Key: Key{"overload-set", "work"}, Value: "(i64)->i64"})
	requireValue(t, db, key, "resolved:(i64)->i64")
	db.Apply(Change{Key: Key{"overload-set", "work"}, Value: "(i64)->i64;(u64)->u64"})
	requireValue(t, db, key, "resolved:(i64)->i64;(u64)->u64")
	db.Apply(Change{Key: Key{"overload-set", "work"}, Remove: true})
	requireValue(t, db, key, "diagnostic: unknown function")
	db.Apply(Change{Key: Key{"overload-set", "work"}, Value: ""})
	requireValue(t, db, key, "resolved:")
}

func TestChangingDependencySet(t *testing.T) {
	db := New(map[string]Evaluator{"choose": func(r *Reader, _ string) (string, error) {
		name, _ := r.Input(Key{"selector", "which"})
		value, _ := r.Input(Key{"value", name})
		return value, nil
	}})
	key := Key{"choose", "root"}
	db.Apply(Change{Key: Key{"selector", "which"}, Value: "a"}, Change{Key: Key{"value", "a"}, Value: "same"}, Change{Key: Key{"value", "b"}, Value: "same"})
	requireValue(t, db, key, "same")
	db.Apply(Change{Key: Key{"selector", "which"}, Value: "b"})
	requireValue(t, db, key, "same")
	before := db.Stats()
	db.Apply(Change{Key: Key{"value", "a"}, Value: "unused"})
	requireValue(t, db, key, "same")
	if db.Stats().Executed != before.Executed {
		t.Fatal("obsolete dependency retained")
	}
	db.Apply(Change{Key: Key{"value", "b"}, Value: "new"})
	requireValue(t, db, key, "new")
}

func TestCycleAndSuppressedFailureNeverPublish(t *testing.T) {
	db := New(map[string]Evaluator{"cycle": func(r *Reader, name string) (string, error) {
		stop, _ := r.Input(Key{"stop", name})
		if stop == "yes" {
			return "complete", nil
		}
		_, _ = r.Query(Key{"cycle", name})
		return "partial fallback", nil
	}})
	key := Key{"cycle", "function"}
	for i := 0; i < 2; i++ {
		_, err := db.Evaluate(context.Background(), key)
		var cycle *CycleError
		if !errors.As(err, &cycle) {
			t.Fatalf("ignored child error published partial result: %v", err)
		}
	}
	if db.Stats().Executed != 2 {
		t.Fatal("cycle result memoized")
	}
	db.Apply(Change{Key: Key{"stop", "function"}, Value: "yes"})
	requireValue(t, db, key, "complete")
}

func TestAtomicNoOpBatchAndOptionDependencies(t *testing.T) {
	db := New(map[string]Evaluator{"check": func(r *Reader, _ string) (string, error) {
		target, _ := r.Input(Key{"options", "target"})
		origin, _ := r.Input(Key{"origin", "runtime"})
		return target + ":" + origin, nil
	}})
	key := Key{"check", "function"}
	db.Apply(Change{Key: Key{"options", "target"}, Value: "native64"}, Change{Key: Key{"origin", "runtime"}, Value: "trusted canonical runtime"})
	requireValue(t, db, key, "native64:trusted canonical runtime")
	before := db.Stats()
	changed := db.Apply(Change{Key: Key{"options", "target"}, Value: "wasm32"}, Change{Key: Key{"options", "target"}, Value: "native64"})
	if changed {
		t.Fatal("reversed transaction was not a no-op")
	}
	requireValue(t, db, key, "native64:trusted canonical runtime")
	if db.Stats().Executed != before.Executed {
		t.Fatal("no-op batch invalidated result")
	}
	db.Apply(Change{Key: Key{"options", "target"}, Value: "wasm32"}, Change{Key: Key{"origin", "runtime"}, Value: "untrusted copy"})
	requireValue(t, db, key, "wasm32:untrusted copy")
}

func TestCancellationWhileWaitingForRevision(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	db := New(map[string]Evaluator{"slow": func(r *Reader, _ string) (string, error) { close(entered); <-release; return "value", nil }})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() { defer workers.Done(); _, _ = db.Evaluate(context.Background(), Key{"slow", "a"}) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := db.Evaluate(ctx, Key{"slow", "b"}); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Error("cancellation waited for another evaluator")
	}
	close(release)
	workers.Wait()
}

func BenchmarkIndependentFunctionBodyEdit(b *testing.B) {
	db := New(map[string]Evaluator{"check": func(r *Reader, name string) (string, error) {
		value, _ := r.Input(Key{"body", name})
		return value, nil
	}})
	keys := make([]Key, 4096)
	for i := range keys {
		keys[i] = Key{"check", string(rune(i))}
		db.Apply(Change{Key: Key{"body", keys[i].Name}, Value: "body"})
		_, _ = db.Evaluate(context.Background(), keys[i])
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		value := "changed"
		if i%2 == 0 {
			value = "body"
		}
		db.Apply(Change{Key: Key{"body", keys[0].Name}, Value: value})
		for _, key := range keys {
			_, _ = db.Evaluate(context.Background(), key)
		}
	}
}

// Differential edits compare a retained graph with a fresh graph at every step.
// This includes obsolete edges, diamonds, missing inputs, branches, and cycles.
func TestDatabaseHistoryMatchesFreshEvaluation(t *testing.T) {
	evaluators := map[string]Evaluator{"node": func(r *Reader, name string) (string, error) {
		body, present := r.Input(Key{"body", name})
		if !present {
			return "missing:" + name, nil
		}
		fields := strings.Fields(body)
		switch fields[0] {
		case "value":
			return fields[1], nil
		case "call":
			return r.Query(Key{"node", fields[1]})
		case "choose":
			flag, _ := r.Input(Key{"flag", name})
			target := fields[1]
			if flag == "right" {
				target = fields[2]
			}
			return r.Query(Key{"node", target})
		case "pair":
			left, err := r.Query(Key{"node", fields[1]})
			if err != nil {
				return "", err
			}
			right, err := r.Query(Key{"node", fields[2]})
			return left + ":" + right, err
		}
		return "", fmt.Errorf("invalid test instruction")
	}}
	for seed := int64(0); seed < 10; seed++ {
		random := rand.New(rand.NewSource(seed))
		db := New(evaluators)
		inputs := map[Key]string{}
		apply := func(change Change) {
			db.Apply(change)
			if change.Remove {
				delete(inputs, change.Key)
			} else {
				inputs[change.Key] = change.Value
			}
		}
		for i := 0; i < 12; i++ {
			apply(Change{Key: Key{"body", fmt.Sprint(i)}, Value: fmt.Sprintf("value %d", i)})
		}
		for step := 0; step < 200; step++ {
			name := fmt.Sprint(random.Intn(12))
			a, b := random.Intn(12), random.Intn(12)
			change := Change{Key: Key{"body", name}}
			switch random.Intn(6) {
			case 0:
				change.Value = fmt.Sprintf("value %d", random.Intn(8))
			case 1:
				change.Value = fmt.Sprintf("call %d", a)
			case 2:
				change.Value = fmt.Sprintf("choose %d %d", a, b)
			case 3:
				change.Value = fmt.Sprintf("pair %d %d", a, b)
			case 4:
				change.Remove = true
			case 5:
				change.Key = Key{"flag", name}
				change.Value = "right"
				if random.Intn(2) == 0 {
					change.Value = "left"
				}
			}
			apply(change)
			fresh := New(evaluators)
			batch := make([]Change, 0, len(inputs))
			for key, value := range inputs {
				batch = append(batch, Change{Key: key, Value: value})
			}
			fresh.Apply(batch...)
			for root := 0; root < 3; root++ {
				key := Key{"node", fmt.Sprint(random.Intn(12))}
				incrementalValue, incrementalError := db.Evaluate(context.Background(), key)
				freshValue, freshError := fresh.Evaluate(context.Background(), key)
				var first, second *CycleError
				if (incrementalError == nil) != (freshError == nil) || errors.As(incrementalError, &first) != errors.As(freshError, &second) || (incrementalError == nil && incrementalValue != freshValue) {
					t.Fatalf("seed=%d step=%d change=%+v root=%v: incremental=%q/%v fresh=%q/%v", seed, step, change, key, incrementalValue, incrementalError, freshValue, freshError)
				}
			}
		}
	}
}

func TestEvictionRevalidatesParentQueries(t *testing.T) {
	db := New(map[string]Evaluator{
		"leaf": func(r *Reader, name string) (string, error) { return "first", nil },
		"parent": func(r *Reader, name string) (string, error) {
			child, err := r.Query(Key{"leaf", name})
			return "parent:" + child, err
		},
	})
	parent := Key{"parent", "item"}
	leaf := Key{"leaf", "item"}
	before, err := db.Evaluate(context.Background(), parent)
	if err != nil || before != "parent:first" {
		t.Fatal(before, err)
	}
	// Explicit eviction is allowed for an unusable memo even without an input edit.
	if !db.EvictQuery(leaf) || db.EvictQuery(leaf) {
		t.Fatal("eviction status incorrect")
	}
	after, err := db.Evaluate(context.Background(), parent)
	if err != nil || after != "parent:first" {
		t.Fatal("parent reused an evicted child", after, err)
	}
	if db.Stats().Executed != 4 {
		t.Fatal("parent failed to revalidate its discarded child")
	}
}

func TestFailedRefreshPreservesEqualResultRevision(t *testing.T) {
	unavailable := errors.New("product needs refresh")
	parents := 0
	db := New(map[string]Evaluator{
		"child": func(r *Reader, name string) (string, error) {
			ready, _ := r.Input(Key{"ready", name})
			if ready != "yes" {
				return "", unavailable
			}
			return "same fact", nil
		},
		"parent": func(r *Reader, name string) (string, error) { parents++; return r.Query(Key{"child", name}) },
	})
	key := Key{"parent", "item"}
	input := Key{"ready", "item"}
	db.Apply(Change{Key: input, Value: "yes"})
	if _, err := db.Evaluate(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	db.Apply(Change{Key: input, Value: "no"})
	if _, err := db.Evaluate(context.Background(), key); !errors.Is(err, unavailable) {
		t.Fatal("incomplete child was published", err)
	}
	db.Apply(Change{Key: input, Value: "yes"})
	value, err := db.Evaluate(context.Background(), key)
	if err != nil || value != "same fact" || parents != 1 {
		t.Fatal("transient miss invalidated unchanged facts", value, err, parents)
	}
}
