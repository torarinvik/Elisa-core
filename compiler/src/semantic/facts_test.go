package semantic

import "testing"

func TestFactDiagnosticMessageVocabulary(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "local region escape",
			got:  localRegionEscapeMessage("value", "scratch"),
			want: `cannot return value: region dependency facts include local region "scratch"`,
		},
		{
			name: "thread local region",
			got:  threadLocalRegionDependencyMessage("spawn1", "scratch"),
			want: `argument to "spawn1" cannot cross thread boundary: region dependency facts include local region "scratch"`,
		},
		{
			name: "thread store rebase",
			got:  threadUnpublishedStoreDependencyMessage("pool_submit1", "Expr.Store[Local]"),
			want: `argument to "pool_submit1" cannot cross thread boundary: store dependency facts require rebase to frozen/public store, got "Expr.Store[Local]"`,
		},
		{
			name: "ensure widen",
			got:  ensurePreserveWidenedMessage("job", "finish"),
			want: `cannot prove ensures job => preserve on function "finish": target facts may have been widened conservatively by a call`,
		},
		{
			name: "ensure named mismatch",
			got:  ensureNamedStateMismatchMessage("job", "Ready", "finish", "ParseJob[Failed]"),
			want: `cannot prove ensures job => Ready on function "finish": current tracked facts are ParseJob[Failed]`,
		},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, tc.got)
		}
	}
}

func TestFormatFactTransforms(t *testing.T) {
	got := FormatFactTransforms([]FactTransform{
		{},
		{Kind: FactTransformProduce, Classes: []FactClass{FactRepresentation, FactStorage}, Target: "node", Source: "store", Reason: "node construction"},
		{Kind: FactTransformRebase, Classes: []FactClass{FactStoreDeps}, Target: "store", Reason: "freeze rebases store provenance"},
	})
	want := "[produce node [representation,storage] <- store (node construction); rebase store [store-deps] (freeze rebases store provenance)]"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	if got := FormatFactTransforms([]FactTransform{{}}); got != "" {
		t.Fatalf("expected empty transform list to format empty, got %q", got)
	}
}
