package main

import (
	"strings"
	"testing"
)

// Regression for the "comprehension-seeded darray grown through a by-ref grower" region
// crash: a `mutable darray[T]` local initialized from a LIST COMPREHENSION (instead of `[]`)
// and then passed to a grower (`mutable darray[T]&` callee that pushes) used to trip
// `assert a.end != null` in arena_realloc, exactly like the bare-`[]` shapes covered by
// TestRegionGrowerWithLocalAllocRuns / TestRegionGrowerByRefThenExtendRuns.
//
// The comprehension lowering emits the build into a SYNTHETIC result variable
// (`list.comp.result.N`), so the initial backing is allocated under a name that has no
// darrayStackTag entry — while the user variable the result is bound to IS stack-tagged to
// its own parallel arena. The backing then straddles two arenas once growth continues under
// the user variable's tag (in-function or through a grower callee).
//
// Shape (reduced from the stage1 semantic checker's check_func: `fn_scope: mutable
// darray[sview] = [p.name for p in params]` + check_dup_locals(fn_scope) — the workaround
// there was replacing the comprehension with `[]` + explicit push loop):
//   - consume() seeds `out` from a 1-element comprehension over a darray param,
//   - poke(out) is a grower (local alloc + push into the forwarded ref),
//   - the copy loop crosses the realloc boundary — where the abort fires.
func TestRegionGrowerComprehensionSeedRuns(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def build_body() -> darray[u8] can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        body: mutable darray[u8] = []
        i: mutable usize = 0
        while i < 280:
            body.push(65)
            i <- i + 1
        return body

def poke(out: mutable darray[u8]&) -> void can[Memory.Allocate, Abort.Panic]:
    tmp: mutable darray[u8] = []
    tmp.push(9)
    out.push(88)

def consume(body: darray[u8], params: darray[u8]) -> usize can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[u8] = [p for p in params]
        poke(out)
        i: mutable usize = 0
        while i < body.count:
            out.push(body[i])
            i <- i + 1
        return out.count

def main() -> int can[Memory.Allocate, Abort.Panic]:
    body: darray[u8] = build_body()
    params: darray[u8] = [7]
    n: usize = consume(body, params)
    return n.int()
`)
	if strings.Contains(out, "assert failed") {
		t.Fatalf("comprehension-seeded grower crash REGRESSED (arena assert): status=%s out=%q", status, out)
	}
	// consume returns 282 (= 1 seed + 1 poke push + 280 copied); 282 mod 256 = 26.
	if status != "RUNERR" || !strings.Contains(out, "exit status 26") {
		t.Fatalf("comprehension-seeded grower: expected clean exit with code 26 (consume returns 282), "+
			"got status=%s out=%q", status, out)
	}
}
