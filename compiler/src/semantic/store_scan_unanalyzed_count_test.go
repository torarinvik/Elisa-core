package semantic

import "testing"

// A callee analyzed after its caller has no exprTypes: a parameter field chain's builtin
// .count is still a scalar and stores nothing (dc1); a view element of the same chain does (dc2).
func TestStoreScanUnanalyzedCalleeContainerCount(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"count", `struct Scope:
    names: mutable darray[sview] = []
    deferred: mutable darray[sview] = []

struct Loops:
    marks: mutable darray[usize] = []

def caller(loops: mutable Loops&) -> void:
    can Memory.Allocate, Abort.Panic:
        sc: mutable Scope = Scope{names: [], deferred: []}
        d: mutable dstr = []
        d.push(65)
        sc.deferred.push(d.as_sview())
        enter_loop(sc, loops)

def enter_loop(sc: mutable Scope&, loops: mutable Loops&) -> void:
    can Memory.Allocate, Abort.Panic:
        loops.marks <- loops.marks.push(sc.deferred.count)
`, nil},
		{"view", `struct Scope:
    names: mutable darray[sview] = []
    deferred: mutable darray[sview] = []

struct Loops:
    marks: mutable darray[usize] = []
    held: mutable darray[sview] = []

def caller(loops: mutable Loops&) -> void:
    can Memory.Allocate, Abort.Panic:
        sc: mutable Scope = Scope{names: [], deferred: []}
        d: mutable dstr = []
        d.push(65)
        sc.deferred.push(d.as_sview())
        enter_loop(sc, loops)

def enter_loop(sc: mutable Scope&, loops: mutable Loops&) -> void:
    can Memory.Allocate, Abort.Panic:
        loops.marks <- loops.marks.push(sc.deferred.count)
        loops.held <- loops.held.push(sc.deferred[0])
`, []string{":15:20"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
