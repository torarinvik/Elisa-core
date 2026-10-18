package incremental

import (
	"context"
	"fmt"
	"testing"
)

// Measures full snapshot publication for one changed cell in a growing retained
// world. Source construction and initial admission are outside the timer;
// evaluation and resolver work are deliberately excluded.
func BenchmarkSessionOneCellEditPublication(b *testing.B) {
	for _, count := range []int{64, 1024, 8192} {
		b.Run(fmt.Sprintf("queries=%d", count), func(b *testing.B) {
			ctx := context.Background()
			publications := make([]Publication, count)
			for i := range publications {
				publications[i] = readPublication(fmt.Sprint(i), "value", false)
			}
			owner, err := NewSession(map[string]Evaluator{"read": sessionRead}, DefaultLimits())
			if err != nil {
				b.Fatal(err)
			}
			initial, err := owner.Publish(ctx, publications)
			if err != nil || len(initial.Skipped) != 0 {
				b.Fatal(err, initial.Skipped)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%2 == 0 {
					publications[0].Inputs[0].Value = "edited"
				} else {
					publications[0].Inputs[0].Value = "value"
				}
				world, err := owner.Publish(ctx, publications)
				if err != nil || len(world.Skipped) != 0 {
					b.Fatal(err, world.Skipped)
				}
			}
			b.StopTimer()
			stats := owner.Stats()
			if stats.RetainedQueries != count || stats.RetainedInputs != count {
				b.Fatal(stats)
			}
			b.ReportMetric(float64(count), "world-queries")
			b.ReportMetric(1, "changed-cells/op")
		})
	}
}

// The same one-cell edit through owner deltas. The entire world is admitted once;
// each timed call replaces one small owner, independent of unrelated owners.
func BenchmarkSessionOneCellDeltaPublication(b *testing.B) {
	for _, count := range []int{64, 1024, 8192} {
		b.Run(fmt.Sprintf("queries=%d", count), func(b *testing.B) {
			ctx := context.Background()
			publications := make([]Publication, count)
			for i := range publications {
				publications[i] = readPublication(fmt.Sprint(i), "value", false)
			}
			owner, err := NewSession(map[string]Evaluator{"read": sessionRead}, DefaultLimits())
			if err != nil {
				b.Fatal(err)
			}
			if _, err := owner.PublishDelta(ctx, Delta{Upsert: publications}); err != nil {
				b.Fatal(err)
			}
			patch := Delta{Upsert: publications[:1]}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%2 == 0 {
					patch.Upsert[0].Inputs[0].Value = "edited"
				} else {
					patch.Upsert[0].Inputs[0].Value = "value"
				}
				if _, err := owner.PublishDelta(ctx, patch); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			stats := owner.Stats()
			if stats.RetainedQueries != count || stats.RetainedInputs != count {
				b.Fatal(stats)
			}
			b.ReportMetric(float64(count), "world-queries")
			b.ReportMetric(1, "changed-cells/op")
		})
	}
}
