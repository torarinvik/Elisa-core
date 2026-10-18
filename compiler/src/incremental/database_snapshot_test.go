package incremental

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// Session publication already resolves conflicting/duplicate ownership inputs.
// Its direct-map path must preserve Apply's revisions, presence semantics and
// invalidation, including absent inputs read by a previously memoized query.
func TestSnapshotPublicationMatchesApplyHistory(t *testing.T) {
	evaluators := map[string]Evaluator{"read": func(r *Reader, name string) (string, error) {
		value, present := r.Input(Key{"input", name})
		return fmt.Sprintf("%t:%s", present, value), nil
	}}
	direct, reference := New(evaluators), New(evaluators)
	random := rand.New(rand.NewSource(42))
	for revision := 0; revision < 200; revision++ {
		inventory := map[Key]Change{}
		for i := 0; i < 32; i++ {
			key := Key{"input", fmt.Sprint(i)}
			inventory[key] = Change{Key: key, Value: fmt.Sprint(random.Intn(4)), Remove: random.Intn(4) == 0}
		}
		var changes []Change
		for _, change := range inventory {
			changes = append(changes, change)
		}
		if direct.applySnapshot(inventory) != reference.Apply(changes...) || direct.revision != reference.revision {
			t.Fatalf("revision %d: publication paths diverged", revision)
		}
		if direct.applySnapshot(inventory) || direct.applySnapshot(nil) {
			t.Fatal("identical or empty inventory advanced the revision")
		}
		// Caller-owned storage must not become the retained input inventory.
		clear(inventory)
		for i := 0; i < 32; i++ {
			key := Key{"read", fmt.Sprint(i)}
			want, err := reference.Evaluate(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			requireValue(t, direct, key, want)
		}
	}
}
