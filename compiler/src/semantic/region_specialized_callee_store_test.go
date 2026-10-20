package semantic

import (
	"strings"
	"testing"
)

// A call that binds only region parameters (`keep[r](h, xs[0])`) resolves to its callee, so the
// callee's store summary decides whether a borrow of a local element can reach a longer-lived
// argument: a callee that stores it is rejected, one that never stores it is accepted.
func TestRegionSpecializedCallUsesCalleeStoreSummary(t *testing.T) {
	r := analyzeContractStrict(t, "region_specialized_store.elisa", `struct Ctx:
    n: i64

struct Holder[@r]:
    ctxs: mutable darray[Ctx&] @r

def keep[@r](h: mutable Holder[r]& @r, c: Ctx& @r) -> void:
    can Memory.Allocate:
        h.ctxs.push(c)

def ignore[@r](h: mutable Holder[r]& @r, c: Ctx& @r) -> void:
    can Memory.Allocate:
        h.ctxs.clear()

def stores[@r](h: mutable Holder[r]& @r) -> void:
    can Memory.Allocate:
        xs: mutable darray[Ctx] @r = [Ctx{n: 1}]
        keep[r](h, xs[0])

def drops[@r](h: mutable Holder[r]& @r) -> void:
    can Memory.Allocate:
        xs: mutable darray[Ctx] @r = [Ctx{n: 1}]
        ignore[r](h, xs[0])
`)
	var stores, drops bool
	for _, e := range r.Errors() {
		if !strings.Contains(e, "function-local storage") {
			continue
		}
		stores = stores || strings.Contains(e, ":18:")
		drops = drops || strings.Contains(e, ":23:")
	}
	if !stores {
		t.Fatalf("a callee that stores the local borrow was accepted: %s", strings.Join(r.Errors(), "\n"))
	}
	if drops {
		t.Fatalf("a callee that never stores the borrow was rejected: %s", strings.Join(r.Errors(), "\n"))
	}
}

// A wrapper's own store summary reads through the region-specialized calls in its body: wrapping
// a storing callee still stores, wrapping one that never stores does not.
func TestRegionSpecializedCallInsideWrapperSummary(t *testing.T) {
	r := analyzeContractStrict(t, "region_specialized_wrapper.elisa", `struct Ctx:
    n: i64

struct Holder[@r]:
    ctxs: mutable darray[Ctx&] @r

def keep[@r](h: mutable Holder[r]& @r, c: Ctx& @r) -> void:
    can Memory.Allocate:
        h.ctxs.push(c)

def ignore[@r](h: mutable Holder[r]& @r, c: Ctx& @r) -> void:
    can Memory.Allocate:
        h.ctxs.clear()

def wrap_keep[@r](h: mutable Holder[r]& @r, c: Ctx& @r) -> void:
    can Memory.Allocate:
        keep[r](h, c)

def wrap_ignore[@r](h: mutable Holder[r]& @r, c: Ctx& @r) -> void:
    can Memory.Allocate:
        ignore[r](h, c)

def stores[@r](h: mutable Holder[r]& @r) -> void:
    can Memory.Allocate:
        xs: mutable darray[Ctx] @r = [Ctx{n: 1}]
        wrap_keep[r](h, xs[0])

def drops[@r](h: mutable Holder[r]& @r) -> void:
    can Memory.Allocate:
        xs: mutable darray[Ctx] @r = [Ctx{n: 1}]
        wrap_ignore[r](h, xs[0])
`)
	var stores, drops bool
	for _, e := range r.Errors() {
		if !strings.Contains(e, "function-local storage") {
			continue
		}
		stores = stores || strings.Contains(e, ":26:")
		drops = drops || strings.Contains(e, ":31:")
	}
	if !stores {
		t.Fatalf("a wrapper around a storing callee was accepted: %s", strings.Join(r.Errors(), "\n"))
	}
	if drops {
		t.Fatalf("a wrapper around a non-storing callee was rejected: %s", strings.Join(r.Errors(), "\n"))
	}
}
