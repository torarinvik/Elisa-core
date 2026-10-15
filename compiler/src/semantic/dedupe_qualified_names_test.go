package semantic

import (
	"fmt"
	"slices"
	"testing"
)

func dedupeQualifiedNamesReference(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// The allocation-free small-list path returns exactly what the map-based dedupe did: the first
// occurrence of each non-empty name, in order, and nil for an all-empty input.
func TestDedupeQualifiedNamesMatchesMapReference(t *testing.T) {
	inputs := [][]string{
		nil, {}, {""}, {"", ""}, {"a"}, {"a", "a"}, {"A.x", "x", "A.x", "", "B.x", "x"},
	}
	for n := 30; n <= 36; n++ { // straddle the small-list threshold
		var v []string
		for i := 0; i < n; i++ {
			v = append(v, fmt.Sprintf("N%d.x", i%(n/2)))
		}
		inputs = append(inputs, v)
	}
	for _, in := range inputs {
		got, want := dedupeQualifiedNames(in), dedupeQualifiedNamesReference(in)
		if !slices.Equal(got, want) || (got == nil) != (want == nil) {
			t.Fatalf("dedupe(%q) = %q, want %q", in, got, want)
		}
	}
}
