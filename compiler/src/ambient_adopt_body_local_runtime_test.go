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

def fill_alt(alt: mutable Snap&, k: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<10 |alt|:
            alt.names.push(k * 100 + i.i64())

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        flow: mutable Flow = Flow{}
        alt1: mutable Snap = Snap{}
        fill_alt(&alt1, 1)
        merge_normal(&flow, &alt1)
        alt2: mutable Snap = Snap{}
        fill_alt(&alt2, 2)
        merge_normal(&flow, &alt2)
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

def fill_alt(alt: mutable Snap&, k: i64) -> void:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<10 |alt|:
            alt.names.push(k * 100 + i.i64())

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        flow: mutable Flow = Flow{}
        other: mutable Flow = Flow{}
        alt1: mutable Snap = Snap{}
        fill_alt(&alt1, 1)
        merge_normal(&flow, &other, &alt1)
        alt2: mutable Snap = Snap{}
        fill_alt(&alt2, 2)
        merge_normal(&flow, &other, &alt2)
        c: i64 = churn()
        if flow.normal.names.count != 20 or flow.normal.names[0] != 100 or flow.normal.names[19] != 209 or other.normal.names.count != 2 or c != 27993:
            panic("destination-allocated result freed with the call's region")
`
	buildAndRunSoATest(t, "destination_region_merge", src, "bt")
}

// A view picked out of a local darray[sview] and stored by the callee is an element value: when the
// local's elements view caller-owned bytes, the stored views stay valid after the local's region
// is freed (accepted by the store scan's non-byte-darray view rule).
func TestStoredElementViewOfLocalDArrayOutlivesIt(t *testing.T) {
	src := `
struct Table:
    names: mutable darray[sview] = []

def pick(names: darray[sview]&) -> sview:
    can Abort.Panic:
        return names[1] if names.count > 1 else ""

def note(table: mutable Table&, names: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        n: sview = pick(names)
        table.names.push(n)

def run(table: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[sview] = []
        for i in 0..<50 |local|:
            local.push("pad")
        local[1] <- src
        note(table, &local)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        owner: mutable dstr = []
        for i in 0..<5 |owner|:
            owner.push(72 + i.u8())
        table: mutable Table = Table{}
        run(&table, owner.as_sview())
        c: i64 = churn()
        if table.names.count != 1 or table.names[0].len != 5 or table.names[0][0] != 72 or table.names[0][4] != 76 or c != 27993:
            panic("stored element view lost")
`
	buildAndRunSoATest(t, "stored_element_view", src, "bt")
}

// storeViews at the call site: views stored out of an immutable byte-free struct local (two
// writable targets, so nothing is adopted) point at the caller's bytes, which outlive the call.
func TestStoreViewsFromImmutableStructLocalRun(t *testing.T) {
	src := `
struct Wrap:
    names: darray[sview]
    n: i64

struct Table:
    names: mutable darray[sview] = []

def keep_first(table: mutable Table&, w: Wrap&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names.push(w.names[0])

def make_wrap(src: sview) -> Wrap:
    can Memory.Allocate:
        return Wrap{names: [src, "lit"], n: 2}

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        w: Wrap = Wrap{names: [src, "lit"], n: 2}
        keep_first(table, &w)
        w2: Wrap = make_wrap(src)
        keep_first(table, &w2)
        other.names.push("x")

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        owner: mutable dstr = []
        for i in 0..<5 |owner|:
            owner.push(72 + i.u8())
        table: mutable Table = Table{}
        other: mutable Table = Table{}
        run(&table, &other, owner.as_sview())
        c: i64 = churn()
        if table.names.count != 2 or table.names[0].len != 5 or table.names[1][4] != 76 or c != 27993:
            panic("stored views lost")
`
	buildAndRunSoATest(t, "store_views_immutable_struct", src, "bt")
}

// Store-scan scalar locals and range binders: views of the caller's bytes stored through an
// index computed from the source, and through a counted loop, outlive the call intact.
func TestStoreScanScalarIndexStoresRun(t *testing.T) {
	src := `
struct Table:
    names: mutable darray[sview] = []

def run(table: mutable Table&, bytes: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        bound: mutable darray[sview] = [bytes, "x"]
        keep_one(bound, table)
        keep_looped(bound, table)

def last_index(bound: darray[sview]&, name: sview) -> int:
    can Abort.Panic:
        for i in 0..<bound.count |bound|:
            return i.int() if bound[i] == name
        return -1

def keep_one(bound: darray[sview]&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        index: int = last_index(bound, "x")
        if index >= 0:
            table.names <- table.names.push(bound[index.usize() - 1])

def keep_looped(src: darray[sview]&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<src.count |table, src|:
            table.names <- table.names.push(src[i])

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        owner: mutable dstr = []
        for i in 0..<5 |owner|:
            owner.push(72 + i.u8())
        table: mutable Table = Table{}
        run(&table, owner.as_sview())
        c: i64 = churn()
        if table.names.count != 3 or table.names[0].len != 5 or table.names[0][4] != 76 or table.names[1][0] != 72 or table.names[2][0] != 120 or c != 27993:
            panic("stored views lost")
`
	buildAndRunSoATest(t, "store_scan_scalar_index", src, "bt")
}

// Rule-225 bundles judged by the callee's return summary: views stored through a call result
// that carries no writable target part outlive the call intact.
func TestStoreScanCallBundleStoresRun(t *testing.T) {
	src := `
struct Wrap:
    names: darray[sview]
    n: i64

struct Pair:
    a: sview
    b: darray[sview]

struct Table:
    names: mutable darray[sview] = []

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        w: Wrap = Wrap{names: [src, "lit"], n: 2}
        via_pick(&w, table)
        via_relabel(&w, table)
        other.names.push("x")

def pick(w: Wrap&, table: Table&) -> sview:
    can Abort.Panic:
        return "" if table.names.count == 0
        return w.names[0]

def short_name(table: Table&) -> sview:
    can Abort.Panic:
        return table.names[0]

def relabel(w: Wrap&, table: Table&) -> Pair:
    can Memory.Allocate, Abort.Panic:
        name: sview = short_name(table)
        return Pair{a: name, b: w.names}

def keep(v: sview, table: mutable Table&) -> void:
    can Memory.Allocate:
        table.names.push(v)

def keep_pair(p: Pair, table: mutable Table&) -> void:
    can Memory.Allocate:
        table.names.push(p.a)

def via_pick(w: Wrap&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        keep(pick(w, table), table)

def via_relabel(w: Wrap&, table: mutable Table&) -> void:
    can Memory.Allocate, Abort.Panic:
        keep_pair(relabel(w, table), table)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        owner: mutable dstr = []
        for i in 0..<5 |owner|:
            owner.push(72 + i.u8())
        table: mutable Table = Table{}
        table.names.push("seed")
        other: mutable Table = Table{}
        run(&table, &other, owner.as_sview())
        c: i64 = churn()
        if table.names.count != 3 or table.names[1].len != 5 or table.names[1][4] != 76 or table.names[2][0] != 115 or c != 27993:
            panic("stored views lost")
`
	buildAndRunSoATest(t, "store_scan_call_bundle", src, "bt")
}
