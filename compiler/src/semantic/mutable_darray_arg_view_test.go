package semantic

import (
	"strings"
	"testing"
)

const mutableDArrayArgPrelude = `
def at(b: darray[i64]&) -> i64&:
    return &b[0]

def grow(b: mutable darray[i64]&) -> void:
    for i in 0..<5000:
        b.push(9)

struct Holder:
    xs: darray[i64]
`

// A darray passed as `mutable darray&` may be grown by the callee: every live view into it
// dies, including one obtained through a helper (not syntactically `&b[i]`) and slices.
func TestMutableDArrayArgKillsHelperViews(t *testing.T) {
	cases := map[string]string{
		"helper_ref": `
def run(b: mutable darray[i64]&) -> i64:
    r: i64& = at(b)
    grow(b)
    return r
`,
		"sview": `
def growu(b: mutable darray[u8]&) -> void:
    b.push(1.u8())

def run(b: mutable darray[u8]&) -> i64:
    v: sview = b.as_sview()
    growu(b)
    return v[0].i64()
`,
		"field_path": `
def run(h: mutable Holder&) -> i64:
    r: i64& = at(h.xs)
    grow(h.xs)
    return r
`,
	}
	for name, body := range cases {
		result := analyzeTreeTestSourceWithSemanticErrors(t, "mutable_darray_arg_"+name+".elisa", mutableDArrayArgPrelude+body)
		if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "cannot be used") {
			t.Fatalf("%s: expected the view to die across the mutable darray& call, got:\n%s", name, got)
		}
	}
}

func TestMutableDArrayArgViewRederivedAfterCallIsAccepted(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "mutable_darray_arg_rederive.elisa", mutableDArrayArgPrelude+`
def run(b: mutable darray[i64]&) -> i64:
    r: i64& = at(b)
    grow(b)
    r2: i64& = at(b)
    return r2
`)
	if got := strings.Join(result.Errors(), "\n"); strings.Contains(got, "cannot be used") {
		t.Fatalf("a view re-derived after the call must be accepted, got:\n%s", got)
	}
}

const structFieldViewPrelude = `
struct P:
    storage: darray[u8]
    other: darray[u8]
    pos: i64

def name_at(p: P&, start: usize, n: usize) -> sview:
    return bytes_view_range(p.storage, start, n)

def step(p: mutable P&) -> void:
    p.pos <- p.pos + 1

def grow_other(p: mutable P&) -> void:
    p.other.push(1.u8())

def grow_storage(p: mutable P&) -> void:
    p.storage.push(1.u8())

def grow_via(p: mutable P&) -> void:
    grow_storage(p)

def alias_whole(p: mutable P&) -> void:
    q: mutable P& = p
    q.storage.push(1.u8())

def recur(p: mutable P&, n: i64) -> void:
    if n > 0:
        recur(p, n - 1)
    else:
        p.storage.push(1.u8())
`

// t4 narrowed: a whole-struct mutable borrow ends a field view only when the callee may
// write that field (directly, through a callee, through an alias or recursively).
func TestMutableStructArgSparesUnwrittenFieldViews(t *testing.T) {
	accepted := map[string]string{
		"scalar_field": "step",
		"other_field":  "grow_other",
	}
	for name, callee := range accepted {
		result := analyzeTreeTestSourceWithSemanticErrors(t, "struct_field_view_ok_"+name+".elisa", structFieldViewPrelude+`
def run(p: mutable P&) -> i64:
    v: sview = name_at(p, 0, 1)
    `+callee+`(p)
    return sview_len(v).i64()
`)
		if got := strings.Join(result.Errors(), "\n"); strings.Contains(got, "cannot be used") {
			t.Fatalf("%s: a callee that cannot write p.storage must not end its view, got:\n%s", name, got)
		}
	}
	killed := map[string]string{
		"direct":     "grow_storage(p)",
		"transitive": "grow_via(p)",
		"alias":      "alias_whole(p)",
		"recursive":  "recur(p, 3)",
	}
	for name, call := range killed {
		result := analyzeTreeTestSourceWithSemanticErrors(t, "struct_field_view_kill_"+name+".elisa", structFieldViewPrelude+`
def run(p: mutable P&) -> i64:
    v: sview = name_at(p, 0, 1)
    `+call+`
    return sview_len(v).i64()
`)
		if got := strings.Join(result.Errors(), "\n"); !strings.Contains(got, "cannot be used") {
			t.Fatalf("%s: a callee that may grow p.storage must end its view, got:\n%s", name, got)
		}
	}
}
