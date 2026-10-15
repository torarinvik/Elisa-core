package semantic

import "testing"

// A view borrowed from the caller with no region is only known to outlive the call, not a
// region parameter: storing it into `@a` storage must be rejected (the caller could pass a
// local string's view). Annotating the parameter `@a` moves the obligation to the caller, which
// must then not pass a shorter-lived view.
func TestRegionParamViewStoreSoundness(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"unannotated_param_into_region_field", `struct Node[@r]:
    name: mutable sview @r

def f[@a](n: mutable Node[a]&, s: sview) -> void:
    n.name <- s
`, []string{":5:6"}},
		{"annotated_param_into_region_field", `struct Node[@r]:
    name: mutable sview @r

def g[@a](n: mutable Node[a]&, s: sview @a) -> void:
    n.name <- s
`, nil},
		{"caller_passes_local_view", `struct Node[@r]:
    name: mutable sview @r

def g[@a](n: mutable Node[a]&, s: sview @a) -> void:
    n.name <- s

def caller[@a](n: mutable Node[a]&) -> void:
    can Memory.Allocate:
        d: mutable dstr = []
        d.push(65)
        g(n, d.as_sview())
`, []string{":11:11", ":11:24"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
