package semantic

import (
	"strings"
	"testing"
)

func TestRawExternCallRequiresUnsafeRawExternGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "raw_extern_requires_unsafe.elisa", `
extern c_puts(text: u8&) -> int

def write(text: u8&) -> int:
    return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "c_puts" requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.RawExtern or a surrounding can ...: block`) {
		t.Fatalf("expected missing raw extern grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("write")
	if !ok {
		t.Fatal("expected write symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected write function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.RawExtern]" {
		t.Fatalf("expected raw extern to infer caller permission, got %q", got)
	}
}

func TestTrustedRawExternCallDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_raw_extern.elisa", `
extern c_puts(text: u8&) -> int

def write(text: u8&) -> int:
    trusted Unsafe.RawExtern:
        return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `c_puts`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted raw extern grant to satisfy call, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("write")
	if !ok {
		t.Fatal("expected write symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected write function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted raw extern not to infer caller permission, got %q", got)
	}
}

func TestRawExternPreservesDeclaredPermissions(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "raw_extern_preserves_declared_permissions.elisa", `
extern c_puts(text: u8&) -> int can[Console.Write]

def write(text: u8&) -> int:
    can Console.Write:
        trusted Unsafe.RawExtern:
            return c_puts(text)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `c_puts`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected raw extern grant to be satisfied by trusted block, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("write")
	if !ok {
		t.Fatal("expected write symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected write function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Console.Write]" {
		t.Fatalf("expected declared extern permission to remain visible, got %q", got)
	}
}

func TestGlobalReadInfersGlobalReadPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_global_requires_unsafe.elisa", `
global mutable counter: int = 0

def read_counter() -> int:
    return counter
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `mutable global read requires`) {
		t.Fatalf("expected direct global read to require a local grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_counter")
	if !ok {
		t.Fatal("expected read_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Global.Read, Unsafe.MutableGlobal]" {
		t.Fatalf("expected global read to infer caller permission, got %q", got)
	}
}

func TestTrustedGlobalReadStillRequiresAndPropagatesCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_mutable_global.elisa", `
global mutable counter: int = 0

def read_counter() -> int:
    trusted Global.Read, Unsafe.MutableGlobal:
        return counter
def caller() -> int:
    return read_counter()
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable global read requires`) {
		t.Fatalf("trusted Global.Read must not satisfy the local mutable-global read, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_counter")
	if !ok {
		t.Fatal("expected read_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Global.Read]" {
		t.Fatalf("expected Global.Read to remain visible in the function row, got %q", got)
	}
	if !strings.Contains(all, `call to "read_counter" requires can[Global]`) {
		t.Fatalf("expected caller to need the propagated Global.Read grant, got:\n%s", all)
	}
}

func TestTrustedGlobalWriteStillRequiresAndPropagatesCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_mutable_global_write.elisa", `
global mutable counter: int = 0

def set_counter(value: int) -> void:
    trusted Global.Write, Unsafe.MutableGlobal:
        counter <- value
def caller() -> void:
    set_counter(1)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable global write requires`) {
		t.Fatalf("trusted Global.Write must not satisfy the local mutable-global write, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("set_counter")
	if !ok {
		t.Fatal("expected set_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected set_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Global.Write]" {
		t.Fatalf("expected Global.Write to remain visible in the function row, got %q", got)
	}
	if !strings.Contains(all, `call to "set_counter" requires can[Global]`) {
		t.Fatalf("expected caller to need the propagated Global.Write grant, got:\n%s", all)
	}
}

func TestTrustedNonUnsafeEffectsDoNotSuppressCallerTracking(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_sync_is_tracked.elisa", `
extern platform_lock() -> void can[Sync.Lock]

def wrapper() -> void:
    trusted Sync.Lock:
        platform_lock()
def caller() -> void:
    wrapper()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	sym, ok := result.GlobalScope.Lookup("wrapper")
	if !ok {
		t.Fatal("expected wrapper symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected wrapper function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Sync.Lock]" {
		t.Fatalf("trusted non-Unsafe effect must remain in the inferred row, got %q", got)
	}
	if !strings.Contains(all, `call to "wrapper" requires can[Sync]`) {
		t.Fatalf("expected caller to need the propagated Sync grant, got:\n%s", all)
	}
	if !strings.Contains(all, `call to "platform_lock" requires can[Sync]`) {
		t.Fatalf("trusted Sync.Lock must not satisfy the operation's local grant, got:\n%s", all)
	}
}

func TestCanSyncEffectGrantsLocallyAndPropagates(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "can_sync_is_tracked.elisa", `
extern platform_lock() -> void can[Sync.Lock]

def wrapper() -> void:
    can Sync.Lock:
        platform_lock()
def caller() -> void:
    wrapper()
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if strings.Contains(all, `call to "platform_lock" requires can[Sync]`) {
		t.Fatalf("can Sync.Lock should satisfy the operation's local grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("wrapper")
	if !ok {
		t.Fatal("expected wrapper symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected wrapper function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Sync.Lock]" {
		t.Fatalf("can Sync.Lock must remain in the inferred row, got %q", got)
	}
	if !strings.Contains(all, `call to "wrapper" requires can[Sync]`) {
		t.Fatalf("expected caller to need the propagated Sync grant, got:\n%s", all)
	}
}

func TestLocalGlobalGrantInfersCallerRowWithoutSignature(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "local_global_grant_infers_row.elisa", `
global mutable counter: int = 0

def set_counter(value: int) -> void:
    can Global.Write:
        counter <- value
def caller() -> void:
    set_counter(1)
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	sym, ok := result.GlobalScope.Lookup("set_counter")
	if !ok {
		t.Fatal("expected set_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected set_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Global.Write]" {
		t.Fatalf("local Global.Write grant should infer the caller row without a signature, got %q", got)
	}
	if !strings.Contains(all, `call to "set_counter" requires can[Global]`) {
		t.Fatalf("expected caller to need the inferred Global.Write grant, got:\n%s", all)
	}
}

func TestGlobalAssignmentInfersGlobalWritePermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "mutable_global_assignment_requires_unsafe.elisa", `
global mutable counter: int = 0

def set_counter(value: int) -> void:
    counter <- value
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), `mutable global write requires`) {
		t.Fatalf("expected direct global write to require a local grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("set_counter")
	if !ok {
		t.Fatal("expected set_counter symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected set_counter function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Global.Write, Unsafe.MutableGlobal]" {
		t.Fatalf("expected global assignment to infer caller permission, got %q", got)
	}
}

func TestGlobalReadWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "global_read_with_grant.elisa", `
global counter: int = 7

def read_counter() -> int:
    can Global.Read:
        return counter
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `global read requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected Global.Read grant to satisfy read, got:\n%s", all)
	}
}

func TestGlobalWriteWithLocalGrantIsQuiet(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "global_write_with_grant.elisa", `
global mutable counter: int = 0

def set_counter(value: int) -> void:
    can Global.Write, Unsafe.MutableGlobal:
        counter <- value
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `global write requires`) || strings.Contains(all, `mutable global access requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected Global.Write grant to satisfy write, got:\n%s", all)
	}
}

func TestDeclaredGlobalReadCallRequiresLocalGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "declared_global_read_call_requires_grant.elisa", `
def read_effect() -> int can[Global.Read]:
    return 1

def caller() -> int:
    return read_effect()
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `call to "read_effect" requires can[Global]`) {
		t.Fatalf("expected declared Global.Read call to require an explicit local grant, got:\n%s", all)
	}
}

func TestRefIndexRequiresUnsafeUncheckedIndexGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "ref_index_requires_unsafe.elisa", `
def read_at(ptr: u8&, index: usize) -> u8:
    return ptr[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `unchecked index requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.UncheckedIndex or a surrounding can ...: block`) {
		t.Fatalf("expected missing unchecked index grant warning, got:\n%s", all)
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
		t.Fatalf("expected ref index to infer caller permission, got %q", got)
	}
}

func TestTrustedRefIndexDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_ref_index.elisa", `
def read_at(ptr: u8&, index: usize) -> u8:
    trusted Unsafe.UncheckedIndex:
        return ptr[index]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `unchecked index requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted unchecked index grant to satisfy access, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_at")
	if !ok {
		t.Fatal("expected read_at symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_at function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted ref index not to infer caller permission, got %q", got)
	}
}

func TestStaleViewRequiresUnsafeStaleRefGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "stale_view_requires_unsafe.elisa", `
def read_stale(items: mutable darray[i32]&) -> i32:
    view: view[i32] = items[0:items.count]
    items.clear()
    trusted Unsafe.UncheckedIndex:
        return view[0]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `stale reference requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.StaleRef or a surrounding can ...: block`) {
		t.Fatalf("expected missing stale ref grant warning, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_stale")
	if !ok {
		t.Fatal("expected read_stale symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_stale function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.StaleRef]" {
		t.Fatalf("expected stale view use to infer caller permission, got %q", got)
	}
}

func TestTrustedStaleViewDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_stale_view.elisa", `
def read_stale(items: mutable darray[i32]&) -> i32:
    view: view[i32] = items[0:items.count]
    items.clear()
    trusted Unsafe.StaleRef:
        trusted Unsafe.UncheckedIndex:
            return view[0]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `stale reference requires`) || strings.Contains(all, `unchecked index requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted stale ref and unchecked index grants to satisfy access, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("read_stale")
	if !ok {
		t.Fatal("expected read_stale symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected read_stale function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected trusted stale ref not to infer caller permission, got %q", got)
	}
}

func TestDuplicateMutableCallArgRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "duplicate_mutable_call_arg_requires_alias.elisa", `
struct Node:
    value: mutable i32

def update_pair(left: mutable Node&, right: mutable Node&) -> void:
    pass

def run(node: mutable Node&) -> void:
    update_pair(node, node)
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
		t.Fatalf("expected duplicate mutable call arg to infer alias permission, got %q", got)
	}
}

func TestTrustedDuplicateMutableCallArgDoesNotInferCallerPermission(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "trusted_duplicate_mutable_call_arg.elisa", `
struct Node:
    value: mutable i32

def update_pair(left: mutable Node&, right: mutable Node&) -> void:
    pass

def run(node: mutable Node&) -> void:
    trusted Unsafe.Alias:
        update_pair(node, node)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected trusted alias grant to satisfy duplicate mutable call, got:\n%s", all)
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
		t.Fatalf("expected trusted alias not to infer caller permission, got %q", got)
	}
}

func TestSharedAndMutableCallArgsRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shared_and_mutable_call_args_require_alias.elisa", `
struct Node:
    value: mutable i32

def inspect_and_update(shared: Node&, writable: mutable Node&) -> void:
    pass

def run(node: mutable Node&) -> void:
    inspect_and_update(node, node)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe] and has no explicit local effect grant; add can Unsafe.Alias or a surrounding can ...: block`) {
		t.Fatalf("expected shared+mutable call alias warning, got:\n%s", all)
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
		t.Fatalf("expected shared+mutable call to infer alias permission, got %q", got)
	}
}

func TestDuplicateSharedCallArgsDoNotRequireUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "duplicate_shared_call_args_no_alias.elisa", `
struct Node:
    value: mutable i32

def inspect_pair(left: Node&, right: Node&) -> void:
    pass

def run(node: Node&) -> void:
    inspect_pair(node, node)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) || strings.Contains(all, `explicit local effect grant`) {
		t.Fatalf("expected duplicate shared refs to stay safe, got:\n%s", all)
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
		t.Fatalf("expected duplicate shared refs not to infer alias permission, got %q", got)
	}
}
