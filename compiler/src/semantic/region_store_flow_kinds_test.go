package semantic

import (
	"strings"
	"testing"
)

const storeFlowKindsSource = `struct Box:
    items: mutable darray[sview] = []

def copy_loop(xs: darray[sview]) -> darray[sview]:
    can Memory.Allocate:
        out: mutable darray[sview] = []
        for x in xs |out|:
            out.push(x)
        return out

def copy_index(xs: darray[sview]) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview] = []
        out.push(xs[0])
        return out

def copy_compr(xs: darray[sview]) -> darray[sview]:
    can Memory.Allocate:
        return [x for x in xs]

def copy_extend(xs: darray[sview]) -> darray[sview]:
    can Memory.Allocate:
        out: mutable darray[sview] = []
        out.extend(xs)
        return out

def copy_boxed(xs: darray[sview]) -> Box:
    can Memory.Allocate:
        out: mutable darray[sview] = []
        out.extend(xs)
        return Box{items: out}

def copy_rec(xs: darray[sview], n: i64) -> darray[sview]:
    can Memory.Allocate:
        if n > 0:
            return copy_rec(copy_loop(xs), n - 1)
        return copy_loop(xs)

def same(xs: darray[sview]) -> darray[sview]:
    return xs

def hdr_slice(xs: darray[sview]) -> view[sview]:
    can Abort.Panic:
        return xs[0:1]

def hdr_field(b: Box) -> darray[sview]:
    return b.items

def hdr_helper(xs: darray[sview]) -> darray[sview]:
    return same(xs)

def hdr_rec(xs: darray[sview], n: i64) -> darray[sview]:
    if n > 0:
        return hdr_rec(xs, n - 1)
    return xs

def hdr_rec_mixed(xs: darray[sview], n: i64) -> darray[sview]:
    can Memory.Allocate:
        if n > 0:
            return copy_loop(hdr_rec_mixed(xs, n - 1))
        return xs

def hdr_nested(xss: darray[darray[sview]]) -> darray[darray[sview]]:
    can Memory.Allocate:
        out: mutable darray[darray[sview]] = []
        for x in xss |out|:
            out.push(x)
        return out

def hdr_holder(xs: darray[sview]) -> darray[darray[sview]]:
    can Memory.Allocate:
        out: mutable darray[darray[sview]] = []
        out.push(xs)
        return out

def hdr_alias_local(xs: darray[sview]) -> darray[sview]:
    alias: darray[sview] = xs
    return alias

def put_copy(into: mutable darray[sview]&, xs: darray[sview]) -> void:
    can Memory.Allocate:
        into.extend(xs)

def put_header(into: mutable darray[darray[sview]]&, xs: darray[sview]) -> void:
    can Memory.Allocate:
        into.push(xs)
`

// Result edges: copied element values (loop push, index push, comprehension, extend, a box of a
// copy, recursion over a copy) are Elements; a slice, a field, an alias through a helper,
// recursion returning the parameter, nested containers, holders and a local alias stay Header.
func TestStoreFlowEdgeKinds(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_kinds.elisa", storeFlowKindsSource)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected errors:\n%s", strings.Join(errs, "\n"))
	}
	a := result.analyzer
	a.currentScope = a.globalScope
	want := map[string]storeFlowEdgeKind{
		"copy_loop": storeFlowEdgeElements, "copy_index": storeFlowEdgeElements, "copy_compr": storeFlowEdgeElements,
		"copy_extend": storeFlowEdgeElements, "copy_boxed": storeFlowEdgeElements, "copy_rec": storeFlowEdgeElements,
		"hdr_slice": storeFlowEdgeHeader, "hdr_field": storeFlowEdgeHeader, "hdr_helper": storeFlowEdgeHeader,
		"hdr_rec": storeFlowEdgeHeader, "hdr_rec_mixed": storeFlowEdgeHeader, "hdr_nested": storeFlowEdgeHeader,
		"hdr_holder": storeFlowEdgeHeader, "hdr_alias_local": storeFlowEdgeHeader,
	}
	for name, kind := range want {
		sum := a.storeFlowKindsFor(storeScanFindFunc(t, result, name))
		if got := sum.retKindOf(0); got != kind {
			t.Errorf("%s: result edge from parameter 0 is %v, want %v", name, got, kind)
		}
	}
	if got := a.storeFlowKindsFor(storeScanFindFunc(t, result, "put_copy")).reachKindOf(1, 0); got != storeFlowEdgeElements {
		t.Errorf("put_copy: xs -> into is %v, want elements", got)
	}
	if got := a.storeFlowKindsFor(storeScanFindFunc(t, result, "put_header")).reachKindOf(1, 0); got != storeFlowEdgeHeader {
		t.Errorf("put_header: xs -> into is %v, want header", got)
	}
}

// An Elements edge drops only the argument's own storage: copied views that point into a
// short-lived local buffer still escape through it, so the call stays rejected.
func TestStoreFlowElementsEdgeStillJudgesCopiedViews(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_flow_kinds_views.elisa", `struct Table:
    names: mutable darray[sview] = []

def keep_copies(table: mutable Table&, xs: darray[sview]) -> void:
    can Memory.Allocate:
        table.names.extend(xs)

def run(table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        owner: mutable dstr = []
        owner.push(72)
        xs: mutable darray[sview] = []
        xs.push(owner.as_sview())
        keep_copies(table, xs)
`)
	errs := result.Errors()
	if len(errs) == 0 {
		t.Fatalf("copied views into a local dstr stored into the table must be rejected")
	}
	found := false
	for _, e := range errs {
		found = found || strings.Contains(e, ":14:")
	}
	if !found {
		t.Fatalf("expected the keep_copies call (line 14) rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}
