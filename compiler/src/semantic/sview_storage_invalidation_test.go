package semantic

import (
	"strings"
	"testing"
)

func TestSViewOfDArrayIsInvalidatedAfterRelocatingPush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_after_darray_push.elisa", `def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			view: sview = values.as_sview()
			values.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected growing the backing darray to invalidate its sview, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewDependencyFollowsMutableContainerAlias(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_after_alias_push.elisa", `def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			view: sview = values.as_sview()
			alias: mutable darray[u8]& = &values
			alias.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected growth through a mutable alias to invalidate its sview, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewOfStableReserveCommitBufferSurvivesGrowth(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_stable_reserve_commit.elisa", `def read() -> char:
	can Memory.Allocate, Abort.Panic:
		region storage(4096) using reserve_commit:
			values: mutable darray[u8] @storage = []
			values.push(65)
			view: sview = values.as_sview()
			values.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected reserve_commit backing to keep the sview pointer stable, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewDependenciesJoinAcrossBranches(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_branch_dependencies.elisa", `def read(owner: mutable Arena&, choose_left: bool) -> char:
	can Memory.Allocate, Abort.Panic:
		left: mutable darray[u8] = []
		right: mutable darray[u8] = []
		in owner:
			left.push(65)
			right.push(66)
			view: mutable sview = ""
			if choose_left:
				view <- left.as_sview()
			else:
				view <- right.as_sview()
			left.push(67)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected branch-joined sview to retain every possible backing dependency, got:\n%s", allDiagnostics(result))
	}
}

func TestTupleOfSViewsRetainsEveryBackingDependency(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "tuple_sview_dependencies.elisa", `def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic:
		left: mutable darray[u8] = []
		right: mutable darray[u8] = []
		in owner:
			left.push(65)
			right.push(66)
			views: (left: sview, right: sview) = (left.as_sview(), right.as_sview())
			left.push(67)
			return views.left[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected tuple storage to retain its sview fields' backing dependencies, got:\n%s", allDiagnostics(result))
	}
}
