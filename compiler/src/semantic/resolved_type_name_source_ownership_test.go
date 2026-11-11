package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestResolvedTypeNameSourceOwnershipKeepsContributionAmbiguitySeparate(t *testing.T) {
	cases := []struct {
		name      string
		location  resolvedTypeNameLocation
		ambiguous bool
		path      string
	}{
		{name: "unique", location: resolvedTypeNameLocation{firstPath: "$.Type"}, path: "$.Type"},
		{name: "missing path", ambiguous: true},
		{name: "several paths within owner", location: resolvedTypeNameLocation{firstPath: "$.Type", morePaths: map[string]bool{"$.Return": true}}, ambiguous: true},
		{name: "several owners", location: resolvedTypeNameLocation{firstPath: "$.Type", ambiguous: true}, ambiguous: true, path: "$.Type"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			expr := &ast.NamedType{Name: "Box"}
			locations := map[ast.TypeExpr]resolvedTypeNameLocation{expr: test.location}
			owners := resolvedTypeNameSourceOwners(locations)
			got := owners[expr]
			if got.sourceAmbiguous() != test.ambiguous || got.sourcePath() != test.path {
				t.Fatalf("source ambiguity/path = %v/%q, want %v/%q", got.sourceAmbiguous(), got.sourcePath(), test.ambiguous, test.path)
			}
			if locations[expr].ambiguous != test.location.ambiguous || len(locations[expr].morePaths) != len(test.location.morePaths) {
				t.Fatal("source ownership changed contribution locations")
			}
		})
	}
	if resolvedTypeNameSourceOwners(nil) == nil {
		t.Fatal("empty ownership map must accept later rows")
	}
}
