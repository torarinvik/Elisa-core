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
	want := []string{":7:", ":13:", ":13:", ":16:", ":18:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 7, 13, 16, 18 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 7, 13, 16, 18 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}

// The program arena is never freed or reset because its Arena value never reaches code: the
// name is usable only in region position.
func TestProgramLivedRegionIsNotAnArenaValue(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_lived_value.elisa", `global region source

def take(a: Arena&) -> void:
    return

def addr_it() -> void:
    take(&source)

def bind_it() -> void:
    a: Arena& = &source
    take(a)

def copy_it() -> void:
    a: Arena = source

def destroy_it() -> void:
    destroy source

def owner_ok() -> i64&:
    return new[source] 5

def in_ok() -> void:
    in source:
        return
`)
	errs := result.Errors()
	want := []string{":7:", ":10:", ":14:", ":17:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 7, 10, 14, 17 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 7, 10, 14, 17 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}

// `-> T @source` names no parameter: the result is a store into the program-lived region,
// so a parameter-region view or an auto-region allocation is rejected, and a caller can
// store the result into any program-lived slot. Element regions (`darray[sview @source]`)
// follow the same rule.
func TestProgramLivedRegionReturnsAndElements(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "program_lived_return.elisa", `global region source

struct Box:
    name: mutable sview @source

def ret_param[@a](s: sview @a) -> sview @source:
    return s

def ret_local() -> sview @source:
    can Memory.Allocate:
        d: mutable dstr = []
        d.push(65)
        return d.as_sview()

def ret_good(s: sview @source) -> sview @source:
    return s

def ret_lit() -> sview @source:
    return "x"

def use_it(b: mutable Box&) -> void:
    b.name <- ret_good(ret_lit())

def push_bad[@a](xs: mutable darray[sview @source]&, s: sview @a) -> void:
    can Memory.Allocate:
        xs.push(s)

def push_good(xs: mutable darray[sview @source]&, b: Box) -> void:
    can Memory.Allocate:
        xs.push(b.name)
`)
	errs := result.Errors()
	want := []string{":7:", ":7:", ":13:", ":26:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 7, 13, 26 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 7, 13, 26 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}
