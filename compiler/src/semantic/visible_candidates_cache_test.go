package semantic

import (
	"slices"
	"testing"

	"elisacore/src/ast"
)

// The memoized candidate list equals a fresh build in every context, is shared across repeated
// lookups, and follows changes to the namespace, the usings and the alias maps.
func TestVisibleNameCandidatesMemoized(t *testing.T) {
	a := &Analyzer{currentNamespace: "App.Core", currentUsings: []string{"Std", "Util"}}
	first := a.visibleNameCandidates("foo")
	want := []string{"App.Core.foo", "App.foo", "Std.foo", "Util.foo", "foo"}
	if !slices.Equal(first, want) {
		t.Fatalf("candidates = %v, want %v", first, want)
	}
	if again := a.visibleNameCandidates("foo"); &again[0] != &first[0] {
		t.Fatal("a repeated lookup in the same context must reuse the memoized list")
	}
	if cap(first) != len(first) {
		t.Fatalf("memoized list must have cap == len so appends copy; len=%d cap=%d", len(first), cap(first))
	}
	_ = append(first, "Extra.__init__")
	if got := a.visibleNameCandidates("foo"); !slices.Equal(got, want) {
		t.Fatalf("a caller's append leaked into the memo: %v", got)
	}

	// Context switch: different usings (fresh slice, as the analyzer assigns them).
	a.currentUsings = []string{"Other"}
	if got := a.visibleNameCandidates("foo"); !slices.Equal(got, []string{"App.Core.foo", "App.foo", "Other.foo", "foo"}) {
		t.Fatalf("usings change not reflected: %v", got)
	}
	a.currentNamespace = ""
	if got := a.visibleNameCandidates("foo"); !slices.Equal(got, []string{"Other.foo", "foo"}) {
		t.Fatalf("namespace change not reflected: %v", got)
	}

	// Alias registration after the list was memoized must invalidate it.
	a.registerUsingMember(&ast.UsingDecl{Name: "Lib", Member: "foo"}, "")
	if got := a.visibleNameCandidates("foo"); !slices.Contains(got, "Lib.foo") {
		t.Fatalf("import alias registered after memoization not reflected: %v", got)
	}
	before := a.visibleNameCandidates("M.bar")
	a.registerModuleAlias(&ast.UsingDecl{Name: "Mod", Alias: "M"}, "")
	if got := a.visibleNameCandidates("M.bar"); slices.Equal(got, before) || !slices.Contains(got, "Mod.bar") {
		t.Fatalf("module alias registered after memoization not reflected: before %v, after %v", before, got)
	}
	// Returning to the earlier context gives the same answer as before.
	a.currentNamespace, a.currentUsings = "App.Core", []string{"Std", "Util"}
	if got := a.visibleNameCandidates("bar"); !slices.Equal(got, a.buildVisibleNameCandidates("App.Core", "bar")) {
		t.Fatalf("memo disagrees with a fresh build: %v", got)
	}
}
