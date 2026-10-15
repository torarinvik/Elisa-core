package semantic

import "testing"

// docs/128: a region-polymorphic function's synthesized auto region adopts the CALLER's arena,
// which does not outlive a program-lived region. That adoption must not sanction a store of a
// local value into program-lived storage, through a callee or a fresh container.
func TestProgramRegionTargetNotSanctionedByAdoption(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"callee_store", `global region source

def fill(out: mutable darray[sview]&, s: sview) -> void:
    can Memory.Allocate:
        out.push(s)

def go() -> darray[sview @source] @source:
    can Memory.Allocate:
        found: mutable darray[sview @source] @source = []
        d: mutable dstr = []
        d.push(65)
        fill(found, d.as_sview())
        return found
`, []string{":12:"}},
		{"fresh_container", `global region source

struct Ann:
    name: sview @source

def collect() -> darray[Ann] @source:
    can Memory.Allocate:
        found: mutable darray[Ann] @source = []
        d: mutable dstr = []
        d.push(65)
        found.push(Ann{name: d.as_sview()})
        return found
`, []string{":11:40", ":11:20"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
