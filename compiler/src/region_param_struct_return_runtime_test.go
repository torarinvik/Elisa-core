package main

import (
	"testing"
)

func TestS4ReturnViewUAFRejectedNotSegfault(t *testing.T) {
	t.Parallel()
	status, _ := s4CompileRun(t, s4StructHdr+`def leak[@r](m: mutable Mod& @r) -> view[u8] can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    return m.bits[0:1]
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    escaped: mutable view[u8] = zeroed
    region inner(64):
        m: mutable Mod& @inner = new[inner] Mod{bits: []}
        escaped <- leak(m) can Memory.Allocate, Abort.Panic
    print(escaped[0].i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("returning a region-less view into a grown @r field and storing it past the region must be REJECTED at compile time (was a segfault), got %s", status)
	}
}

func TestS4ReturnRefUAFRejectedNotSegfault(t *testing.T) {
	t.Parallel()
	status, _ := s4CompileRun(t, s4StructHdr+`def leak[@r](m: mutable Mod& @r) -> u8& can[Memory.Allocate, Abort.Panic]:
    m.bits.push(65)
    return &m.bits[0]
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic]:
    escaped: mutable u8& = zeroed
    region inner(64):
        m: mutable Mod& @inner = new[inner] Mod{bits: []}
        escaped <- leak(m) can Memory.Allocate, Abort.Panic
    print(escaped.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic
    return 0`)
	if status != "REJECTED" {
		t.Fatalf("returning a region-less ref into a grown @r field must be REJECTED at compile time (was a segfault), got %s", status)
	}
}

// Regression: a by-ref struct param's field grown via a helper whose body CONSTRUCTS a
// struct-literal element and forwards it to push (`e = E{...}; p.errs.push(e)`) — the shape
// the parser speculatively wraps in a synthesized `__auto_*` region (it can't tell the
// by-value element from an auto-ref'd `mutable&` arg without types). That wrap used to hide the
// field growth from paramFieldContainerIsGrown, so NO region param was inferred and the push
// allocated into the per-call auto arena, freed on return -> use-after-free (SIGSEGV on the 2nd
// push). The fix makes the predicate see through the SYNTHESIZED wrap so the caller's region is
// threaded. Loop + read-back so a dangling backing corrupts the result instead of passing by luck.
func TestS4FieldGrowthThroughSyntheticAutoRegionWrapNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, "struct E:\n    v: u32\nstruct Bag:\n    errs: mutable darray[E]\n"+`def Bag() -> Bag:
    return Bag{errs: []}
def add(b: mutable Bag&, n: u32) -> void can[Memory.Allocate, Abort.Panic]:
    e: E = E{v: n}
    b.errs.push(e)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    total: mutable u64 = 0
    i: mutable i64 = 0
    while i < 2000:
        b: mutable Bag = Bag()
        add(b, 65) can Memory.Allocate, Abort.Panic
        add(b, 66) can Memory.Allocate, Abort.Panic
        total <- total + b.errs[0].v.u64() + b.errs[1].v.u64()
        i <- i + 1
    print(total.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "262000" {
		t.Fatalf("field growth through a synthesized auto-region wrap must thread the caller region (no UAF); expected RAN 262000, got %s %q", status, out)
	}
}

// A region-poly builder constructs a struct via a constructor (whose name collides with the struct),
// grows a container field through a forwarded ref, and RETURNS that field inside a DIFFERENT struct;
// the caller reads it back. Before the fix, the builder was not classified region-polymorphic (the
// constructor call `Bag()` resolved to the shadowing struct, and the returned `Out{items: b.errs}`
// field access was not recognized as region-carrying), so the builder's auto-region was freed on
// return -> the returned aggregate's container dangled (headers copy out fine, element reads UAF).
// This is the exact shape of the stage1 parser's `parser = Parser(...); return Ast::File{errors:
// parser.errors, ...}`. Loop + read-back so a dangling backing corrupts the sum instead of passing
// by luck.
func TestRegionPolyBuilderReturnsFieldInOtherStructNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, "struct E:\n    v: u32\nstruct Bag:\n    errs: mutable darray[E]\nstruct Out:\n    items: darray[E]\n"+`def Bag() -> Bag:
    return Bag{errs: []}
def add(b: mutable Bag&, n: u32) -> void can[Memory.Allocate, Abort.Panic]:
    e: E = E{v: n}
    b.errs.push(e)
    return
def build() -> Out can[Memory.Allocate, Abort.Panic]:
    b: mutable Bag = Bag()
    add(b, 65) can Memory.Allocate, Abort.Panic
    add(b, 66) can Memory.Allocate, Abort.Panic
    return Out{items: b.errs}
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    total: mutable u64 = 0
    i: mutable i64 = 0
    while i < 2000:
        o: Out = build() can Memory.Allocate, Abort.Panic
        total <- total + o.items[0].v.u64() + o.items[1].v.u64()
        i <- i + 1
    print(total.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "262000" {
		t.Fatalf("a region-poly builder returning a grown field inside another struct must thread+adopt the caller region (no UAF); expected RAN 262000, got %s %q", status, out)
	}
}

// Void-grower region adoption (docs/75 cross-fn container growth): a `void` helper that grows a
// caller-owned `darray[Node]&` by INSERTING a region-poly node-with-payload (`out.push(Block(...))`)
// must allocate the inserted node — and its side-table darray payload — into the caller's container
// region, not a per-call arena freed on return. Before AmbientGrownContainerRegion this segfaulted on
// traversal (the payload darray backing dangled: `body.count` read, `for x in body` faulted). Builds
// cross-fn through the void grower, then folds the tree: 99 + 10 + 20 = 129.
func TestVoidGrowerPayloadDarrayAdoptedNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `enum Node: pass
enum Stmt is Node:
    Leaf(value: i64)
    Block(body: darray[Node], tag: i64)
def make_leaf(v: i64) -> Node can[Memory.Allocate, Abort.Panic]:
    return new Stmt.Leaf(value: v)
def grow(out: mutable darray[Node]&) -> void can[Memory.Allocate, Abort.Panic]:
    kids: mutable darray[Node] = []
    kids.push(make_leaf(10) can Memory.Allocate, Abort.Panic)
    kids.push(make_leaf(20) can Memory.Allocate, Abort.Panic)
    out.push(new Stmt.Block(body: kids, tag: 99))
    return
def sumtree(n: Node) -> i64 can[Memory.Allocate, Abort.Panic]:
    match n:
        Stmt.Leaf(value: v):
            return v
        Stmt.Block(body: b, tag: t):
            total: mutable i64 = t
            for k in b:
                total <- total + sumtree(k)
            return total
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write]:
    nodes: mutable darray[Node] = []
    grow(nodes) can Memory.Allocate, Abort.Panic
    acc: mutable i64 = 0
    for n in nodes:
        acc <- acc + sumtree(n) can Memory.Allocate, Abort.Panic
    print(acc.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "129" {
		t.Fatalf("void grower inserting a region-poly node-with-payload must adopt its payload into the caller's container region; expected RAN 129, got %s %q", status, out)
	}
}

// Void-grower SAFETY GATE: the ambient-region binding only engages for a function with a SINGLE
// regionless container parameter — so the inserted value's only destination is that one caller-owned
// container, sound by construction (no second different-lifetime container to escape into). This
// exercises the sound path end-to-end: a one-arg grower builds a node-with-payload that is folded
// after return (1 + 1 leaves + tag 0 = 2). The orthogonal handle-level cross-region escape check for
// MULTI-container growers (not ambient-bound by this fix) is tracked separately
// ([[void-grower-region-adoption]]).
func TestVoidGrowerSingleContainerAdoptedNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `enum Node: pass
enum Stmt is Node:
    Leaf(value: i64)
    Block(body: darray[Node], tag: i64)
def make_block(tag: i64) -> Node can[Memory.Allocate, Abort.Panic]:
    kids: mutable darray[Node] = []
    kids.push(new Stmt.Leaf(value: 1))
    kids.push(new Stmt.Leaf(value: 1))
    return new Stmt.Block(body: kids, tag: tag)
def grow(out: mutable darray[Node]&) -> void can[Memory.Allocate, Abort.Panic]:
    out.push(make_block(0) can Memory.Allocate, Abort.Panic)
    return
def sumtree(n: Node) -> i64 can[Memory.Allocate, Abort.Panic]:
    match n:
        Stmt.Leaf(value: v):
            return v
        Stmt.Block(body: b, tag: t):
            total: mutable i64 = t
            for k in b:
                total <- total + sumtree(k)
            return total
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write]:
    nodes: mutable darray[Node] = []
    grow(nodes) can Memory.Allocate, Abort.Panic
    print(sumtree(nodes[0]).i64() can Memory.Allocate, Abort.Panic) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "2" {
		t.Fatalf("single-container void grower must adopt the payload soundly; expected RAN 2, got %s %q", status, out)
	}
}

// Auto-region inference into an iterator-loop body (`for x in coll:`): a container literal built
// inside such a loop and forwarded to a by-ref grower must get the function-body auto region with NO
// explicit `region NAME:` — functionBodyNeedsAutoRegion was missing the IterForStmt case, so the
// container got NO region (loop tightening also skips it because the forward makes it non-iteration-
// local). Mirrors the stage1 resolver's per-function scratch pattern. Folds (1+99)+(2+99)=201.
func TestIterForLoopLocalContainerForwardedNoExplicitRegion(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def grow(out: mutable darray[i64]&) -> void can[Memory.Allocate, Abort.Panic]:
    out.push(99)
    return
def process(xs: darray[i64]) -> i64 can[Memory.Allocate, Abort.Panic]:
    total: mutable i64 = 0
    for v in xs:
        bound: mutable darray[i64] = []
        bound.push(v)
        grow(bound)
        total <- total + bound[0] + bound[1]
    return total
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    xs: mutable darray[i64] = []
    xs.push(1) can Memory.Allocate, Abort.Panic
    xs.push(2) can Memory.Allocate, Abort.Panic
    print(process(xs).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "201" {
		t.Fatalf("loop-local container forwarded to a grower must infer its region with no explicit region; expected RAN 201, got %s %q", status, out)
	}
}

// A region-polymorphic container-growing function defined inside a MODULE must be
// dispatched as a direct call so its hidden region-param args thread correctly.
// directCallTarget looked up the callee by its BARE name in GlobalScope, missing the
// module-scoped symbol (`M.grow`) — so the call fell through to the function-value
// (closure) path, whose lowered type omits the region param, yielding an LLVM
// "Incorrect number of arguments passed to called function" build failure. The fix
// resolves the canonical (namespaced) name first, exactly as resolveCallTarget does.
// Exercises both an unqualified same-module call (`grow`) and a qualified one
// (`M::build`). Folds 1+2=3.
func TestModuleScopedRegionPolyGrowerCall(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `module M:
    def grow(out: mutable darray[i64]&, v: i64) -> void can[Memory.Allocate, Abort.Panic]:
        out.push(v)
        return
    def build() -> i64 can[Memory.Allocate, Abort.Panic]:
        xs: mutable darray[i64] = []
        grow(xs, 1)
        grow(xs, 2)
        return xs[0] + xs[1]
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    print(M::build().i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "3" {
		t.Fatalf("module-scoped region-poly grower call must dispatch directly and thread region args; expected RAN 3, got %s %q", status, out)
	}
}

// TestRegionLocalStructFieldReturnedNoUAF pins a BARE `return b.items` -- a local struct's grown
// container field, returned directly.
//
// This SILENTLY MISCOMPILED: it compiled clean and segfaulted, because the function was not
// classified region-polymorphic, so `b.items`' backing lived in its auto region and was freed on
// return while the header copied out. Both neighbouring shapes already worked -- `return b` (the
// whole struct) and `return xs` (a local darray) -- which is what made the gap easy to miss; the
// classifier handled a container field only as a STRUCT-LITERAL field (`return Out{items: b.errs}`),
// not as a bare field return.
//
// The loop and read-back are load-bearing: a freed backing usually still reads plausibly once, so a
// single read can pass by luck. Summing 2000 iterations turns a dangling backing into a wrong total
// or a crash rather than a green test.
func TestRegionLocalStructFieldReturnedNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, "struct Bag:\n    items: mutable darray[u32]\n"+`def build(n: u32) -> darray[u32] can[Memory.Allocate, Abort.Panic]:
    b: mutable Bag = Bag{items: []}
    b.items.push(n)
    b.items.push(n + 1)
    return b.items
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    total: mutable u64 = 0
    i: mutable i64 = 0
    while i < 2000:
        xs: darray[u32] = build(65) can Memory.Allocate, Abort.Panic
        total <- total + xs[0].u64() + xs[1].u64()
        i <- i + 1
    print(total.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "262000" {
		t.Fatalf("a bare `return localStruct.containerField` must thread the caller region (no UAF); expected RAN 262000, got %s %q", status, out)
	}
}

// A region-polymorphic function may return one local container while using another local
// container as scratch storage. The returned container must stay on the caller-owned stack, but
// scratch must use a private stack: otherwise a reserve_commit caller arena sees a non-tail growth
// after the scratch allocation and aborts with "not the tail allocation".
func TestRegionPolyReturnedContainerHasPrivateScratchStack(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def build() -> darray[u8] can[Memory.Allocate, Abort.Panic]:
    out: mutable darray[u8] = []
    scratch: mutable darray[u8] = []
    scratch.resize(8192)
    i: mutable usize = 0
    while i < 300:
        out.push(i.u8())
        i <- i + 1
    return out
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    xs: darray[u8] = build() can Memory.Allocate, Abort.Panic
    print(xs.count.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "300" {
		t.Fatalf("returned region-poly container must not share its caller arena with scratch growth; expected RAN 300, got %s %q", status, out)
	}
}

// A returned darray may contain structs whose own darray fields were built in a
// nested temporary scope. The outer header is not enough: every copied nested
// backing must follow the returned aggregate into the caller-owned region.
func TestRegionPolyReturnedAggregateDeepStorageNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `struct Arm:
    body: darray[i64]
    tag: i64
def build() -> darray[Arm] can[Memory.Allocate, Abort.Panic]:
    arms: mutable darray[Arm] = []
    body: mutable darray[i64] = []
    body.push(41)
    arms.push(Arm{body: body, tag: 1})
    return arms
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    arms: darray[Arm] = build() can Memory.Allocate, Abort.Panic
    print((arms[0].body[0] + arms[0].tag).i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "42" {
		t.Fatalf("returned aggregate must retain nested darray storage; expected RAN 42, got %s %q", status, out)
	}
}

// An inferred region parameter can be introduced by a void grower rather than by a
// RegionPolymorphic return signature.  Its synthesized auto region must still adopt the
// caller arena, and a region-polymorphic local initializer inside that function must use the
// private scratch arena.  Before the backend bound only explicit __region_auto parameters, the
// call to build() below allocated after the caller-owned report buffer; report's later growth
// then aborted with "reserve_commit/fixed array cannot grow in place: not the tail allocation".
func TestInferredGrowerUsesPrivateScratchForRegionPolyLocal(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `def make_bytes() -> darray[u8] can[Memory.Allocate, Abort.Panic]:
    values: mutable darray[u8] = []
    i: mutable usize = 0
    while i < 256:
        values.push(i.u8())
        i <- i + 1
    return values
def consume(report: mutable darray[u8]&) -> void can[Memory.Allocate, Abort.Panic]:
    captured: darray[u8] = make_bytes() can Memory.Allocate, Abort.Panic
    for value in captured:
        report.push(value)
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write]:
    report: mutable darray[u8] = [0]
    consume(report) can Memory.Allocate, Abort.Panic
    print(report.count.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "257" {
		t.Fatalf("an inferred grower must isolate a non-escaping region-poly local; expected RAN 257, got %s %q", status, out)
	}
}

// Void grower called MANY times: the caller's container outgrows its initial capacity, so its
// arena must tolerate the callee's adopted payload allocations interleaved between growth
// steps. Before the ambient-forwarded chained-backing demotion (assignRegionStacks), the
// container sat on a tail-only reserve_commit stack and the first non-tail realloc panicked
// ("reserve_commit/fixed array cannot grow in place") — the shipped single-call test never grew
// past the initial capacity, so this stayed latent.
func TestVoidGrowerRepeatedGrowthChainedBackingNoPanic(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `enum Node: pass
enum Stmt is Node:
    Leaf(value: i64)
    Block(body: darray[Node], tag: i64)
def make_leaf(v: i64) -> Node can[Memory.Allocate, Abort.Panic]:
    return new Stmt.Leaf(value: v)
def grow(out: mutable darray[Node]&) -> void can[Memory.Allocate, Abort.Panic]:
    kids: mutable darray[Node] = []
    kids.push(make_leaf(10) can Memory.Allocate, Abort.Panic)
    kids.push(make_leaf(20) can Memory.Allocate, Abort.Panic)
    out.push(new Stmt.Block(body: kids, tag: 99))
    return
def sumtree(n: Node) -> i64 can[Memory.Allocate, Abort.Panic]:
    match n:
        Stmt.Leaf(value: v):
            return v
        Stmt.Block(body: b, tag: t):
            total: mutable i64 = t
            for k in b:
                total <- total + sumtree(k) can Memory.Allocate, Abort.Panic
            return total
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write]:
    nodes: mutable darray[Node] = []
    i: mutable i64 = 0
    while i < 2000:
        grow(nodes) can Memory.Allocate, Abort.Panic
        i <- i + 1
    acc: mutable i64 = 0
    for n in nodes:
        acc <- acc + sumtree(n) can Memory.Allocate, Abort.Panic
    print(acc.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "258000" {
		t.Fatalf("repeated void-grower growth must relocate (chained backing), not panic; expected RAN 258000, got %s %q", status, out)
	}
}

// An INLINE value enum (no packed store) whose variant payload wraps a region-poly call result,
// pushed by a single-container void grower: previously the wrapper hid the payload from
// containerParamGrownWithRegionValue, so ambient adoption never engaged and the payload backing
// freed on the grower's return — a SILENT use-after-free (segfault at 2000 iterations). Now
// typeCarriesRegionStorage sees the enum payload and the ctor args are traced, so the payload
// adopts into the caller's container region.
func TestVoidGrowerInlineEnumPayloadAdoptedNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `enum Item:
    Row(vals: darray[i64])
    Nothing()
def make_vals(n: i64) -> darray[i64] can[Memory.Allocate, Abort.Panic]:
    xs: mutable darray[i64] = []
    xs.push(n)
    xs.push(n + 1)
    return xs
def grow(out: mutable darray[Item]&) -> void can[Memory.Allocate, Abort.Panic]:
    out.push(Item.Row(make_vals(10) can Memory.Allocate, Abort.Panic))
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write]:
    nested: mutable darray[Item] = []
    i: mutable i64 = 0
    while i < 2000:
        grow(nested) can Memory.Allocate, Abort.Panic
        i <- i + 1
    total: mutable i64 = 0
    for it in nested:
        match it:
            Item.Row(vals: vs):
                for x in vs:
                    total <- total + x
            Item.Nothing():
                total <- total + 0
    print(total.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "42000" {
		t.Fatalf("inline-enum payload void grower must adopt the payload into the caller's region; expected RAN 42000, got %s %q", status, out)
	}
}

// A STRUCT-literal wrapper around a region-poly call result, pushed by a single-container void
// grower: `out.push(Holder{items: make_vals(10), tag: 5})`. The enum-ctor equivalent already
// adopted; the struct form was REJECTED ("via a fresh container") because
// checkInteriorRegionAgainstTarget lacked the sanctioned-adoption exemption. With it, the
// payload adopts into the caller's container region and runs.
func TestVoidGrowerStructLiteralPayloadAdoptedNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `struct Holder:
    items: darray[i64]
    tag: i64
def make_vals(n: i64) -> darray[i64] can[Memory.Allocate, Abort.Panic]:
    xs: mutable darray[i64] = []
    xs.push(n)
    xs.push(n + 1)
    return xs
def grow(out: mutable darray[Holder]&) -> void can[Memory.Allocate, Abort.Panic]:
    items: darray[i64] = make_vals(10) can Memory.Allocate, Abort.Panic
    out.push(Holder{items: items, tag: 5})
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write]:
    nested: mutable darray[Holder] = []
    i: mutable i64 = 0
    while i < 2000:
        grow(nested) can Memory.Allocate, Abort.Panic
        i <- i + 1
    total: mutable i64 = 0
    for h in nested:
        for x in h.items:
            total <- total + x
    print(total.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "42000" {
		t.Fatalf("struct-literal payload single-container grower must adopt; expected RAN 42000, got %s %q", status, out)
	}
}

