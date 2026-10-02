package semantic

import (
	"strings"
	"testing"
)

// A ternary's condition decides which branch flows; its names do not flow
// into the value. A condition that binds (`r if a is r else b`) still hands
// its binder to the branch and must keep flowing.
func TestStoreFlowTernaryConditionDoesNotFlow(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "store_flow_ternary_cond_ok.elisa", `struct Out:
    names: mutable darray[sview] = []

struct Scope:
    kind: u8 = 0
    locals: mutable darray[sview] = []

def put(out: mutable Out&, env: Scope&) -> void:
    can Memory.Allocate:
        out.names.push("a" if env.kind == 0 else "b")

def fill(out: mutable Out&) -> void:
    can Memory.Allocate:
        env: Scope = Scope{}
        put(out, env)

def main() -> i32:
    can Memory.Allocate:
        out: mutable Out = Out{}
        fill(out)
        return 0
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("condition-only local must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

func TestStoreFlowTernaryBranchesStillFlow(t *testing.T) {
	cases := map[string]string{
		"branch":    `struct Out:
    refs: mutable darray[i64&] = []

def put(out: mutable Out&, a: i64&, b: i64&, flag: bool) -> void:
    can Memory.Allocate:
        out.refs.push(a if flag else b)

def fill(out: mutable Out&) -> void:
    can Memory.Allocate:
        local: i64 = 3
        other: i64 = 4
        put(out, local, other, true)

def main() -> i32:
    can Memory.Allocate:
        out: mutable Out = Out{}
        fill(out)
        return 0
`,
		"bind_cond": `struct Out:
    refs: mutable darray[i64&] = []

def put(out: mutable Out&, a: i64&?, b: i64&) -> void:
    can Memory.Allocate:
        out.refs.push(r if a is r else b)

def fill(out: mutable Out&, keep: i64&) -> void:
    can Memory.Allocate:
        local: i64 = 3
        put(out, local, keep)

def main() -> i32:
    can Memory.Allocate:
        out: mutable Out = Out{}
        k: i64 = 1
        fill(out, k)
        return 0
`,
	}
	for name, src := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_ternary_"+name+".elisa", src)
		errs := strings.Join(result.Errors(), "\n")
		if !strings.Contains(errs, "function-local storage") {
			t.Fatalf("%s: must be rejected, got: %q", name, errs)
		}
	}
}
