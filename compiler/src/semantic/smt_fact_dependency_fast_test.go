package semantic

import "testing"

func TestAssertFactDependencyIntersectionMatchesUnifiedPredicate(t *testing.T) {
	cases := []struct{ deps, roots map[string]bool }{
		{nil, nil}, {map[string]bool{"x": true}, nil},
		{map[string]bool{"x": true}, map[string]bool{"x": true}},
		{map[string]bool{"x": false}, map[string]bool{"x": true}},
		{map[string]bool{"x": true}, map[string]bool{"x": false}},
	}
	for _, tc := range cases {
		fact := smtFact{Deps: tc.deps}
		if got, want := depsIntersect(fact.Deps, tc.roots), factInvalidatedBy(assertHypothesisFact{fact: fact}, tc.roots); got != want {
			t.Fatalf("intersection = %v, want %v", got, want)
		}
	}
}
