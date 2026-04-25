package semantic

import (
	"strings"
	"testing"
)

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
			got:  ensurePreserveWidenedMessage("job", "finish", "unknown_update(job)"),
			want: `cannot prove ensures job => preserve on function "finish": target facts may have been widened conservatively by a call at "unknown_update(job)"`,
		},
		{
			name: "ensure named mismatch",
			got:  ensureNamedStateMismatchMessage("job", "Ready", "finish", "ParseJob[Failed]"),
			want: `cannot prove ensures job => Ready on function "finish": current tracked facts are ParseJob[Failed]`,
		},
		{
			name: "interface conformance",
			got:  interfaceConformanceFactMessage("Plain", "Builder", "call to \"build\""),
			want: `type "Plain" does not satisfy required interface fact "Builder" for call to "build"`,
		},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Fatalf("%s: expected %q, got %q", tc.name, tc.want, tc.got)
		}
	}
}

func TestFormatFactSnapshotIncludesPathAndHandleFacts(t *testing.T) {
	snapshot := FactSnapshot{
		Parameters:      []string{"player"},
		Widened:         []string{"player.health"},
		PathFacts:       []FactPath{{Target: "player.health", Root: "player", Path: "health", Steps: []FactPathStep{{Kind: "field", Name: "health"}}}},
		AliasClasses:    []string{"alias-class#0"},
		HandleStoreDeps: []string{"node<-store"},
		RegionDeps:      []string{"scratch[2->1]"},
	}
	got := FormatFactSnapshot(snapshot)
	for _, want := range []string{"params=[player]", "widened=[player.health]", "path_facts=[player.health{root=player,path=health,steps=field:health}]", "alias_classes=[alias-class#0]", "handle_store_deps=[node<-store]", "region_deps=[scratch[2->1]]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected snapshot %q to contain %q", got, want)
		}
	}
}

func TestFormatFactTraceContractAndEffectSummary(t *testing.T) {
	if got := FormatFactTraceContract(); !strings.Contains(got, FactTraceFormatVersion) || !strings.Contains(got, "filters=") {
		t.Fatalf("expected trace contract to include version and filters, got %q", got)
	}
	got := FormatFactEffectSummary(FactEffectSummary{Required: []string{"Memory.Allocate", "Abort.Panic"}, Provided: []string{"Console.Write"}})
	for _, want := range []string{"required=[Abort.Panic, Memory.Allocate]", "provided=[Console.Write]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected effect summary %q to contain %q", got, want)
		}
	}
}

func TestFormatFactExplanationsDescribesWidening(t *testing.T) {
	got := ExplainFactTransform(FactTransform{Kind: FactTransformWiden, Classes: []FactClass{FactTypestate}, Target: "player", Source: "unknown_update(player)", Details: []FactTransformDetail{{Name: "before", Value: "Player[Alive]"}, {Name: "after", Value: "Player[*]"}}, Reason: "ref call without matching ensures"})
	want := "widen player from Player[Alive] to Player[*] after unknown_update(player) because ref call without matching ensures"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
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
