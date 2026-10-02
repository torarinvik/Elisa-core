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
