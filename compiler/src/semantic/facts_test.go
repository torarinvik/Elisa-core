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
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, tc.got)
		}
	}
}
