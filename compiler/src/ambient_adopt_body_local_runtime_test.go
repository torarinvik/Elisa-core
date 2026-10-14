package main

import "testing"

// A function whose only writable target is a struct param (here `parser: lmut P`) and that
// stores a body-built value into it binds its ambient region to that param's region: `buf` and
// `captures` are allocated in the caller's region, so the views pushed into parser.params stay
// valid after the call (formerly rejected as a local-region escape).
func TestAmbientAdoptsBodyLocalStoredIntoSoleStructTarget(t *testing.T) {
	src := `
struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def fill[@r](parser: lmut P, items: mutable darray[Prm]& @r, bytes: mutable darray[u8]& @r) -> bool:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<64 |bytes|:
            bytes.push(65 + (i % 26).u8())
        items.push(Prm{name: bytes.as_sview(), has_default: false})
        parser.position <- parser.position + 1
        return true

def handler(parser: lmut P) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = []
        captures: mutable darray[Prm] = []
        rebind ignored: bool, parser = parser.fill(captures, buf)
        for capture in captures |parser|:
            parser.params <- parser.params.push(capture)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        p: mutable P = P{source: "", position: 0, params: []}
        rebind p = handler(p)
        c: i64 = churn()
        if p.params.count != 1 or p.params[0].name.len != 64 or c != 27993:
            panic("shape")
        if p.params[0].name[0] != 65 or p.params[0].name[63] != 65 + 11:
            panic("view bytes freed")
`
	buildAndRunSoATest(t, "ambient_adopt_lmut_views", src, "bt")
}

// The void-grower form with a struct target: a local struct built in the body and pushed into the
// sole writable target's container lives in the caller's region.
func TestAmbientAdoptsLocalStructPushedIntoStructTarget(t *testing.T) {
	src := `
struct Path:
    names: mutable darray[i64] = []

struct Log:
    paths: mutable darray[Path] = []

def record(log: mutable Log&, xs: darray[i64]&) -> void:
    can Memory.Allocate:
        p: mutable Path = Path{}
        p.names.extend(xs)
        log.paths.push(p)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        log: mutable Log = Log{}
        src: mutable darray[i64] = []
        for i in 0..<50 |src|:
            src.push(i.i64())
        record(&log, &src)
        record(&log, &src)
        c: i64 = churn()
        if log.paths.count != 2 or log.paths[1].names.count != 50 or log.paths[0].names[49] != 49 or log.paths[1].names[49] != 49 or c != 27993:
            panic("recorded path freed with record's region")
`
	buildAndRunSoATest(t, "ambient_adopt_struct_push", src, "bt")
}

// `flow.normal <- merge(flow.normal, alternate.normal)`: the call mentions the target, but its
// FRESH result is built in the ambient region, adopted into flow's (the sole writable target).
// A merge that may return an argument instead is judged at its callers by what it returns
// (TestMergeReturningArgumentFromLocalRejected); this one allocates fresh.
func TestAmbientAdoptsMergeResultMentioningTarget(t *testing.T) {
	src := `
struct Snap:
    names: mutable darray[i64] = []

struct Flow:
    normal: mutable Snap = Snap{}
    seen: mutable bool = false

def merge(a: Snap&, b: Snap&) -> Snap:
    can Memory.Allocate, Abort.Panic:
        out: mutable Snap = Snap{}
        for v in a.names |out|:
            out.names.push(v)
        for v in b.names |out|:
            out.names.push(v)
        return out

def merge_normal(flow: mutable Flow&, alternate: Snap&) -> void:
    can Memory.Allocate, Abort.Panic:
        flow.normal <- merge(flow.normal, alternate)
        flow.seen <- true

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

def add_round(flow: mutable Flow&, k: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        alt: mutable Snap = Snap{}
        for i in 0..<10 |alt|:
            alt.names.push(k * 100 + i.i64())
        merge_normal(flow, &alt)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        flow: mutable Flow = Flow{}
        add_round(&flow, 1)
        add_round(&flow, 2)
        c: i64 = churn()
        if flow.normal.names.count != 20 or flow.normal.names[0] != 100 or flow.normal.names[19] != 209 or c != 27993:
            panic("merged snapshot freed with merge_normal's region")
`
	buildAndRunSoATest(t, "ambient_adopt_merge", src, "bt")
}

// Destination-driven allocation: with TWO writable targets the ambient region cannot be adopted,
// but `flow.normal <- merge(..)` still allocates merge's fresh result directly in flow's own
// (caller-owned) region -- the hidden region argument names the store target's region -- so the
// result outlives the call while the other target is untouched.
func TestDestinationRegionAllocatesCallResultInTargetRegion(t *testing.T) {
	src := `
struct Snap:
    names: mutable darray[i64] = []

struct Flow:
    normal: mutable Snap = Snap{}

def merge(a: Snap&, b: Snap&) -> Snap:
    can Memory.Allocate, Abort.Panic:
        out: mutable Snap = Snap{}
        for v in a.names |out|:
            out.names.push(v)
        for v in b.names |out|:
            out.names.push(v)
        return out

def merge_normal(flow: mutable Flow&, other: mutable Flow&, alternate: Snap&) -> void:
    can Memory.Allocate, Abort.Panic:
        flow.normal <- merge(flow.normal, alternate)
        other.normal.names.push(1)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

def add_round(flow: mutable Flow&, other: mutable Flow&, k: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        alt: mutable Snap = Snap{}
        for i in 0..<10 |alt|:
            alt.names.push(k * 100 + i.i64())
        merge_normal(flow, other, &alt)

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        flow: mutable Flow = Flow{}
        other: mutable Flow = Flow{}
        add_round(&flow, &other, 1)
        add_round(&flow, &other, 2)
        c: i64 = churn()
        if flow.normal.names.count != 20 or flow.normal.names[0] != 100 or flow.normal.names[19] != 209 or other.normal.names.count != 2 or c != 27993:
            panic("destination-allocated result freed with the call's region")
`
	buildAndRunSoATest(t, "destination_region_merge", src, "bt")
}
