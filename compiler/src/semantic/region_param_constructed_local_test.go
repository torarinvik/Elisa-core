package semantic

import (
	"strings"
	"testing"
)

// A struct local constructed from a builder call or a struct literal, whose `&` is forwarded to a
// region-polymorphic growth callee, gets a synthesized function auto-region threaded into the callee
// (was: "cannot infer region parameter __rg_b"). The parser wraps such a body in an auto-region
// (functionBodyNeedsAutoRegion -> bodyForwardsConstructedStructLocal) and recordStructLocalAllocRegion
// binds the local to it. SOUNDNESS is preserved by the escape checker: a borrow of the grown field
// that outlives the frame is still rejected.

// Builder-returned struct local forwarded to a growth callee: pure inference, no annotation.
func TestConstructedLocalBuilderForwardedInferred(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "builder_local.elisa",
		`struct Bag:
    items: mutable darray[i64]
def Bag_new() -> Bag:
    return Bag{items: zeroed}
def grow(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)
def use() -> void:
    bag: mutable Bag = Bag_new()
    grow(&bag, 7)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("builder-returned struct local forwarded to a growth callee must infer region-poly, got: %s", errs)
	}
}

// Struct-literal local (with `zeroed`/empty container fields) forwarded to a growth callee: same.
func TestConstructedLocalLiteralForwardedInferred(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "literal_local.elisa",
		`struct Bag:
    items: mutable darray[i64]
def grow(b: mutable Bag&, v: i64) -> void:
    b.items.push(v)
def use() -> void:
    bag: mutable Bag = Bag{items: zeroed}
    grow(&bag, 7)
`).Errors(), " | ")
	if errs != "" {
		t.Fatalf("struct-literal local forwarded to a growth callee must infer region-poly, got: %s", errs)
	}
}

// SOUNDNESS GATE: a borrow of the grown field returned region-less (so it would outlive the
// frame-local `bag`) must STILL be rejected — the auto-region inference must not enable a dangle.
func TestConstructedLocalGrownBorrowEscapeStillRejected(t *testing.T) {
	errs := strings.Join(analyzeTreeTestSourceWithSemanticErrors(t, "builder_escape.elisa",
		`struct Bag:
    items: mutable darray[i64]
def Bag_new() -> Bag:
    return Bag{items: zeroed}
def grow(b: mutable Bag&, v: i64) -> view[i64]:
    b.items.push(v)
    return b.items[0:1]
def leak() -> view[i64]:
    bag: mutable Bag = Bag_new()
    return grow(&bag, 7)
`).Errors(), " | ")
	if errs == "" {
		t.Fatalf("returning a borrow of a frame-local struct's grown field must be rejected (dangling)")
	}
}
