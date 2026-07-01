package main

import (
	"strings"
	"testing"
)

// KNOWN-BUG regression for the "grower + local allocator" region-inference crash.
//
// A function that BOTH grows a forwarded `mutable darray[T]&` param (so region inference
// stamps it with a forwarded-param region `@__rg_out`, but does NOT make it
// RegionPolymorphic and gives it no `__region_auto`) AND allocates a FRESH local darray
// mis-threads regions: the caller's later growth of its own darray across a realloc
// boundary (256 -> 512) trips `assert a.end == null` inside arena_realloc and aborts.
//
// Minimal shape (reduced from a live Elisa-LSP publishDiagnostics crash):
//   - build_body() returns a >256-byte darray by value (region-poly builder),
//   - poke(out: mutable darray[u8]&) both allocates a local `tmp` AND pushes into `out`
//     (it is the COMBINATION that misfires: growing-only or local-alloc-only are both fine),
//   - consume(body) makes a local `out`, calls poke(out), then copies body into `out`,
//     crossing the 256->512 realloc boundary — where the abort fires.
//
// Root cause is localized (see the analysis handed to the repo) to the interaction of
// analyzer_region_param_inference.go (forwarded-param `@__rg` stamping) with the
// void-grower ambient-region binding in the backend
// (llvm_bodies_valuebinding_to_clonepackedviewbindingmap.go:574-582 /
// regionPolyAutoAdopts at llvm_bodies_clonecapturedcodegenscope_to_emitstmt.go:280).
// A correct fix requires deeper region-dataflow work (a re-wrapping pass) and is NOT
// attempted here — this test PINS THE CURRENT BUGGY BEHAVIOR so it stays visible and the
// suite stays green.
//
// WHEN YOU FIX THE BUG: this test will start failing (the abort will stop happening).
// That is expected — flip it to assert the correct result: status=="RUNERR" with
// out==" exit status 25" (consume returns 281 = 1 + 280; the shell reports 281 mod 256 = 25).
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
	// CURRENT (buggy) behavior: aborts with an arena assert instead of returning 281.
	if status != "RUNERR" || !strings.Contains(out, "assert failed") {
		t.Fatalf("region grower+local-alloc bug appears FIXED (got status=%s out=%q). "+
			"Flip this test to assert the correct result: status==RUNERR, out==\" exit status 25\".", status, out)
	}
}
