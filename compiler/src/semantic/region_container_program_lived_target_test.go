package semantic

import "testing"

// A region-carrying container of program-lived elements (darray[sview @source] @r) can only
// receive program-lived values, so a by-value local argument is no escape into it (rp4); a callee
// pushing a region-less view into it is still rejected in the callee (rp5).
func TestRegionContainerProgramLivedElemsTarget(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"accept", `global region source

struct Entry:
    owners: mutable darray[sview] = []
    key: sview @source = ""

def stage(names: mutable darray[sview @source]&, entries: mutable darray[Entry]) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(entries[0].key) if entries.count > 0

def context[@r](names: mutable darray[sview @source]& @r) -> void:
    can Memory.Allocate, Abort.Panic:

        staged: mutable darray[Entry] = []
        staged.push(Entry{owners: ["x"]})
        stage(names, staged)
`, nil},
		{"callee_view", `global region source

struct Entry:
    owners: mutable darray[sview] = []
    key: sview @source = ""

def stage(names: mutable darray[sview @source]&, entries: mutable darray[Entry]) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(entries[0].owners[0]) if entries.count > 0

def context[@r](names: mutable darray[sview @source]& @r) -> void:
    can Memory.Allocate, Abort.Panic:

        staged: mutable darray[Entry] = []
        d: mutable dstr = []
        d.push(65)
        staged.push(Entry{owners: ["x"]})
        stage(names, staged)
`, []string{":9:37"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
