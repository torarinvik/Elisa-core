package main

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// Property-based fuzzer for AUTOMATIC REGION INFERENCE.
//
// Elisa infers regions by default: a function body with region-less allocations gets a
// synthesized auto region, and loop-body allocations are auto-tightened into a
// per-iteration region. Objects with DIFFERENT lifetimes are expressed by NESTING —
// a value that should die earlier lives in an inner (loop-body) scope; longer-lived
// values in the enclosing one.
//
// This fuzzer generates random trees of nested scopes. Each node:
//   - allocates a darray in its own (inferred) region,
//   - pushes a run of linear values into it,
//   - recursively emits child scopes whose darrays die at their inner scope exit
//     while the child's computed scalar sum propagates up into THIS node's darray,
//   - optionally repeats its whole region block N times (region create/destroy churn).
//
// The exact final checksum is computed independently in Go (contribution()). The
// generated `@test` panics if its runtime sum disagrees — so any region-inference or
// codegen bug (freeing a live region, growing the wrong arena, a use-after-free of a
// dead inner region, mis-sized region) shows up as a checksum mismatch or a crash.
// On failure the seed AND the full generated program are logged for reproduction.

type rnode struct {
	count    int // number of linear values pushed into this node's darray
	base     int // first value
	step     int // increment between values
	rep      int // how many times this node's whole region block repeats (>=1)
	children []*rnode
}

func genRegionNode(rng *rand.Rand, depth int) *rnode {
	n := &rnode{
		count: 1 + rng.Intn(120),
		base:  rng.Intn(20),
		step:  rng.Intn(4),
		rep:   1 + rng.Intn(3),
	}
	if depth > 0 {
		for kids := rng.Intn(4); kids > 0; kids-- {
			n.children = append(n.children, genRegionNode(rng, depth-1))
		}
	}
	return n
}

// contribution mirrors the generated program's arithmetic exactly: each repeat of a
// node's block sums its own linear run plus every child's contribution.
func contribution(n *rnode) int64 {
	c := int64(n.count)
	own := c*int64(n.base) + int64(n.step)*(c-1)*c/2
	var kids int64
	for _, ch := range n.children {
		kids += contribution(ch)
	}
	return int64(n.rep) * (own + kids)
}

func regionIndent(n int) string { return strings.Repeat("    ", n) }

// emitRegionNode writes the node's region block. `sink` is a format string with one
// %s, emitted as the block's final statement, that delivers the node's scalar sum to
// its parent (e.g. `d3.push(%s)` for a child, `root <- root + %s` for the root).
func emitRegionNode(b *strings.Builder, n *rnode, indent int, ctr *int, sink string) {
	id := *ctr
	*ctr++
	autoIndent := indent
	if n.rep > 1 {
		fmt.Fprintf(b, "%sfor rep%d in 0..<%d:\n", regionIndent(indent), id, n.rep)
		autoIndent = indent + 1
	}
	inner := autoIndent
	d := fmt.Sprintf("d%d", id)
	fmt.Fprintf(b, "%s%s: mutable darray[i64] = []\n", regionIndent(inner), d)
	fmt.Fprintf(b, "%sfor k%d in 0..<%d:\n", regionIndent(inner), id, n.count)
	fmt.Fprintf(b, "%s%s.push(%d.i64() + k%d.i64() * %d.i64())\n", regionIndent(inner+1), d, n.base, id, n.step)
	for _, ch := range n.children {
		emitRegionNode(b, ch, inner, ctr, d+".push(%s)")
	}
	fmt.Fprintf(b, "%ss%d: mutable i64 = 0\n", regionIndent(inner), id)
	fmt.Fprintf(b, "%sfor v%d in %s:\n", regionIndent(inner), id, d)
	fmt.Fprintf(b, "%ss%d <- s%d + v%d\n", regionIndent(inner+1), id, id, id)
	fmt.Fprintf(b, "%s"+sink+"\n", regionIndent(inner), fmt.Sprintf("s%d", id))
}

