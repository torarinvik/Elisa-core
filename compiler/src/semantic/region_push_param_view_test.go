package semantic

import "testing"

// A view pushed into program-region or source-declared region storage obeys the assignment
// rule: a region-less parameter view is rejected; a parameter whose views carry the target region
// (or a region-annotated reference) is accepted, and its caller is checked instead. A by-ref
// struct whose every field is program-lived needs no inferred region parameter.
func TestRegionPushParamViewSoundness(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"push_region_less_param", `global region source

struct S:
    names: mutable darray[sview @source] = []

def a2(s: mutable S&, n: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        s.names.push(n)

def a3(s: mutable darray[sview @source]&, n: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        s.push(n)
`, []string{":8:22", ":12:16"}},
		{"push_annotated_param", `global region source

struct S:
    names: mutable darray[sview @source] = []

def a2(s: mutable S&, n: sview @source) -> void:
    can Memory.Allocate, Abort.Panic:
        s.names.push(n)

def a3(s: mutable darray[sview @source]&, n: sview @source) -> void:
    can Memory.Allocate, Abort.Panic:
        s.push(n)
`, nil},
		{"region_ref_param_views", `struct M:
    names: mutable darray[sview] = []

def e[@r](m: mutable M& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview] @r = []
        for v in m.names:
            out.push(v)
        m.names <- out
`, nil},
		{"element_region_param_views", `def add[@r](names: mutable darray[sview]& @r, terms: darray[sview @r]&) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(terms[0])
`, nil},
		{"element_region_less_param_views", `def add[@r](names: mutable darray[sview]& @r, terms: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(terms[0])
`, []string{":3:25"}},
		{"caller_passes_local_views", `def add[@r](names: mutable darray[sview]& @r, terms: darray[sview @r]&) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(terms[0])

def caller[@a](names: mutable darray[sview]& @a) -> void:
    can Memory.Allocate, Abort.Panic:
        d: mutable dstr = []
        d.push(65)
        terms: mutable darray[sview] = [d.as_sview()]
        add(names, terms)
`, []string{":10:20"}},
		{"program_lived_struct_param_grows", `global region source

struct S:
    xs: mutable darray[u32] @source = []
    names: darray[sview @source] @source = []

def grow(s: mutable S&) -> void:
    can Memory.Allocate, Abort.Panic:
        s.xs.push(1)

def caller() -> u32:
    can Memory.Allocate, Abort.Panic:
        local: mutable S = S{}
        grow(local)
        return local.xs[0]
`, nil},
		{"program_lived_struct_param_unannotated_push", `global region source

struct S:
    names: mutable darray[sview @source] @source = []

def grow(s: mutable S&, n: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        s.names.push(n)
`, []string{":8:22"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
