package main

import "testing"

// Repro: a module-scoped region-polymorphic builder returns a packed-enum value whose
// payload carries a darray of nested packed-enum values; a region-less caller then
// dereferences the children. Works at TOP LEVEL but SEGFAULTS when the builder is in a
// `module` (the packed-enum store region isn't adopted across the namespaced call).
// Expected once fixed: exit 30 (10 + 20).
func TestPackedEnumReturnModuleRegion(t *testing.T) {
	prog := `enum Node layout(handle: u32):
    pass

module M:
    public:
        enum Val is Node:
            Leaf(n: i64)
            Arr(items: darray[Val])

        def build() -> Val can[Memory.Allocate, Abort.Panic]:
            items: mutable darray[Val] = []
            items.push(Val.Leaf(10))
            items.push(Val.Leaf(20))
            return (Val.Arr(items))

def main() -> int can[Memory.Allocate, Abort.Panic]:
    v: M::Val = M::build()
    s: mutable i64 = 0
    match v:
        M::Val.Arr(items):
            for it in items:
                match it:
                    M::Val.Leaf(n):
                        s <- s + n
                    _:
                        pass
        _:
            pass
    return s.int()
`
	status, out := s4CompileRun(t, prog)
	t.Logf("status=%s out=%q", status, out)
	if status != "RUNERR" || out != " exit status 30" {
		t.Fatalf("module packed-enum region return: got status=%s out=%q, want exit 30", status, out)
	}
}
