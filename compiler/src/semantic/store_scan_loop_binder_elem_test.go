package semantic

import "testing"

// A by-value loop binder over a parameter field chain is a copy of one element: a scalar element
// stores nothing (lb1); a view element still does (lb2); a binder whose name is bound twice is
// not typed from the loop (lb3).
func TestStoreScanLoopBinderElement(t *testing.T) {
	head := `struct Scope:
    names: mutable darray[sview] = []
    marks: mutable darray[usize] = []

struct Out:
    marks: mutable darray[usize] = []
    held: mutable darray[sview] = []

def caller(out: mutable Out&) -> void:
    can Memory.Allocate, Abort.Panic:
        sc: mutable Scope = Scope{names: [], marks: []}
        d: mutable dstr = []
        d.push(65)
        sc.names.push(d.as_sview())
        sc.marks.push(1)
        publish(sc, out)

`
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"lb1", `def publish(sc: Scope&, out: mutable Out&) -> void:
    can Memory.Allocate, Abort.Panic:
        for mark in sc.marks |out| -> out:
            out.marks <- out.marks.push(mark)
`, nil},
		{"lb2", `def publish(sc: Scope&, out: mutable Out&) -> void:
    can Memory.Allocate, Abort.Panic:
        for name in sc.names |out| -> out:
            out.held <- out.held.push(name)
`, []string{":16:"}},
		{"lb3", `def publish(sc: Scope&, out: mutable Out&) -> void:
    can Memory.Allocate, Abort.Panic:
        for mark in sc.marks |out| -> out:
            out.marks <- out.marks.push(mark)
        for mark in sc.names |out| -> out:
            out.held <- out.held.push(mark)
`, []string{":16:"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", head+c.body)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
