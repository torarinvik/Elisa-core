//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A pure view-returning ghost is read through its body in a view equality, so a postcondition
// stated through it proves, while a wrong claim or an unguarded call outside its requires does not.
func TestSMTViewEquationInlining(t *testing.T) {
	const helpers = `
ghost def idx(xs: darray[sview]&, ys: darray[sview]&, name: sview) -> usize:
    ensure result < xs.count implies result < ys.count
    return 0 if xs.count > 0 and ys.count > 0 and xs[0] == name else xs.count

ghost def val(xs: darray[sview]&, ys: darray[sview]&, name: sview) -> sview:
    return ys[idx(xs, ys, name)] if idx(xs, ys, name) < xs.count and idx(xs, ys, name) < ys.count else ""
`
	for _, tc := range []struct {
		name, body string
		proven     bool
	}{
		{"through_body", `
ghost def f(xs: darray[sview]&, ys: darray[sview]&, name: sview) -> sview:
    ensure (idx(xs, ys, name) < xs.count and val(xs, ys, name) == name) implies result == ""
    return name if idx(xs, ys, name) >= xs.count
    return "" if ys[idx(xs, ys, name)] == name
    return name
`, true},
		{"wrong_claim", `
ghost def f(xs: darray[sview]&, ys: darray[sview]&, name: sview) -> sview:
    ensure (idx(xs, ys, name) < xs.count and val(xs, ys, name) == name) implies result == name
    return name if idx(xs, ys, name) >= xs.count
    return "" if ys[idx(xs, ys, name)] == name
    return name
`, false},
		{"outside_requires", `
ghost def at(xs: darray[sview]&, k: usize) -> sview:
    requires k < xs.count
    return xs[k]

def g(xs: darray[sview]&, k: usize) -> bool:
    ensure result == true
    return at(xs, k) == xs[k]
`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := analyzeContractStrict(t, "view_equation_"+tc.name+".elisa", helpers+tc.body)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the view postcondition to prove: %s", es)
			}
			if !tc.proven && es == "" {
				t.Fatal("an unprovable view claim was accepted")
			}
		})
	}
}
