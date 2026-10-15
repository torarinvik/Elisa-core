package semantic

import "testing"

// A bulk copy (`extend`) moves the source's ELEMENTS. Elements whose declared type is
// program-lived (`darray[sview @source]`) do not live in the source container's region, so
// copying them into program-region storage dangles nowhere; region-less elements still do.
func TestProgramLivedBulkElementStore(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"program_lived_elements_extended", `global region source

struct E:
    a: mutable darray[sview @source] = []
    b: mutable darray[sview @source] = []

def f(e: mutable E&) -> void:
    can Memory.Allocate, Abort.Panic:
        e.b.extend(e.a)
`, nil},
		{"region_less_elements_extended", `global region source

struct E:
    a: mutable darray[sview] = []
    b: mutable darray[sview @source] = []

def f(e: mutable E&) -> void:
    can Memory.Allocate, Abort.Panic:
        e.b.extend(e.a)
`, []string{":9:21"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
