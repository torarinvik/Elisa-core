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
			r := &Reader{seen: map[dependency]bool{}, boundedDependencies: true, maxDependencies: budget}
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
