package semantic

import (
	"strings"
	"testing"
)

// A global's owning container returned by value shares the global's buffer with the caller;
// the global's next growth frees it under the caller's copy (verified by running: the copy
// read the global's later write).
func TestAnalyzeRejectsReturningGlobalStorageByValue(t *testing.T) {
	cases := map[string]string{
		"global_field.neg.elisa": `struct Box:
    items: mutable darray[i64]

global store: mutable Box

def items_of() -> darray[i64]:
    return store.items
`,
		"global_bare.neg.elisa": `global mutable gitems: mutable darray[i64] = zeroed

def items_of() -> darray[i64]:
    return gitems
`,
		"global_nested_paren.neg.elisa": `struct Inner:
    items: mutable darray[i64]

struct Outer:
    inner: mutable Inner

global store: mutable Outer

def items_of() -> darray[i64]:
    return (store.inner.items)
`,
		"global_tail.neg.elisa": `global mutable gitems: mutable darray[i64] = zeroed

def items_of() -> darray[i64]:
    gitems
`,
		"global_via_local.neg.elisa": `global mutable gitems: mutable darray[i64] = zeroed

def items_of() -> darray[i64]:
    x: darray[i64] = gitems
    return x
`,
		"global_via_assign.neg.elisa": `global mutable gitems: mutable darray[i64] = zeroed

def items_of() -> darray[i64]:
    x: mutable darray[i64] = []
    x <- gitems
    return x
`,
		"global_dstr.neg.elisa": `global mutable name: mutable dstr = zeroed

def name_of() -> dstr:
    return name
`,
		"global_struct_holder.neg.elisa": `struct Box:
    items: mutable darray[i64]

global store: mutable Box

def box_of() -> Box:
    return store
`,
	}
	for name, source := range cases {
		result := analyzeTreeTestSourceWithSemanticErrors(t, name, source)
		all := strings.Join(result.Errors(), "\n")
		if !strings.Contains(all, "cannot be returned by value with a region-less type (the copy shares the global's storage") {
			t.Fatalf("%s: expected global-storage return rejection, got:\n%s", name, all)
		}
	}
}

func TestAnalyzeAcceptsGlobalStorageReturnsThatDoNotShare(t *testing.T) {
	cases := map[string]string{
		"global_ref.pos.elisa": `global mutable gitems: mutable darray[i64] = zeroed

def items_of() -> darray[i64]&:
    can Global.Read:
        return &gitems
`,
		"global_scalar.pos.elisa": `struct Box:
    items: mutable darray[i64]
    n: i64

global store: mutable Box

def first() -> i64:
    return store.items[0] + store.n

def count() -> i64:
    return store.items.count.i64()
`,
		"global_fresh.pos.elisa": `global mutable gitems: mutable darray[i64] = zeroed

def items_of() -> darray[i64]:
    can Global.Read:
        out: mutable darray[i64] = []
        out.extend([v for v in gitems])
        return out
`,
	}
	for name, source := range cases {
		analyzeTreeTestSource(t, name, source)
	}
}
