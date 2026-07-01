package main

import (
	"strings"
	"testing"
)

// Regression for the "grower + local allocator" region-inference crash (FIXED).
//
// A function that BOTH grows a forwarded `mutable darray[T]&` param AND allocates a FRESH
// local darray used to mis-thread regions. The caller's `out` darray is a multi-stack
// darray routed to its own parallel arena (`__auto#1`) via darrayStackTag, so all of the
// caller's OWN growth ops allocate there. But when `out` was passed to the grower, the call
// site's hidden-Arena resolution (resolveRegionArenaArgs) ignored darrayStackTag and threaded
// the caller's *base* ambient arena instead — so the grower allocated `out`'s first backing
// in the base arena while the caller later realloc'd it in the parallel arena. The realloc
// then straddled two arenas and tripped `assert a.end != null` inside arena_realloc.
//
// Minimal shape (reduced from a live Elisa-LSP publishDiagnostics crash):
//   - build_body() returns a >256-byte darray by value (region-poly builder),
//   - poke(out: mutable darray[u8]&) both allocates a local `tmp` AND pushes into `out`
//     (it is the COMBINATION that misfires: growing-only or local-alloc-only are both fine),
//   - consume(body) makes a local `out`, calls poke(out), then copies body into `out`,
//     crossing the 256->512 realloc boundary — where the abort used to fire.
//
// Fix: resolveRegionArenaArgs
// (llvm_support_createentryalloca_to_invalidatepackedvariantview.go) now consults
// darrayStackTag for the forwarded argument, exactly like darrayGrowthOwner, so the grower
// threads the SAME parallel arena the caller grows into. `consume` returns 281 (= 1 + 280);
// as a process exit code that is 281 mod 256 = 25, which s4CompileRun reports as RUNERR
// with output " exit status 25".
func TestRegionGrowerWithLocalAllocRuns(t *testing.T) {
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

def consume(body: darray[u8]) -> usize can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[u8] = []
        poke(out)
        i: mutable usize = 0
        while i < body.count:
            out.push(body[i])
            i <- i + 1
        return out.count

def main() -> int can[Memory.Allocate, Abort.Panic]:
    body: darray[u8] = build_body()
    n: usize = consume(body)
    return n.int()
`)
	// `consume` returns 281; as a process exit code that is 281 mod 256 = 25.
	if status != "RUNERR" || !strings.Contains(out, "exit status 25") {
		t.Fatalf("region grower+local-alloc: expected clean exit with code 25 (consume returns 281), "+
			"got status=%s out=%q", status, out)
	}
	if strings.Contains(out, "assert failed") {
		t.Fatalf("region grower+local-alloc crash REGRESSED (arena assert): status=%s out=%q", status, out)
	}
}
