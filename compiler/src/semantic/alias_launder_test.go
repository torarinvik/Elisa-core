package semantic

import (
	"strings"
	"testing"
)

// A borrow laundered through a reference-returning call must still be caught as an alias when
// passed alongside an overlapping mutable borrow. Without provenance propagation, `r = get_ref(&x)`
// would lose x's root and `mutate_pair(r, &x)` would slip past the call-site alias checker.
func TestLaunderedRefThroughCallReturnRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "laundered_ref_call_return.elisa", `
def get_ref(p: mutable i32&) -> mutable i32&:
    return p

def mutate_pair(a: mutable i32&, b: mutable i32&) -> void:
    pass

def run(x: mutable i32&) -> void:
    r: mutable i32& = get_ref(x)
    mutate_pair(r, x)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe]`) {
		t.Fatalf("expected laundered-ref alias to require unsafe grant, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("run")
	if !ok {
		t.Fatal("expected run symbol")
	}
	fnType := sym.Type.(*FuncType)
	if got := PermissionRefsString(fnType.PermissionRefs); got != " can[Unsafe.Alias]" {
		t.Fatalf("expected laundered call to infer alias permission, got %q", got)
	}
}

// Inline form: f(get_ref(&x), &x) — the laundering call is itself an argument.
func TestInlineLaunderedRefThroughCallReturnRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "inline_laundered_ref_call_return.elisa", `
def get_ref(p: mutable i32&) -> mutable i32&:
    return p

def mutate_pair(a: mutable i32&, b: mutable i32&) -> void:
    pass

def run(x: mutable i32&) -> void:
    mutate_pair(get_ref(x), x)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe]`) {
		t.Fatalf("expected inline laundered-ref alias to require unsafe grant, got:\n%s", all)
	}
}

// A reference returned from a call that aliases MULTIPLE parameters carries every possible root,
// so passing it alongside one of those parameters is caught.
func TestMultiParamAliasReturnLaunderRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "multi_param_alias_return_launder.elisa", `
def pick(cond: bool, a: mutable i32&, b: mutable i32&) -> mutable i32&:
    if cond:
        return a
    return b

def mutate_pair(x: mutable i32&, y: mutable i32&) -> void:
    pass

def run(p: mutable i32&, q: mutable i32&) -> void:
    r: mutable i32& = pick(true, p, q)
    mutate_pair(r, p)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe]`) {
		t.Fatalf("expected multi-param alias-return launder to require unsafe grant, got:\n%s", all)
	}
}

// Passing the laundered multi-alias ref alongside a DISJOINT location stays accepted: the ref can
// only be p or q, neither of which overlaps an unrelated third reference.
func TestMultiParamAliasReturnDisjointStaysSafe(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "multi_param_alias_return_disjoint.elisa", `
def pick(cond: bool, a: mutable i32&, b: mutable i32&) -> mutable i32&:
    if cond:
        return a
    return b

def mutate_pair(x: mutable i32&, y: mutable i32&) -> void:
    pass

def run(p: mutable i32&, q: mutable i32&, s: mutable i32&) -> void:
    r: mutable i32& = pick(true, p, q)
    mutate_pair(r, s)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) {
		t.Fatalf("expected disjoint third reference to stay safe, got:\n%s", all)
	}
}

// A method (UFCS) call whose return aliases the whole receiver is mapped through the param-aligned
// argument list, so the receiver shift no longer hides the borrow.
func TestMethodReceiverAliasReturnLaunderRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "method_receiver_alias_return_launder.elisa", `
struct Box:
    value: mutable i32

def identity(self: mutable Box&) -> mutable Box&:
    return self

def mutate_pair(x: mutable Box&, y: mutable Box&) -> void:
    pass

def run(box: mutable Box&) -> void:
    h: mutable Box& = box.identity()
    mutate_pair(h, box)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe]`) {
		t.Fatalf("expected method receiver-alias launder to require unsafe grant, got:\n%s", all)
	}
}

// A method returning a FIELD borrow of the receiver (`get_mut(self) -> &self.value`) — the field
// borrow is recorded as a param-rooted alias location, so laundering it past the checker is caught.
func TestFieldBorrowReturnMethodLaunderRequiresUnsafeAliasGrant(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_borrow_return_method_launder.elisa", `
struct Box:
    value: mutable i32

def get_mut(self: mutable Box&) -> mutable i32&:
    return &self.value

def mutate_pair(a: mutable i32&, b: mutable i32&) -> void:
    pass

def run(box: mutable Box&) -> void:
    h: mutable i32& = box.get_mut()
    mutate_pair(h, &box.value)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, `mutable alias requires can[Unsafe]`) {
		t.Fatalf("expected field-borrow-return method launder to require unsafe grant, got:\n%s", all)
	}
}

// A method returning a by-VALUE copy of a field (no `&`) creates no alias and must stay accepted.
func TestFieldValueReturnMethodStaysSafe(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "field_value_return_method_safe.elisa", `
struct Box:
    value: mutable i32

def get(self: Box&) -> i32:
    return self.value

def use_twice(a: i32, b: i32) -> void:
    pass

def run(box: mutable Box&) -> void:
    v: i32 = box.get()
    use_twice(v, box.value)
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if strings.Contains(all, `mutable alias requires`) {
		t.Fatalf("expected by-value field return to stay safe, got:\n%s", all)
	}
}