// The SINGLE-container extend form adopts (ambient exemption in checkInteriorRegionAgainstTarget)
// and must keep running — the rejection above is gated to multi-container growers only.
func TestVoidGrowerExtendEnumCtorElementAdoptedNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `enum Item:
    Row(vals: darray[i64])
    Nothing()
def make_vals(n: i64) -> darray[i64] can[Memory.Allocate, Abort.Panic]:
    xs: mutable darray[i64] = []
    xs.push(n)
    xs.push(n + 1)
    return xs
def grow(out: mutable darray[Item]&) -> void can[Memory.Allocate, Abort.Panic]:
    out.extend([Item.Row(make_vals(10) can Memory.Allocate, Abort.Panic)])
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write]:
    nested: mutable darray[Item] = []
    i: mutable i64 = 0
    while i < 2000:
        grow(nested) can Memory.Allocate, Abort.Panic
        i <- i + 1
    total: mutable i64 = 0
    for it in nested:
        match it:
            Item.Row(vals: vs):
                for x in vs:
                    total <- total + x
            Item.Nothing():
                total <- total + 0
    print(total.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "42000" {
		t.Fatalf("single-container extend of an enum-ctor element must adopt; expected RAN 42000, got %s %q", status, out)
	}
}

// The single-container ident-bound form must still ADOPT and run — the taint the recorder now
// records is exempted by the ambient-adoption carve-out, exactly like the inline-ctor form.
func TestVoidGrowerIdentBoundEnumCtorAdoptedNoUAF(t *testing.T) {
	t.Parallel()
	status, out := s4CompileRun(t, `enum Item:
    Row(vals: darray[i64])
    Nothing()
def make_vals(n: i64) -> darray[i64] can[Memory.Allocate, Abort.Panic]:
    xs: mutable darray[i64] = []
    xs.push(n)
    xs.push(n + 1)
    return xs
def grow(out: mutable darray[Item]&) -> void can[Memory.Allocate, Abort.Panic]:
    node: Item = Item.Row(make_vals(10) can Memory.Allocate, Abort.Panic)
    out.push(node)
    return
def main() -> int can[Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write]:
    nested: mutable darray[Item] = []
    i: mutable i64 = 0
    while i < 2000:
        grow(nested) can Memory.Allocate, Abort.Panic
        i <- i + 1
    total: mutable i64 = 0
    for it in nested:
        match it:
            Item.Row(vals: vs):
                for x in vs:
                    total <- total + x
            Item.Nothing():
                total <- total + 0
    print(total.i64()) can Console.Write, Memory.Allocate, Console.Format, Abort.Panic, Atomics.Exchange, Atomics.Store, Global.Read, Global.Write
    return 0`)
	if status != "RAN" || out != "42000" {
		t.Fatalf("single-container ident-bound ctor must adopt; expected RAN 42000, got %s %q", status, out)
	}
}
