package semantic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A view stored into an outer holder inside a loop whose body grows the view's backing dangles
// on the next iteration, whether the store is a direct `held.push(v)` or a callee handed the
// holder by mutable reference (store-flow summary). The .pos fixture keeps the holder legal when
// the backing is not mutated in the loop or the callee never stores the view.
func TestLoopHeldViewNegative(t *testing.T) {
	dir := filepath.Join("testdata", "loop_held_view")
	expect, err := os.ReadFile(filepath.Join(dir, "EXPECT"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(expect)), "\n") {
		name, want, _ := strings.Cut(line, "\t")
		source, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
		if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, want) {
			t.Errorf("%s: want %q, got:\n%s", name, want, got)
		}
	}
}

func TestLoopHeldViewPositive(t *testing.T) {
	name := "loop_held_view.pos.elisa"
	source, err := os.ReadFile(filepath.Join("testdata", "loop_held_view", name))
	if err != nil {
		t.Fatal(err)
	}
	result := analyzeTreeTestSourceWithSemanticErrors(t, name, string(source))
	for _, e := range result.Errors() {
		t.Errorf("%s rejected: %s", name, e)
	}
}
