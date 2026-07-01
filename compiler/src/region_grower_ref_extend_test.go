package main

import (
	"strings"
	"testing"
)

// Regression for the "stack-tagged darray grown through a by-reference helper, then
// extend()" region-inference crash (FIXED).
//
// Distinct from TestRegionGrowerWithLocalAllocRuns: there the forwarded argument reaches
// resolveRegionArenaArgs as a bare identifier. Here the darray is grown by a UFCS/by-ref
// helper call (`body.emit()` lowers to `emit(&body)`), so the argument is an *ast.AddrOfExpr
// wrapping the identifier. resolveRegionArenaArgs matched only a bare *ast.Ident, so it
// missed the darrayStackTag and threaded the caller's BASE arena into the helper — while the
// darray itself is routed to its own parallel stack (#1). The helper allocated `body`'s first
// backing in the base arena; the later `extend` reallocated it in the parallel arena; the
// straddling realloc tripped `assert a.end != null`.
//
// Fix: resolveRegionArenaArgs peels ParenExpr/AddrOfExpr (stackTagArgIdentName) before the
// darrayStackTag lookup, so a stack-tagged darray passed by reference to a grower threads its
// OWN stack arena — matching darrayGrowthOwner (whose receiver is already a bare Ident).
//
// Shape (reduced from the live Elisa-LSP publishDiagnostics crash, `extend` variant):
//   - emit(buf) grows a by-reference `mutable darray[u8]&` (like the LSP's push_str),
//   - make_entry() returns a >256-byte darray by value (region-poly builder),
//   - build() makes a local `body`, grows it via the UFCS helper `body.emit()`, then
//     `body.extend(entry)` crosses the realloc boundary — where the abort used to fire.
//
// build() returns 301 (= 1 + 300); as a process exit code that is 301 mod 256 = 45.
func TestRegionGrowerByRefThenExtendRuns(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def emit(buf: mutable darray[u8]&) -> void can[Memory.Allocate, Abort.Panic]:
    buf.push(123)

def make_entry() -> darray[u8] can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        e: mutable darray[u8] = []
        i: mutable usize = 0
        while i < 300:
            e.push(66)
            i <- i + 1
        return e

def build() -> usize can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        body: mutable darray[u8] = []
        body.emit()
        entry: darray[u8] = make_entry()
        body.extend(entry)
        return body.count

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return build().int()
`)
	if strings.Contains(out, "assert failed") {
		t.Fatalf("by-ref grower + extend crash REGRESSED (arena assert): status=%s out=%q", status, out)
	}
	// build() returns 301; as a process exit code that is 301 mod 256 = 45.
	if status != "RUNERR" || !strings.Contains(out, "exit status 45") {
		t.Fatalf("by-ref grower + extend: expected clean exit with code 45 (build returns 301), "+
			"got status=%s out=%q", status, out)
	}
}
