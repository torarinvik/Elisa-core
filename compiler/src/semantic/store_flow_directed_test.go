package semantic

import (
	"strings"
	"testing"
)

// Adversarial cases for the one-way (directed) store-flow rules: each rule narrows what a summary
// claims, and each of these stores a frame-local borrow through exactly the shape a rule narrows.

// A module-qualified call (`Keep.keep`, as the resolver spells calls inside a module) binds its
// arguments to the callee's parameters one to one; its summary must still report the store.
func TestStoreFlowQualifiedCallStillStores(t *testing.T) {
	src := `module Keep:
    def keep(out: mutable darray[i64&]&, r: i64&) -> void can[Memory.Allocate]:
        can Memory.Allocate:
            out.push(r)

    def fill(out: mutable darray[i64&]&) -> void can[Memory.Allocate]:
        can Memory.Allocate:
            x: i64 = 3
            keep(out, &x)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_qualified_store.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, ":9:") && strings.Contains(e, "function-local storage") {
			return
		}
	}
	t.Fatalf("want the qualified store of &x (line 9) rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
}

// A match binder of a parameter is a one-way element copy only at a type that shares no storage.
// `items` is a darray of references: pushing it stores the caller's list itself.
func TestStoreFlowSharingBinderKeepsHeader(t *testing.T) {
	src := `enum Box:
    Held(items: darray[i64&])
    Empty

def stash(out: mutable darray[darray[i64&]]&, b: Box) -> void can[Memory.Allocate]:
    can Memory.Allocate:
        match b:
            Box.Held(items):
                out.push(items)
            _:
                pass

def fill(out: mutable darray[darray[i64&]]&) -> void can[Memory.Allocate]:
    can Memory.Allocate:
        x: i64 = 1
        local: mutable darray[i64&] = []
        local.push(&x)
        stash(out, Box.Held(local))

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_sharing_binder.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, ":18:") && strings.Contains(e, "function-local storage") {
			return
		}
	}
	t.Fatalf("want the stashed local list (line 18) rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
}

// An element-wise view copy is fresh storage, but its views still point where the source's did:
// copying the caller's views and storing one keeps the caller's local text reachable.
func TestStoreFlowElementCopyKeepsReferents(t *testing.T) {
	src := `struct Table:
    names: mutable darray[sview] = []

def note(table: mutable Table&, names: darray[sview]&) -> void can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        copy: darray[sview] = [name for name in names]
        table.names.push(copy[0])

def run(table: mutable Table&) -> void can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        text: mutable dstr = []
        text.push(65)
        views: mutable darray[sview] = []
        views.push(text.as_sview())
        note(table, &views)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_element_copy_referents.elisa", src)
	for _, e := range result.Errors() {
		if strings.Contains(e, ":15:") {
			return
		}
	}
	t.Fatalf("want the copied local views (line 15) rejected, got:\n%s", strings.Join(result.Errors(), "\n"))
}
