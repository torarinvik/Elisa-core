package semantic

import "testing"

// A program-lived struct declared inside a module is resolved in the module's namespace before
// region inference stamps its ref param; a region-less view stored into it is still rejected.
func TestProgramLivedModuleStructParamNoRegionStamp(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"module_program_lived_param", `global region source

module Sem:
    struct Path:
        names: mutable darray[sview @source] @source = []

    struct Exits:
        names: mutable darray[sview @source] @source = []
        paths: mutable darray[Path] @source = []
        seen: mutable bool = false

    def note(exits: mutable Exits&, n: sview @source) -> void:
        can Memory.Allocate, Abort.Panic:
            exits.names.push(n)

    def caller() -> void:
        can Memory.Allocate, Abort.Panic:
            exits: mutable Exits = Exits{}
            note(exits, "a")
`, nil},
		{"module_regionless_view_store", `global region source

module Sem:
    struct Path:
        names: mutable darray[sview @source] @source = []

    struct Exits:
        names: mutable darray[sview @source] @source = []
        paths: mutable darray[Path] @source = []
        seen: mutable bool = false

    def note(exits: mutable Exits&, n: sview @source) -> void:
        can Memory.Allocate, Abort.Panic:
            exits.names.push(n)

    def note2(exits: mutable Exits&, n: sview) -> void:
        can Memory.Allocate, Abort.Panic:
            exits.names.push(n)

    def caller() -> void:
        can Memory.Allocate, Abort.Panic:
            exits: mutable Exits = Exits{}
            d: mutable dstr = []
            d.push(65)
            note2(exits, d.as_sview())
`, []string{":18:30"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
