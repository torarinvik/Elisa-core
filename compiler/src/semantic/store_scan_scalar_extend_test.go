package semantic

import "testing"

// A builtin darray `extend(xs)` copies xs's ELEMENTS. When they hold no region storage
// (`darray[u32]`) nothing of xs -- or of the struct it was read from -- reaches the receiver, so
// passing a local struct whose scalar rows are merged into a caller-owned one stores nothing
// that dangles. View elements still carry their bytes' region and stay rejected. Likewise a
// struct whose every view is program-lived (`darray[sview @source]`) yields only views that
// outlive any local holding it.
func TestStoreScanScalarExtendCarriesNothing(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"scalar_extend_through_struct_ref", `struct S:
    kinds: mutable darray[u32] = []

def merge(t: mutable S&, alt: S&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.kinds.extend(alt.kinds)

def caller(t: mutable S&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable S = S{}
        local.kinds.push(1)
        merge(t, local)
`, nil},
		{"scalar_extend_into_pushed_local", `struct P:
    a: mutable darray[u32] = []

struct S:
    paths: mutable darray[P] = []
    kinds: mutable darray[u32] = []
    seen: mutable bool = false

def cap(paths: mutable darray[P]&, xs: darray[u32]&) -> void:
    can Memory.Allocate, Abort.Panic:
        p: mutable P = P{}
        p.a.extend(xs)
        paths.push(p)

def merge(t: mutable S&, alt: S&) -> void:
    can Memory.Allocate, Abort.Panic:
        cap(t.paths, alt.kinds) if alt.seen

def caller(t: mutable S&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable S = S{}
        local.kinds.push(1)
        merge(t, local)
`, nil},
		{"view_extend_through_struct_ref", `struct S:
    names: mutable darray[sview] = []

def merge(t: mutable S&, alt: S&) -> void:
    can Memory.Allocate, Abort.Panic:
        t.names.extend(alt.names)

def caller(t: mutable S&) -> void:
    can Memory.Allocate, Abort.Panic:
        d: mutable dstr = []
        d.push(65)
        local: mutable S = S{}
        local.names.push(d.as_sview())
        merge(t, local)
`, []string{":14:18"}},
		{"view_extend_into_pushed_local", `struct P:
    a: mutable darray[sview] = []

struct S:
    paths: mutable darray[P] = []
    kinds: mutable darray[sview] = []
    seen: mutable bool = false

def cap(paths: mutable darray[P]&, xs: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        p: mutable P = P{}
        p.a.extend(xs)
        paths.push(p)

def merge(t: mutable S&, alt: S&) -> void:
    can Memory.Allocate, Abort.Panic:
        cap(t.paths, alt.kinds) if alt.seen

def caller(t: mutable S&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable S = S{}
        d: mutable dstr = []
        d.push(65)
        local.kinds.push(d.as_sview())
        merge(t, local)
`, []string{":25:18"}},
		{"program_lived_views_merged_from_local", `global region source

struct Snap:
    names: mutable darray[sview @source] = []
    slots: mutable darray[usize] = []

struct Flow:
    return_seen: mutable bool = false
    returned: mutable Snap = Snap{}

def merge_snap(left: Snap, right: Snap) -> Snap:
    can Memory.Allocate, Abort.Panic:
        out: mutable Snap = Snap{}
        for row in 0..<right.names.count |row, right, out|:
            out.names.push(right.names[row])
        for row in 0..<left.names.count |row, left, out|:
            out.names.push(left.names[row])
        return out

def add_return(flow: mutable Flow&, state: Snap) -> void:
    can Memory.Allocate, Abort.Panic:
        if flow.return_seen:
            flow.returned <- merge_snap(flow.returned, state)

def merge(flow: mutable Flow&, alt: Flow&) -> void:
    can Memory.Allocate, Abort.Panic:
        if alt.return_seen:
            add_return(flow, alt.returned)

def fill[@r](f: mutable Flow&, n: sview @source, t: mutable dstr& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        tmp: mutable dstr = []
        tmp.push(66)
        t.push(65)
        f.return_seen <- tmp.count > 0
        f.returned.names.push(n)

def caller(flow: mutable Flow&, n: sview @source, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                local: mutable Flow = Flow{}
                d: mutable dstr = []
                fill(local, n, d)
                merge(flow, local)
            _:
                pass
`, nil},
		{"region_less_views_merged_from_local", `global region source

struct Snap:
    names: mutable darray[sview] = []
    slots: mutable darray[usize] = []

struct Flow:
    return_seen: mutable bool = false
    returned: mutable Snap = Snap{}

def merge_snap(left: Snap, right: Snap) -> Snap:
    can Memory.Allocate, Abort.Panic:
        out: mutable Snap = Snap{}
        for row in 0..<right.names.count |row, right, out|:
            out.names.push(right.names[row])
        for row in 0..<left.names.count |row, left, out|:
            out.names.push(left.names[row])
        return out

def add_return(flow: mutable Flow&, state: Snap) -> void:
    can Memory.Allocate, Abort.Panic:
        if flow.return_seen:
            flow.returned <- merge_snap(flow.returned, state)

def merge(flow: mutable Flow&, alt: Flow&) -> void:
    can Memory.Allocate, Abort.Panic:
        if alt.return_seen:
            add_return(flow, alt.returned)

def fill[@r](f: mutable Flow&, n: sview @source, t: mutable dstr& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        tmp: mutable dstr = []
        tmp.push(66)
        t.push(65)
        f.return_seen <- tmp.count > 0
        f.returned.names.push(n)

def caller(flow: mutable Flow&, n: sview @source, k: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        match k:
            0:
                local: mutable Flow = Flow{}
                d: mutable dstr = []
                fill(local, n, d)
                merge(flow, local)
            _:
                pass
`, []string{":45:29"}},
		{"local_views_merged_from_local", `global region source

struct Snap:
    names: mutable darray[sview] = []
    slots: mutable darray[usize] = []

struct Flow:
    seen: mutable bool = false
    ret: mutable Snap = Snap{}

def copy(s: Snap) -> Snap:
    can Memory.Allocate, Abort.Panic:
        out: mutable Snap = Snap{}
        out.names.extend(s.names)
        out.slots.extend(s.slots)
        return out

def add(flow: mutable Flow&, s: Snap) -> void:
    can Memory.Allocate, Abort.Panic:
        flow.ret <- copy(s)

def merge(flow: mutable Flow&, alt: Flow&) -> void:
    can Memory.Allocate, Abort.Panic:
        add(flow, alt.ret) if alt.seen

def caller(flow: mutable Flow&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable Flow = Flow{}
        local.seen <- true
        d: mutable dstr = []
        d.push(65)
        local.ret.names.push(d.as_sview())
        merge(flow, local)
`, []string{":33:21"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
