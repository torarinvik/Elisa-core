package semantic

import (
	"strings"
	"testing"
)

// A BARE sibling call inside a module must be visible to region-poly CLASSIFICATION, so
// the caller's body gets a region wrapped around it.
//
// lookupVisibleGlobal finds a module member only through a.currentNamespace, which is not
// set during that pre-pass, so the callee was invisible and the call then failed at
// analysis with "must occur where a region can be inferred" -- for a program that compiles
// unchanged when the same two functions sit at top level. nw-core's syllogism hit it the
// moment it became a module: `sort_labels` calls `label_text`, which builds a dstr.
func TestRegionPolyClassificationSeesBareSiblingCall(t *testing.T) {
	module := analyzeTreeTestSourceWithSemanticErrors(t, "region_poly_sibling_module.elisa", `module Sy:
    def label_text(v: i64) -> dstr:
        out: mutable dstr = []
        out.push(65.u8() + v.u8())
        return out

    def sort_labels(count: i64, out: mutable darray[i64]&) -> void:
        index: mutable i64 = 0
        while index < count |index, count, out|:
            held_text: dstr = label_text(index)
            out[index.usize()] <- held_text.count.i64()
            index <- index + 1
`)
	if all := strings.Join(module.Errors(), "\n"); strings.Contains(all, "region can be inferred") {
		t.Fatalf("a bare sibling call must be seen by region-poly classification; got: %s", all)
	}

	// The same two functions at top level, which always worked -- so a regression here
	// shows whether the fallback broke the path it was modelled on.
	flat := analyzeTreeTestSourceWithSemanticErrors(t, "region_poly_sibling_flat.elisa", `def sy_label_text(v: i64) -> dstr:
    out: mutable dstr = []
    out.push(65.u8() + v.u8())
    return out

def sy_sort_labels(count: i64, out: mutable darray[i64]&) -> void:
    index: mutable i64 = 0
    while index < count |index, count, out|:
        held_text: dstr = sy_label_text(index)
        out[index.usize()] <- held_text.count.i64()
        index <- index + 1
`)
	if all := strings.Join(flat.Errors(), "\n"); strings.Contains(all, "region can be inferred") {
		t.Fatalf("the top-level form must keep working; got: %s", all)
	}
}