func generateRegionProgram(seed int64) (string, int64) {
	rng := rand.New(rand.NewSource(seed))
	root := genRegionNode(rng, 3)
	expected := contribution(root)
	var b strings.Builder
	b.WriteString("@test\ndef fuzz_regions() -> void:\n")
	b.WriteString("    can Memory.Allocate, Memory.Release, Abort.Panic:\n")
	b.WriteString("        root: mutable i64 = 0\n")
	ctr := 0
	emitRegionNode(&b, root, 2, &ctr, "root <- root + %s")
	fmt.Fprintf(&b, "        if root != %d:\n", expected)
	fmt.Fprintf(&b, "            panic(\"region inference checksum mismatch (seed %d)\")\n", seed)
	return b.String(), expected
}

// Explicit differing-lifetime test: a long-lived darray in the outer inferred region
// survives while 50 short-lived per-iteration (auto-tightened) regions are created,
// filled, summed, and freed. If the inner region's teardown corrupted the surviving outer darray (or
// the outer darray were grown from the wrong/short-lived arena), the checksum breaks.
func TestRegionInferenceDifferingLifetimes(t *testing.T) {
	if testing.Short() {
		t.Skip("native build; skipped under -short")
	}
	body := `
@test
def differing_lifetimes() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        longlived: mutable darray[i64] = []
        for round in 0..<50:
            scratch: mutable darray[i64] = []
            for k in 0..<100:
                scratch.push((round.i64() * 100) + k.i64())
            s: mutable i64 = 0
            for v in scratch:
                s <- s + v
            longlived.push(s)
        total: mutable i64 = 0
        for v in longlived:
            total <- total + v
        if total != 12497500:
            panic("long-lived value corrupted or scratch use-after-free")
`
	exit, stdout, stderr := runStressProgram(t, "differing_lifetimes", body)
	assertAllPassed(t, exit, stdout, stderr, "differing_lifetimes")
}

// Region inference must REJECT a value that outlives its region when the region is an
// EXPLICIT, named, function-local region (`region r(...):`) — it is freed at scope exit and
// is not adopted by the caller, so the returned darray would dangle. (Contrast: a value built
// in the DEFAULT inferred region and returned is now a sound region-polymorphic builder — the
// `__auto_*` region is threaded+adopted from the caller; see TestBuildLocalReturnAdoptedNoUAF.
// Only the synthesized-auto-region return is adopted; an explicit named local region is not.)
func TestRegionInferenceRejectsEscape(t *testing.T) {
	if testing.Short() {
		t.Skip("native build; skipped under -short")
	}
	body := `
def leak() -> darray[i64]:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        region r(4096):
            xs: mutable darray[i64] @r = []
            xs.push(1)
            return xs
`
	exit, stdout, stderr := runStressProgram(t, "region_escape", body)
	if exit == 0 {
		t.Fatalf("expected a value escaping its explicit named region to be rejected, got exit 0\n%s", stdout)
	}
	if !strings.Contains(stderr, "escapes") && !strings.Contains(stdout, "escapes") {
		t.Fatalf("expected a region-escape diagnostic, got:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// TestRegionInferenceFuzz generates randomized nested-region programs, compiles each
// to native, runs it, and asserts the runtime checksum matches the Go-computed one.
func TestRegionInferenceFuzz(t *testing.T) {
	if testing.Short() {
		t.Skip("heavy fuzz test; skipped under -short")
	}
	for seed := int64(1); seed <= 16; seed++ {
		body, expected := generateRegionProgram(seed)
		exit, stdout, stderr := runStressProgram(t, fmt.Sprintf("region_fuzz_%d", seed), body)
		if exit != 0 || !strings.Contains(stdout, "failed=0") {
			t.Fatalf("seed %d: region-inference fuzz FAILED (exit %d, expected sum %d)\n"+
				"--- generated program ---\n%s\n--- stdout ---\n%s\n--- stderr ---\n%s",
				seed, exit, expected, body, stdout, stderr)
		}
	}
}
