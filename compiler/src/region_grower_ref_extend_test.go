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

// KNOWN-OPEN stage0 bug (task_00a7fdf3) — reproducer, currently SKIPPED so the suite stays
// green until the fix lands. Remove the t.Skip when fixing.
//
// A NON-EMPTY-seeded local darray (`xs = [0,1,2,3]` OR `xs = [k for k in 0..<4]` — the seed
// being a literal or a comprehension makes no difference) grown through a callee `mutable&`
// past ~one region's capacity aborts in arena_realloc: `assert a.end != null` (arena.elisa,
// RESERVE_COMMIT/FIXED path). The empty-seed form (`xs = []` then push) is SAFE — that is why
// the stage1 resolver's `bound = []` + push-loop workaround (semantic/resolve.elisa,
// semantic/symbols.elisa) sidesteps it and MUST stay until this is fixed.
//
// Narrowed trigger (matrix-tested): crash requires (a) a non-empty initial seed AND (b) growth
// that crosses one region's capacity (~256 i64 slots — grow-to-200 passes, grow-to-300 aborts).
// The initial seeded backing is threaded to the region-poly grower with an arena tag whose
// region chain isn't reachable at the realloc-into-a-new-region site (same class as the
// by-reference-grower fix above, but for the seeded-initialization path — resolveRegionArenaArgs
// / the darray growth-owner tag for a seeded literal, rather than an empty `[]`).
//
// grow(out) pushes 300 entries into a by-ref `mutable darray[i64]&`; caller seeds `xs` from a
// comprehension then grows it. build() returns 300 + 4 = 304; exit code 304 mod 256 = 48.
func TestRegionGrowerSeededThenGrowRuns(t *testing.T) {
	t.Parallel()
	t.Skip("KNOWN-OPEN task_00a7fdf3: non-empty-seeded local grown past a region boundary aborts in arena_realloc; empty-seed workaround in stage1 stays until fixed")
	status, out := s4CompileRun(t, `def grow(out: mutable darray[i64]&) -> void can[Memory.Allocate, Abort.Panic]:
    i: mutable i64 = 0
    while i < 300:
        out.push(i)
        i <- i + 1

def build() -> usize can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = [k * 2 for k in 0..<4]
        grow(&xs)
        return xs.count

def main() -> int can[Memory.Allocate, Abort.Panic]:
    return build().int()
`)
	if strings.Contains(out, "assert failed") {
		t.Fatalf("comprehension-seeded grower crash REGRESSED (arena assert): status=%s out=%q", status, out)
	}
	if status != "RUNERR" || !strings.Contains(out, "exit status 48") {
		t.Fatalf("comprehension-seeded grower: expected clean exit with code 48 (build returns 304), "+
			"got status=%s out=%q", status, out)
	}
}
