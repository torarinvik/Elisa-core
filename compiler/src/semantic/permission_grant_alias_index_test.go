package semantic

import (
	"strings"
	"testing"
)

func TestLocalMutableAliasRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_mutable_alias_requires_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    first: mutable stack Node& = &node
    second: mutable stack Node& = &node
    _ = first
    _ = second
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected missing unsafe alias grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected local mutable alias to infer alias permission, got %q", got)
	}
}

func TestTrustedLocalMutableAliasDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_local_mutable_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    trusted Unsafe.Alias:
        first: mutable stack Node& = &node
        second: mutable stack Node& = &node
        _ = first
        _ = second
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted alias grant to satisfy local mutable alias, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted local alias not to infer caller permission, got %q", got)
	}
}

func TestLocalSharedThenMutableAliasRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_shared_then_mutable_alias_requires_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    shared: stack Node& = &node
    writable: mutable stack Node& = &node
    _ = shared
    _ = writable
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected shared+mutable local alias warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected shared+mutable local alias to infer alias permission, got %q", got)
	}
}

func TestDuplicateLocalSharedAliasesDoNotRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "duplicate_local_shared_aliases_no_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    left: stack Node& = &node
    right: stack Node& = &node
    _ = left
    _ = right
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected duplicate local shared refs to stay safe, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected duplicate local shared refs not to infer alias permission, got %q", got)
	}
}

func TestInnerBlockSharedAliasDoesNotLeakPastScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "inner_block_shared_alias_no_leak.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    if true:
        shared: stack Node& = &node
        _ = shared
    writable: mutable stack Node& = &node
    _ = writable
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected inner shared alias not to leak past scope, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected scoped local alias not to infer alias permission, got %q", got)
	}
}

func TestOuterSharedAliasConflictsInsideNestedScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "outer_shared_alias_conflicts_in_nested_scope.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    shared: stack Node& = &node
    if true:
        writable: mutable stack Node& = &node
        _ = writable
    _ = shared
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected outer shared alias to conflict with nested mutable alias, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected nested local alias conflict to infer alias permission, got %q", got)
	}
}

func TestLiveSharedAliasConflictsWithMutableCallArg(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "live_shared_alias_conflicts_with_mutable_call_arg.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    node: mutable Node = zeroed
    shared: stack Node& = &node
    update(node)
    _ = shared
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected live shared alias to conflict with mutable call arg, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected live shared alias call conflict to infer alias permission, got %q", got)
	}
}

func TestLiveMutableAliasConflictsWithFreshMutableCallArg(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "live_mutable_alias_conflicts_with_fresh_mutable_call_arg.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    node: mutable Node = zeroed
    writable: mutable stack Node& = &node
    update(node)
    _ = writable
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected live mutable alias to conflict with fresh mutable call arg, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected live mutable alias call conflict to infer alias permission, got %q", got)
	}
}

func TestPassingLiveMutableAliasItselfDoesNotRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "passing_live_mutable_alias_itself_no_alias.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    node: mutable Node = zeroed
    writable: mutable stack Node& = &node
    update(writable)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected passing live mutable alias itself to stay safe, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected passing live mutable alias itself not to infer alias permission, got %q", got)
	}
}

func TestAssignedMutableAliasRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assigned_mutable_alias_requires_alias.elisa", `
struct Node:
    value: mutable i32

def run() -> void:
    node: mutable Node = zeroed
    other: mutable Node = zeroed
    shared: stack Node& = &node
    slot: mutable stack Node& = &other
    slot <- &node
    _ = shared
    _ = slot
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected assigned mutable alias to require alias grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected assigned mutable alias to infer alias permission, got %q", got)
	}
}

func TestAliasAssignmentReleasesPreviousRoot(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "alias_assignment_releases_previous_root.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    left: mutable Node = zeroed
    right: mutable Node = zeroed
    slot: mutable stack Node& = &left
    slot <- &right
    update(left)
    _ = slot
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected reassigned alias to release previous root, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected reassigned alias not to infer alias permission, got %q", got)
	}
}

