package incremental

import (
	"context"
	"fmt"
	"testing"
)

func TestSessionDuplicatePublicationInputsAcrossSmallSetBoundary(t *testing.T) {
	for _, n := range []int{1, 15, 16, 17, 100} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			owner := smallSession(t, map[string]Evaluator{"read": sessionRead}, DefaultLimits())
			p := readPublication("q", "value", false)
			for i := 1; i < n; i++ {
				p.Inputs = append(p.Inputs, Change{Key: Key{"input", fmt.Sprint(i)}, Value: "value"})
			}
			p.Inputs = append(p.Inputs, p.Inputs[0])
			reversed := p
			reversed.Inputs = append([]Change(nil), p.Inputs...)
			for i, j := 0, len(reversed.Inputs)-1; i < j; i, j = i+1, j-1 {
				reversed.Inputs[i], reversed.Inputs[j] = reversed.Inputs[j], reversed.Inputs[i]
			}
			world, err := owner.Publish(context.Background(), []Publication{p, reversed})
			if err != nil {
				t.Fatal(err)
			}
			sessionValue(t, world.Revision, p.Key, "value")
			if owner.Stats().InputEdges != n {
				t.Fatal("duplicate changed reservation", owner.Stats())
			}
			p.Inputs[len(p.Inputs)-1].Value = "conflict"
			if _, err := owner.Publish(context.Background(), []Publication{p}); err == nil {
				t.Fatal("conflict admitted")
			}
			if _, err := world.Revision.Evaluate(context.Background(), p.Key); err == nil {
				t.Fatal("rejected generation stayed live")
			}
		})
	}
}

func BenchmarkSessionSmallInputPublication(b *testing.B) { benchmarkSessionInputPublication(b, 4) }
func BenchmarkSessionLargeInputPublication(b *testing.B) { benchmarkSessionInputPublication(b, 32) }

func benchmarkSessionInputPublication(b *testing.B, count int) {
	publications := make([]Publication, 1024)
	for i := range publications {
		name := fmt.Sprint(i)
		publications[i] = readPublication(name, "value", false)
		for j := 0; j < count-1; j++ {
			publications[i].Inputs = append(publications[i].Inputs, Change{Key: Key{"extra", fmt.Sprintf("%d/%d", i, j)}, Value: "value"})
		}
	}
	owner, err := NewSession(map[string]Evaluator{"read": sessionRead}, DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := owner.Publish(context.Background(), publications); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		world, err := owner.Publish(context.Background(), publications)
		if err != nil || len(world.Skipped) != 0 {
			b.Fatal(err, world.Skipped)
		}
	}
}
