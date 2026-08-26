package semantic

import (
	"strings"
	"testing"
)

// Forwarding a growable ref param to a MODULE MEMBER through its qualified name must thread the
// region, exactly as the unqualified and top-level spellings already did.
//
// The regression this guards: region-param inference resolved call targets through a map keyed by
// the FuncDecl's Name, which flattening leaves BARE ("stow") while a `::` call site parses to one
// joined Ident ("Box.stow"). No module member ever matched, so the caller never learned it had to
// become region-polymorphic, and the call died at "cannot infer region parameter" -- rejecting a
// program that the same module accepts when the sibling is called unqualified, and that the
// self-hosted stage1 compiles and runs correctly.
//
// It blocked module blocks for every growable API: `Display::rect(list, ...)` was unreachable from
// any function holding `list` as a parameter, which is every renderer.
func TestRegionParamForwardsThroughModuleQualifiedCall(t *testing.T) {
	// Param names deliberately DIFFER across the hop (`bag` -> `s`): the binding must come from the
	// argument's region, not from the callee's synthesized `__rg_s` happening to match a caller param
	// of the same name.
	qualified := analyzeTreeTestSourceWithSemanticErrors(t, "region_module_qualified.elisa", `module Box:
    struct Sack:
        items: mutable darray[i64]

    def stow(s: mutable Sack&, v: i64) -> void:
        s.items.push(v)

def fill(bag: mutable Box::Sack&) -> void:
    Box::stow(bag, 12)
`)
	if all := strings.Join(qualified.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("forwarding to a module member by qualified name must thread the region; got: %s", all)
	}

	// The mirror image: registering the qualified key must not cost the bare one, which is what an
	// unqualified sibling call and every top-level call site write.
	sibling := analyzeTreeTestSourceWithSemanticErrors(t, "region_module_sibling.elisa", `module Box:
    struct Sack:
        items: mutable darray[i64]

    def stow(s: mutable Sack&, v: i64) -> void:
        s.items.push(v)

    def fill(bag: mutable Sack&) -> void:
        stow(bag, 12)
`)
	if all := strings.Join(sibling.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("an unqualified sibling forward must keep threading the region; got: %s", all)
	}

	topLevel := analyzeTreeTestSourceWithSemanticErrors(t, "region_module_toplevel.elisa", `struct Sack:
    items: mutable darray[i64]

def stow(s: mutable Sack&, v: i64) -> void:
    s.items.push(v)

def fill(bag: mutable Sack&) -> void:
    stow(bag, 12)
`)
	if all := strings.Join(topLevel.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("a plain top-level forward must keep threading the region; got: %s", all)
	}
}

// The forward must resolve ACROSS modules too, not only from top level into one.
func TestRegionParamForwardsBetweenModules(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_module_cross.elisa", `module Box:
    struct Sack:
        items: mutable darray[i64]

    def stow(s: mutable Sack&, v: i64) -> void:
        s.items.push(v)

module Crate:
    def fill(bag: mutable Box::Sack&) -> void:
        Box::stow(bag, 12)
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("a cross-module qualified forward must thread the region; got: %s", all)
	}
}

// Two modules may each declare a member of the same name. The qualified key must pick the one the
// call site names -- and it can never be shadowed by the bare key, because an identifier cannot
// contain a dot, so "Box.stow" is unreachable as a bare name.
func TestRegionParamQualifiedForwardPicksTheNamedModule(t *testing.T) {
	res := analyzeTreeTestSourceWithSemanticErrors(t, "region_module_samename.elisa", `module Box:
    struct Sack:
        items: mutable darray[i64]

    def stow(s: mutable Sack&, v: i64) -> void:
        s.items.push(v)

module Bin:
    def stow(n: i64, v: i64) -> i64:
        return n + v

def fill(bag: mutable Box::Sack&) -> void:
    Box::stow(bag, 12)
`)
	if all := strings.Join(res.Errors(), "\n"); strings.Contains(all, "cannot infer region parameter") {
		t.Fatalf("a same-named member in another module must not shadow the qualified target; got: %s", all)
	}
}