func TestAssignedMutableAliasItselfCanBePassedSafely(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assigned_mutable_alias_itself_can_be_passed.elisa", `
struct Node:
    value: mutable i32

def update(value: mutable Node&) -> void:
    pass

def run() -> void:
    left: mutable Node = zeroed
    right: mutable Node = zeroed
    slot: mutable stack Node& = &left
    slot <- &right
    update(slot)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected assigned alias itself to be passable, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected run function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected assigned alias itself not to infer alias permission, got %q", got)
	}
}

func TestIndexedCallArgumentAlias(t *testing.T) {
	for _, test := range []struct {
		name     string
		index    string
		conflict bool
	}{
		{"distinct", "1", false},
		{"same", "0x0", true},
		{"unknown", "i", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "indexed_call_alias.elisa", `
def update(a: mutable i64&, b: mutable i64&) -> void:
    a <- 1
    b <- 2

def run(i: usize) -> void:
    values: mutable darray[i64] = [1, 2]
    can Unsafe.UncheckedIndex:
        update(&values[0], &values[`+test.index+`])
`, AnalyzeOptions{EnforceUnsafePermissions: true})
			all := allDiagnostics(result)
			if got := strings.Contains(all, "mutable alias requires"); got != test.conflict {
				t.Fatalf("alias conflict = %t, want %t; diagnostics:\n%s", got, test.conflict, all)
			}
		})
	}
}

func TestUnprovenArrayIndexRequiresUnsafeUncheckedIndexGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "array_index_requires_bounds.elisa", `
def read_at(items: i32[4], index: usize) -> i32:
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `unchecked index requires can[Unsafe]`) {
		t.Fatalf("expected unproven array index to require unchecked index grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.UncheckedIndex]" {
		t.Fatalf("expected unproven array index to infer caller permission, got %q", got)
	}
}

func TestRangeLoopProvesFixedArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "range_loop_proves_array_index.elisa", `
def sum4(items: i32[4]) -> i32:
    total: mutable i32 = 0
    for i in 0..<4:
        total <- total + items[i]
    return total
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected range loop bound to prove array indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("sum4")
	if !ok {
		t.Fatal("expected sum4 symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected sum4 function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected proven array index not to infer caller permission, got %q", got)
	}
}

func TestUnprovenDArrayIndexRequiresUnsafeUncheckedIndexGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "darray_index_requires_bounds.elisa", `
def read_at(items: darray[i32], index: usize) -> i32:
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `unchecked index requires can[Unsafe]`) {
		t.Fatalf("expected unproven darray index to require unchecked index grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.UncheckedIndex]" {
		t.Fatalf("expected unproven darray index to infer caller permission, got %q", got)
	}
}

func TestAssertProvesDArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "assert_proves_darray_index.elisa", `
def read_at(items: darray[i32], index: usize) -> i32:
    can Abort.Panic:
        assert index < items.count
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected assert bound to prove darray indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Abort.Panic]" {
		t.Fatalf("expected only assert panic permission, got %q", got)
	}
}

func TestIfBranchProvesDArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "if_branch_proves_darray_index.elisa", `
def read_or_zero(items: darray[i32], index: usize) -> i32:
    if index < items.count:
        return items[index]
    return 0
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected if branch bound to prove darray indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_or_zero")
	if !ok {
		t.Fatal("expected read_or_zero symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_or_zero function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected proven darray index not to infer caller permission, got %q", got)
	}
}

func TestEarlyReturnGuardProvesDArrayIndexBounds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "early_return_proves_darray_index.elisa", `
def read_or_zero(items: darray[i32], index: usize) -> i32:
    if index >= items.count:
        return 0
    return items[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) {
		t.Fatalf("expected early-return guard to prove darray indexing, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_or_zero")
	if !ok {
		t.Fatal("expected read_or_zero symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_or_zero function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected guarded darray index not to infer caller permission, got %q", got)
	}
}

func TestUncheckedIndexGrantDoesNotCoverPointerArithmetic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unchecked_index_grant_not_arithmetic.elisa", `
def advance(ptr: u8&, offset: usize) -> u8&:
    trusted Unsafe.UncheckedIndex:
        return ptr + offset
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `pointer arithmetic requires can[Unsafe]`) {
		t.Fatalf("expected unchecked index grant not to satisfy pointer arithmetic, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("advance")
	if !ok {
		t.Fatal("expected advance symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected advance function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.PointerArithmetic]" {
		t.Fatalf("expected pointer arithmetic to infer caller permission, got %q", got)
	}
}
