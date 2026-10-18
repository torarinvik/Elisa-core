package incremental

import (
	"context"
	"fmt"
	"testing"
)

// Every revision invalidates the query. Repeated reads exercise deduplication,
// and the larger case guards the transition from linear scans to a hash set.
func BenchmarkQueryDependencyRecording(b *testing.B) {
	for _, count := range []int{1, 4, 8, 64} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			keys := make([]Key, count)
			for i := range keys {
				keys[i] = Key{"input", fmt.Sprint(i)}
			}
			db := New(map[string]Evaluator{"query": func(r *Reader, _ string) (string, error) {
				value := ""
				for repeat := 0; repeat < 2; repeat++ {
					for _, key := range keys {
						value, _ = r.Input(key)
					}
				}
				return value, nil
			}})
			for _, key := range keys {
				db.Apply(Change{Key: key, Value: "initial"})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				value := "even"
				if i%2 != 0 {
					value = "odd"
				}
				db.Apply(Change{Key: keys[0], Value: value})
				if _, err := db.Evaluate(context.Background(), Key{"query", "main"}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestDependencyRecordingAcrossSmallSetBoundary(t *testing.T) {
	for _, budget := range []int{0, 1, 8, 9, 64} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			r := &Reader{boundedDependencies: true, maxDependencies: budget}
			for i := 0; i < budget; i++ {
				dep := dependency{key: Key{"input", fmt.Sprint(i)}, query: i%2 == 0}
				if !r.remember(dep) || !r.remember(dep) {
					t.Fatal("unique and repeated reads must fit reservation")
				}
			}
			for _, dep := range append([]dependency(nil), r.dependencies...) {
				if !r.remember(dep) {
					t.Fatal("duplicate at full reservation rejected")
				}
			}
			if len(r.dependencies) != budget {
				t.Fatalf("edges=%d want %d", len(r.dependencies), budget)
			}
			if r.remember(dependency{key: Key{"extra", "overflow"}}) || r.failure != ErrDependencyBudget {
				t.Fatal("new edge must exceed reservation")
			}
		})
	}
}

func TestDependencySmallSetPreservesDomainsOrderAndPreflightBudget(t *testing.T) {
	for _, budget := range []int{1, 8, 9, 64} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			executions := 0
			db := New(map[string]Evaluator{"child": func(_ *Reader, _ string) (string, error) {
				executions++
				return "value", nil
			}})
			r := &Reader{db: db, context: context.Background(), boundedDependencies: true, maxDependencies: budget}
			for i := 0; i < budget; i++ {
				key := Key{"child", fmt.Sprint(i)}
				if _, err := r.Query(key); err != nil {
					t.Fatal(err)
				}
				if r.dependencies[i] != (dependency{key: key, query: true}) {
					t.Fatal("dependency read order changed")
				}
			}
			if (r.seen != nil) != (budget > smallDependencySet) {
				t.Fatal("hash set allocated on wrong side of threshold")
			}
			if _, err := r.Query(Key{"child", "0"}); err != nil || executions != budget {
				t.Fatal("duplicate child at full budget was rejected or reexecuted")
			}
			if _, err := r.Query(Key{"child", "overflow"}); err != ErrDependencyBudget || executions != budget {
				t.Fatal("over-budget child executed before admission")
			}
			// Input and query dependencies with identical keys remain distinct.
			r = &Reader{}
			key := Key{"child", "same"}
			if !r.remember(dependency{key: key}) || !r.remember(dependency{key: key, query: true}) || len(r.dependencies) != 2 {
				t.Fatal("input and query dependency domains collapsed")
			}
		})
	}
}
