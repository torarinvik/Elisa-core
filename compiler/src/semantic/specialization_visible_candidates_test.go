//go:build cgo

package semantic

import (
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func specializationVisibleCandidateFixture(names int) *Analyzer {
	a := &Analyzer{currentNamespace: "App.Inner", currentUsings: []string{"Library", "Other"}, importAliases: map[string]string{"Thing": "Hidden.Thing"}, moduleAliases: map[string]string{"M": "Module"}, visibleAliasGen: 2}
	for _, namespace := range []string{"App.Inner", "Library", "Other"} {
		a.currentNamespace = namespace
		for i := 0; i < names; i++ {
			a.visibleNameCandidates(fmt.Sprintf("name%d", i))
		}
		a.visibleNameCandidates("Thing")
		a.visibleNameCandidates("M.value")
	}
	a.currentNamespace = "App.Inner"
	return a
}

func TestSpecializationRebuildsVisibleCandidateMemoPrivately(t *testing.T) {
	root := specializationVisibleCandidateFixture(32)
	before := deepCloneValue(reflect.ValueOf(root.visibleCandidates)).Interface().(visibleCandidateCache)
	private := cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	if !reflect.ValueOf(private.visibleCandidates).IsZero() {
		t.Fatal("private construction copied the whole-program candidate memo")
	}
	for _, namespace := range []string{"App.Inner", "Library", "New.Context", ""} {
		private.currentNamespace = namespace
		for _, name := range []string{"name3", "Thing", "M.value", "missing"} {
			want := private.buildVisibleNameCandidates(namespace, name)
			got := private.visibleNameCandidates(name)
			if !slices.Equal(got, want) {
				t.Fatalf("%s/%s: got%q want%q", namespace, name, got, want)
			}
			if len(got) != cap(got) {
				t.Fatal("candidate append can mutate memo backing")
			}
		}
	}
	if !reflect.DeepEqual(root.visibleCandidates, before) {
		t.Fatal("private lookup mutated canonical memo")
	}
}

func TestOrdinaryWorkerStillCopiesVisibleCandidateMemo(t *testing.T) {
	root := specializationVisibleCandidateFixture(8)
	worker := cloneAnalyzerValueForParallel(root).Addr().Interface().(*Analyzer)
	if !reflect.DeepEqual(root.visibleCandidates, worker.visibleCandidates) {
		t.Fatal("ordinary worker clone policy changed")
	}
	worker.visibleCandidates.byName["private-only"] = []string{"local"}
	if _, found := root.visibleCandidates.byName["private-only"]; found {
		t.Fatal("ordinary worker shares mutable candidate table")
	}
}

func BenchmarkSpecializationVisibleCandidateConstruction(b *testing.B) {
	root := specializationVisibleCandidateFixture(4096)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		specializationAnalysisCloneSink = cloneAnalyzerValueForSpecialization(root).Addr().Interface().(*Analyzer)
	}
}
