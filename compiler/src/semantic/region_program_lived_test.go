package semantic

import (
	"strings"
	"testing"
)

// docs/128: `global region source` is program-lived. A view in `source` may be stored into
// any region's slot; a slot in `source` accepts only program-lived values; a binder may not
// reuse the name.
func TestProgramLivedRegionStores(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_lived_region.elisa", `global region source

struct Node[@r]:
    name: mutable sview @r

def into_program_from_param[@a](n: mutable Node[source]&, src: sview @a) -> void:
    n.name <- src

def into_program_from_local(n: mutable Node[source]&) -> void:
    can Memory.Allocate:
        text: mutable dstr = []
        text.push(65)
        n.name <- text.as_sview()

def lit_program[@a](src: sview @a) -> Node[source]:
    return Node[source]{name: src}

def shadow_param[@source](src: sview @source) -> void:
    return

def program_into_param[@a](n: mutable Node[a]&, src: sview @source) -> void:
    n.name <- src

def program_into_program(n: mutable Node[source]&, src: sview @source) -> void:
    n.name <- src

def program_lit[@a](src: sview @source) -> Node[a]:
    return Node[a]{name: src}

def program_new() -> i64&:
    return new[source] 5
`)
	errs := result.Errors()
	want := []string{":7:", ":13:", ":13:", ":16:", ":18:", ":31:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 7, 13, 16, 18, 31 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 7, 13, 16, 18, 31 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}
