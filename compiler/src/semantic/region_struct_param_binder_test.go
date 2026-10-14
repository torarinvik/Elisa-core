package semantic

import (
	"strings"
	"testing"
)

// A value read out of a `Node[b]` carries region b, whatever binds it: a field read, a local
// bound from it, an index into a region-typed container field, a for-loop element and a
// destructuring loop binder. Returning it as `sview @a` or storing it into a `Node[a]` is
// rejected; the same shapes over `Node[a]` are accepted.
func TestStructRegionParamBinderProvenance(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_region_param_binder.elisa", `struct Node[@source]:
    name: mutable sview @source
    names: darray[sview @source] = []
    n: i64 = 0

def read_bad[@a, @b](n: Node[b]) -> sview @a:
    return n.name

def bind_bad[@a, @b](n: Node[b]) -> sview @a:
    x: sview = n.name
    return x

def index_bad[@a, @b](dst: mutable Node[a]&, n: Node[b]&) -> void:
    dst.name <- n.names[0]

def loop_bad[@a, @b](dst: mutable Node[a]&, ns: darray[Node[b]]&) -> void:
    for n in ns |dst|:
        dst.name <- n.name

def loop_names_bad[@a, @b](dst: mutable Node[a]&, n: Node[b]&) -> void:
    for name in n.names |dst|:
        dst.name <- name

def destr_bad[@a, @b](ns: darray[Node[b]]&) -> sview @a:
    for {name, names, n} in ns:
        return name
    return ""

def read_ok[@a](n: Node[a]) -> sview @a:
    return n.name

def loop_ok[@a](dst: mutable Node[a]&, ns: darray[Node[a]]&) -> void:
    for n in ns |dst|:
        dst.name <- n.name

def destr_ok[@a](ns: darray[Node[a]]&) -> sview @a:
    for {name, names, n} in ns:
        return name
    return ""
`)
	errs := result.Errors()
	want := []string{":7:", ":11:", ":14:", ":18:", ":22:", ":26:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 7, 11, 14, 18, 22, 26 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 7, 11, 14, 18, 22, 26 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}
