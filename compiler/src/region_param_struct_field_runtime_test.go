package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/backend"
)

// docs/91 S4 end-to-end: growing a container field of a region-param struct ref param, with the
// caller's region threaded to the field growth, runs correctly; and the borrow-out use-after-free
// (returning a region-less view into the grown field, stored past the region) is a COMPILE error,
// not a runtime segfault.

func s4CompileRun(t *testing.T, prog string) (status, output string) {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	dir := t.TempDir()
	root := repoRootFromMainTest(t)
	std := filepath.Join(root, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	rel, _ := filepath.Rel(dir, std)
	fixture := filepath.Join(dir, "p.elisa")
	if err := os.WriteFile(fixture, []byte("# include \""+filepath.ToSlash(rel)+"\"\n"+prog), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stderr bytes.Buffer
	expanded, err := readSourceWithIncludes(fixture, map[string]bool{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	_, res, ok := analyzeProgram(fixture, expanded, &stderr)
	if !ok {
		return "REJECTED", strings.TrimSpace(stderr.String())
	}
	exe, cleanup, err := buildNativeExecutable(res, nil, nil, "", backend.OptimizationLevel0, backend.DefaultPackedLoweringProfile(), "", false, false, &stderr)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return "BUILD-FAIL", err.Error()
	}
	out, runErr := exec.Command(exe).CombinedOutput()
	if runErr != nil {
		return "RUNERR", strings.TrimSpace(string(out)) + " " + runErr.Error()
	}
	return "RAN", strings.TrimSpace(string(out))
}

const s4StructHdr = "struct Mod[@owner]:\n    bits: mutable darray[u8]\n"

func TestS4FieldGrowthRunsEndToEnd(t *testing.T) {
	status, out := s4CompileRun(t, s4StructHdr+`def fill[@r](m: mutable Mod& @r) -> void can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    m.bits.push(66)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(4096):
        m: mutable Mod& @a = new[a] Mod(bits: [])
        fill(m) can Memory.Allocate, Abort.Panic
        print((m.bits[0].i64() + m.bits[1].i64()).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "131" {
		t.Fatalf("expected RAN 131 (caller region threaded to field growth), got %s %q", status, out)
	}
}

// S4 Stage 1 end-to-end: ZERO-annotation `def fill(m: mutable Mod&): m.bits.push(..)` over a plain
// struct runs correctly — the caller's region is threaded to the field growth with no `[@r]` written.
func TestS4Stage1ZeroAnnotationRunsEndToEnd(t *testing.T) {
	status, out := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def fill(m: mutable Mod&) -> void can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    m.bits.push(66)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(4096):
        m: mutable Mod& @a = new[a] Mod(bits: [])
        fill(m) can Memory.Allocate, Abort.Panic
        print((m.bits[0].i64() + m.bits[1].i64()).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "131" {
		t.Fatalf("expected RAN 131 (region inferred, no annotation), got %s %q", status, out)
	}
}

// S4 Stage 2: storing a region-carrying struct BY VALUE in a container is sound when the struct's
// interior field backing lives in the same (or a longer-lived) region as the container — a death
// cohort: the modules and the table die together. Runs end-to-end with a PLAIN struct, no annotation.
func TestS4Stage2ByValueSameRegionCohortRuns(t *testing.T) {
	status, out := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(8192):
        mods: mutable darray[Mod] = []
        m: mutable Mod = Mod(bits: [])
        m.bits.push(65) can Memory.Allocate, Abort.Panic
        mods.push(m) can Memory.Allocate, Abort.Panic
        print(mods[0].bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "65" {
		t.Fatalf("same-region by-value struct cohort must run (65), got %s %q", status, out)
	}
}

// S4 Stage 2 SAFETY: a by-value struct whose field backing was grown in a SHORTER-lived inner region,
// pushed into an outer container, dangles when the inner region dies — must be a COMPILE error, not a
// segfault. The field-growth taint side-table + the by-value element-store consult close this.
func TestS4Stage2ByValueInnerToOuterUAFRejected(t *testing.T) {
	status, _ := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region outer(8192):
        mods: mutable darray[Mod] = []
        region inner(4096):
            m: mutable Mod = Mod(bits: [])
            m.bits.push(65) can Memory.Allocate, Abort.Panic
            mods.push(m) can Memory.Allocate, Abort.Panic
        print(mods[0].bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("pushing an inner-grown by-value struct into an outer darray must be REJECTED (was a segfault), got %s", status)
	}
}

// Same UAF via dict.put (a different container-mutation handler) is also rejected.
func TestS4Stage2ByValueDictPutInnerToOuterUAFRejected(t *testing.T) {
	status, _ := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region outer(8192):
        d: mutable dict[i64, Mod] = {}
        region inner(4096):
            m: mutable Mod = Mod(bits: [])
            m.bits.push(65) can Memory.Allocate, Abort.Panic
            d.put(1, m) can Memory.Allocate, Abort.Panic
        if d.get(1) is hit:
            print(hit.bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("dict.put of an inner-grown by-value struct into an outer dict must be REJECTED (was a segfault), got %s", status)
	}
}

// S4 Stage 2 SAFETY (return vector): returning a by-VALUE struct whose field was grown in a
// scope-owned local region dangles once that region frees at block exit — must be a COMPILE error.
func TestS4Stage2ReturnByValueTaintedStructRejected(t *testing.T) {
	status, _ := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def leak() -> Mod can[Memory.Allocate, Abort.Panic]:
    region inner(4096):
        m: mutable Mod = Mod(bits: [])
        m.bits.push(65) can Memory.Allocate, Abort.Panic
        return m
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    g: mutable Mod = leak() can Memory.Allocate, Abort.Panic
    print(g.bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("returning a by-value struct grown in a local region must be REJECTED (was a segfault), got %s", status)
	}
}

// Same return UAF wrapped in a fresh darray literal (`return [m]`) — the return check descends into
// fresh producers via returnedAggregateTaintRegion.
func TestS4Stage2ReturnListOfTaintedStructRejected(t *testing.T) {
	status, _ := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def leak() -> darray[Mod] can[Memory.Allocate, Abort.Panic]:
    region inner(4096):
        m: mutable Mod = Mod(bits: [])
        m.bits.push(65) can Memory.Allocate, Abort.Panic
        return [m]
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    g: mutable darray[Mod] = leak() can Memory.Allocate, Abort.Panic
    print(g[0].bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("returning a fresh list of a locally-grown by-value struct must be REJECTED (was a segfault), got %s", status)
	}
}

// S4 Stage 3: a reborrow reinterpret cast `(&self).cast[mutable Mod&]` PRESERVES the operand's region,
// so a wrapper forwarding a region-poly struct ref through such a cast threads the caller's region and
// runs — instead of the cast erasing the region and forcing an explicit `in <region>:`.
func TestS4Stage3ReborrowCastPreservesRegion(t *testing.T) {
	status, out := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def grow(self: mutable Mod&) -> void can[Memory.Allocate, Abort.Panic]:
    self.bits.push(65)
    return
def caller(self: mutable Mod&) -> void can[Memory.Allocate, Abort.Panic]:
    grow((&self).cast[mutable Mod&])
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(4096):
        m: mutable Mod& @a = new[a] Mod(bits: [])
        caller(m) can Memory.Allocate, Abort.Panic
        print(m.bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "65" {
		t.Fatalf("a reborrow-cast forward must preserve the region and run (65), got %s %q", status, out)
	}
}

// S4 Stage 3: growing a NESTED container field (`m.inner.items.push(..)`, two field hops) is inferred
// and threads the region through the intermediate struct-value field — paramFieldContainerIsGrown
// matches any field PATH rooted at the param, and stampFieldRegion propagates the region onto a
// nested struct field's RegionOwner so deeper accesses inherit it.
func TestS4Stage3NestedFieldGrowthRuns(t *testing.T) {
	status, out := s4CompileRun(t, "struct Inner:\n    items: mutable darray[u8]\nstruct Mod:\n    inner: mutable Inner\n"+`def fill(m: mutable Mod&) -> void can[Memory.Allocate, Abort.Panic]:
    m.inner.items.push(65)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    m: mutable Mod = Mod(inner: Inner(items: []))
    in perm:
        fill((&m).cast[mutable Mod&]) can Memory.Allocate, Abort.Panic
    print(m.inner.items[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "65" {
		t.Fatalf("nested-field growth must infer + thread the region and run (65), got %s %q", status, out)
	}
}

// S4 Stage 3: a region-poly field-growth callee reached via a reborrow cast inside `in perm:` threads
// the program-lifetime `perm` arena through the whole chain (PascalCase callees included) and runs —
// the region-inference replacement for a per-hop `in perm:`. (Exercises StructLitExpr forwarding,
// the perm ambient-binding, cast region-preservation, threading-tolerant arg assignability, and the
// backend ambient-arena threading together.)
func TestS4Stage3PermAmbientThreadsAndRuns(t *testing.T) {
	status, out := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def Grow(self: mutable Mod&) -> void can[Memory.Allocate, Abort.Panic]:
    self.bits.push(65)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    m: mutable Mod = Mod(bits: [])
    in perm:
        Grow((&m).cast[mutable Mod&])
    print(m.bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "65" {
		t.Fatalf("perm ambient binding must thread through a PascalCase region-poly callee and run (65), got %s %q", status, out)
	}
}

// SOUNDNESS GATE for Stage 3: the ambient binding is RESTRICTED to `perm` (program-lifetime). Binding
// a region param to a SHORTER ambient region (`in inner:`) and then storing the struct past it would
// be a use-after-free the (interprocedural) growth hides from the interior-taint checks — so it is a
// conservative "cannot infer region parameter" COMPILE error, never a miscompile/segfault.
func TestS4Stage3ShortAmbientRegionRejected(t *testing.T) {
	status, _ := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def Grow(self: mutable Mod&) -> void can[Memory.Allocate, Abort.Panic]:
    self.bits.push(65)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region outer(8192):
        mods: mutable darray[Mod] = []
        region inner(4096):
            m: mutable Mod = Mod(bits: [])
            in inner:
                Grow((&m).cast[mutable Mod&])
            mods.push(m) can Memory.Allocate, Abort.Panic
        print(mods[0].bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("binding a region param to a short ambient region must be REJECTED (not a segfault), got %s", status)
	}
}

// SOUNDNESS: because the reborrow cast now preserves the region, an escape THROUGH the cast (returning
// a region-less view of the grown field) is still caught — previously the region-erasing cast could
// have HIDDEN it. The cast region-provenance is a soundness improvement, not just ergonomics.
func TestS4Stage3ReborrowCastEscapeStillCaught(t *testing.T) {
	status, _ := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def leak(self: mutable Mod&) -> view[u8] can[Memory.Allocate, Abort.Panic]:
    self.bits.push(65)
    return ((&self).cast[mutable Mod&]).bits[0:1]
`)
	if status != "REJECTED" {
		t.Fatalf("returning a region-less view through a reborrow cast must be REJECTED (cast preserves region), got %s", status)
	}
}

// Region-poly forwarding: a wrapper that merely FORWARDS a container ref param to a region-requiring
// callee (without growing it itself) is inferred region-poly so the caller's region threads through —
// it runs end-to-end, and the escape through the wrapper is still caught (see the escape test below).
func TestRegionPolyForwardingThreadsRegion(t *testing.T) {
	status, out := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def sink(dst: mutable darray[Mod]&, v: Mod) -> void can[Memory.Allocate, Abort.Panic]:
    dst.push(v) can Memory.Allocate, Abort.Panic
    return
def mid(dst: mutable darray[Mod]&, v: Mod) -> void can[Memory.Allocate, Abort.Panic]:
    sink(dst, v) can Memory.Allocate, Abort.Panic
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(8192):
        mods: mutable darray[Mod] = []
        m: mutable Mod = Mod(bits: [])
        m.bits.push(65) can Memory.Allocate, Abort.Panic
        mid(mods, m) can Memory.Allocate, Abort.Panic
        print(mods[0].bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "65" {
		t.Fatalf("a forwarding wrapper must thread the caller's region and run (65), got %s %q", status, out)
	}
}

// S4 W5: an inner-region-grown by-value struct passed to a callee that STORES it into an outer
// container dangles when the inner region frees — a COMPILE error via the interprocedural
// store-target summary, not a segfault. The callee body is locally correct; the escape is a
// call-site property.
func TestS4W5InterprocStoreEscapeRejected(t *testing.T) {
	status, _ := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def stash(dst: mutable darray[Mod]&, v: Mod) -> void can[Memory.Allocate, Abort.Panic]:
    dst.push(v) can Memory.Allocate, Abort.Panic
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region outer(8192):
        mods: mutable darray[Mod] = []
        region inner(4096):
            m: mutable Mod = Mod(bits: [])
            m.bits.push(65) can Memory.Allocate, Abort.Panic
            stash(mods, m) can Memory.Allocate, Abort.Panic
        print(mods[0].bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("interprocedural store of an inner-grown by-value struct into an outer container must be REJECTED (was a segfault), got %s", status)
	}
}

// W5 must NOT over-reject: a SAME-region cohort (struct and target container in one region, dying
// together) passed to a storing callee runs correctly.
func TestS4W5InterprocSameRegionCohortRuns(t *testing.T) {
	status, out := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def stash(dst: mutable darray[Mod]&, v: Mod) -> void can[Memory.Allocate, Abort.Panic]:
    dst.push(v) can Memory.Allocate, Abort.Panic
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region a(8192):
        mods: mutable darray[Mod] = []
        m: mutable Mod = Mod(bits: [])
        m.bits.push(65) can Memory.Allocate, Abort.Panic
        stash(mods, m) can Memory.Allocate, Abort.Panic
        print(mods[0].bits[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "65" {
		t.Fatalf("same-region cohort passed to a storing callee must run (65), got %s %q", status, out)
	}
}

// W5 must NOT over-reject: passing a tainted by-value struct to a READ-ONLY callee (no store) runs.
func TestS4W5InterprocReadOnlyPassRuns(t *testing.T) {
	status, out := s4CompileRun(t, "struct Mod:\n    bits: mutable darray[u8]\n"+`def peek(v: Mod) -> i64 can[Abort.Panic]:
    return v.bits[0].i64()
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    region outer(8192):
        region inner(4096):
            m: mutable Mod = Mod(bits: [])
            m.bits.push(65) can Memory.Allocate, Abort.Panic
            print(peek(m).i64()) can Console.Write, Console.Format, Abort.Panic
    return 0`)
	if status != "RAN" || out != "65" {
		t.Fatalf("read-only pass of a tainted struct must run (65), got %s %q", status, out)
	}
}

func TestS4ReturnViewUAFRejectedNotSegfault(t *testing.T) {
	status, _ := s4CompileRun(t, s4StructHdr+`def leak[@r](m: mutable Mod& @r) -> view[u8] can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    return m.bits[0:1]
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    escaped: mutable view[u8] = zeroed
    region inner(64):
        m: mutable Mod& @inner = new[inner] Mod(bits: [])
        escaped <- leak(m) can Memory.Allocate, Abort.Panic
    print(escaped[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("returning a region-less view into a grown @r field and storing it past the region must be REJECTED at compile time (was a segfault), got %s", status)
	}
}

func TestS4ReturnRefUAFRejectedNotSegfault(t *testing.T) {
	status, _ := s4CompileRun(t, s4StructHdr+`def leak[@r](m: mutable Mod& @r) -> u8& can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    return &m.bits[0]
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    escaped: mutable u8& = zeroed
    region inner(64):
        m: mutable Mod& @inner = new[inner] Mod(bits: [])
        escaped <- leak(m) can Memory.Allocate, Abort.Panic
    print(escaped.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("returning a region-less ref into a grown @r field must be REJECTED at compile time (was a segfault), got %s", status)
	}
}
