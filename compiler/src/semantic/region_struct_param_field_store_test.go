package semantic

import (
	"strings"
	"testing"
)

// A struct region parameter is checked like any other region: a field typed `sview @source`
// of a `Node[a]` is storage in region a. A view of a shorter region b (or of a local dstr)
// stored there -- by a struct literal (explicit or expected-type instantiation), a field
// assignment, or a nested field assignment -- is rejected; a view in region a is accepted.
func TestStructRegionParamFieldStore(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "struct_region_param_field_store.elisa", `struct Node[@source]:
    name: mutable sview @source

struct Holder[@source]:
    node: mutable Node[source]

def lit_local[@a](src: sview @a) -> Node[a]:
    can Memory.Allocate:
        text: mutable dstr = []
        text.push(65)
        return Node[a]{name: text.as_sview()}

def lit_param[@a, @b](src: sview @b) -> Node[a]:
    return Node[a]{name: src}

def lit_expected[@a, @b](h: mutable Holder[a]&, src: sview @b) -> void:
    h.node <- Node{name: src}

def lit_push[@a, @b](xs: mutable darray[Node[a]]&, src: sview @b) -> void:
    can Memory.Allocate:
        xs.push(Node[a]{name: src})

def assign_param[@a, @b](n: mutable Node[a]&, src: sview @b) -> void:
    n.name <- src

def nested_param[@a, @b](h: mutable Holder[a]&, src: sview @b) -> void:
    h.node.name <- src

def good[@a](n: mutable Node[a]&, src: sview @a) -> void:
    n.name <- src

def good_lit[@a](src: sview @a) -> Node[a]:
    return Node[a]{name: src}

def good_nested[@a](h: mutable Holder[a]&, src: sview @a) -> void:
    h.node <- Node{name: src}
`)
	errs := result.Errors()
	want := []string{":11:", ":14:", ":17:", ":21:", ":24:", ":27:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 11, 14, 17, 21, 24, 27 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 11, 14, 17, 21, 24, 27 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}
