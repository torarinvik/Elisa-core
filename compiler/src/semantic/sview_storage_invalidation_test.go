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

func TestSViewOfDArrayIsInvalidatedWhenCopiedAliasGrows(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_after_copied_darray_push.elisa", `def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			alias: mutable darray[u8] = values
			view: sview = values.as_sview()
			alias.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected growth through a shallow darray copy to invalidate the original sview, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewOfCopiedDArrayIsInvalidatedWhenOriginalGrows(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_after_original_darray_push.elisa", `def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			alias: mutable darray[u8] = values
			view: sview = alias.as_sview()
			values.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected growth through the original darray to invalidate an alias-derived sview, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewOfCopiedDArrayTracksAliasChains(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_after_chained_darray_alias_push.elisa", `def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			first: mutable darray[u8] = values
			second: mutable darray[u8] = first
			view: sview = second.as_sview()
			values.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected chained darray aliases to preserve storage provenance, got:\n%s", allDiagnostics(result))
	}
}

func TestCopiedDArrayAliasRemainsUsableAfterGrowth(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_copy_alias_remains_usable.elisa", `def count(owner: mutable Arena&) -> usize:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			alias: mutable darray[u8] = values
			alias.push(66)
			return alias.count
	return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("growing a copied darray must not invalidate the darray alias itself, got:\n%s", allDiagnostics(result))
	}
}

func TestIndependentEmptyDArrayGrowthDoesNotInvalidateView(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_independent_empty_darrays.elisa", `def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			view: sview = values.as_sview()
			other: mutable darray[u8] = []
			other.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("growing an independent empty darray must not invalidate the view, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewCapturedByLambdaIsInvalidatedAfterBackingGrowth(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_closure_after_darray_push.elisa", `def read_first(view: sview) -> usize:
	return view.len

def read(owner: mutable Arena&) -> usize:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			view: sview = values.as_sview()
			reader: fn() -> usize = fn() => read_first(view)
			values.push(66)
			return reader()
	return 0
`, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(result), "storage dependency facts were invalidated") {
		t.Fatalf("expected backing growth to invalidate an sview captured by a live closure, got:\n%s", allDiagnostics(result))
	}
}

func TestDArrayCapturedByLambdaIsPinnedAgainstRelocatingGrowth(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_closure_after_growth.elisa", `def read(owner: mutable Arena&) -> usize:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		in owner:
			values.push(65)
			reader: fn() -> usize = fn() => values.as_sview().len
			values.push(66)
			return reader()
	return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected growth to invalidate a closure that can derive a view from its captured darray, got:\n%s", allDiagnostics(result))
	}
}

func TestMutatingScalarCaptureDoesNotInvalidateLambda(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "scalar_closure_mutation.elisa", `def read() -> i64:
	value: mutable i64 = 1
	reader: fn() -> i64 = fn() => value
	value <- value + 1
	return reader()
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if diagnostics := allDiagnostics(result); diagnostics != "" {
		t.Fatalf("scalar mutation must not invalidate a closure's unrelated storage dependencies, got:\n%s", diagnostics)
	}
}

func TestSViewCopiedDArrayAliasDependenciesJoinAcrossBranches(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_branch_joined_darray_alias.elisa", `def read(owner: mutable Arena&, choose_values: bool) -> char:
	can Memory.Allocate, Abort.Panic:
		values: mutable darray[u8] = []
		other: mutable darray[u8] = []
		in owner:
			values.push(65)
			other.push(66)
			alias: mutable darray[u8] = []
			if choose_values:
				alias <- values
			else:
				alias <- other
			view: sview = values.as_sview()
			alias.push(67)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected the branch-joined alias set to retain every possible backing, got:\n%s", allDiagnostics(result))
	}
}

func TestStorageViewSourcesOverlapAggregateFieldsThroughTheirRoot(t *testing.T) {
	if !storageViewSourcesOverlap("parser.source", "parser.tokens") {
		t.Fatal("expected sibling aggregate fields to conservatively overlap because their storage may alias")
	}
	if storageViewSourcesOverlap("left.source", "right.tokens") {
		t.Fatal("unrelated aggregate roots must not be treated as storage aliases")
	}
}

func TestSViewFieldInvalidatedByGrowthThroughSiblingField(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_sibling_darray_field_growth.elisa", `struct ParserBuffers:
	source: mutable darray[u8]
	tokens: mutable darray[u8]

def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic, Unsafe.UncheckedIndex:
		shared: mutable darray[u8] = []
		in owner:
			shared.push(65)
			parser: mutable ParserBuffers = ParserBuffers{source: shared, tokens: shared}
			view: sview = parser.source.as_sview()
			parser.tokens.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected growth through a sibling darray field to invalidate a possibly aliased sview, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewFieldInvalidatedWhenItsSharedSourceGrows(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_field_source_darray_growth.elisa", `struct ParserBuffers:
	source: mutable darray[u8]
	tokens: mutable darray[u8]

def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic, Unsafe.UncheckedIndex:
		shared: mutable darray[u8] = []
		in owner:
			shared.push(65)
			parser: mutable ParserBuffers = ParserBuffers{source: shared, tokens: shared}
			view: sview = parser.source.as_sview()
			shared.push(66)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected growth of a darray shared into an aggregate to invalidate its field-derived sview, got:\n%s", allDiagnostics(result))
	}
}

func TestSViewFieldRetainsSourceAliasAfterFieldRebind(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "sview_field_rebind_keeps_old_source.elisa", `struct ParserBuffers:
	source: mutable darray[u8]
	tokens: mutable darray[u8]

def read(owner: mutable Arena&) -> char:
	can Memory.Allocate, Abort.Panic, Unsafe.UncheckedIndex:
		shared: mutable darray[u8] = []
		other: mutable darray[u8] = []
		in owner:
			shared.push(65)
			other.push(66)
			parser: mutable ParserBuffers = ParserBuffers{source: shared, tokens: other}
			view: sview = parser.source.as_sview()
			parser.source <- other
			shared.push(67)
			return view[0]
	return 'x'
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	if !strings.Contains(allDiagnostics(result), "stale reference") {
		t.Fatalf("expected a view to keep its original source provenance after its field is rebound, got:\n%s", allDiagnostics(result))
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
