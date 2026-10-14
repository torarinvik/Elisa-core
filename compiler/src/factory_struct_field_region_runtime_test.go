package main

import "testing"

// A function that builds a struct local (`s: T = T{}`), grows its container FIELDS and returns it
// must hand back live backings. Before the fix the field growth either had no region at all
// ("darray push has no region to grow into") or, when some unrelated bare local opened the
// function's auto region, grew into that region and was freed at return: the header survived
// but the elements dangled (a silent use-after-free).
func TestFactoryStructFieldGrowthOutlivesCall(t *testing.T) {
	src := `
struct Snap:
    names: mutable darray[sview] = []
    nums: mutable darray[usize] = []

def build_plain(src: darray[sview]&) -> Snap:
    can Memory.Allocate, Abort.Panic:
        s: mutable Snap = Snap{}
        s.nums.reserve(2)
        for n in src |s|:
            s.names.push(n)
            s.nums.push(n.len)
        return s

def build_with_scratch(n: usize) -> Snap:
    can Memory.Allocate, Abort.Panic:
        s: mutable Snap = Snap{}
        tmp: mutable darray[usize] = []
        tmp.push(1)
        for i in 0..<n |s|:
            s.nums.push(i + 100)
        return s

def churn() -> usize:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[usize] = []
        for i in 0..<1000 |junk|:
            junk.push(i * 7)
        return junk[999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        b: mutable darray[sview] = ["alpha", "be", "gamma1", "d"]
        x: Snap = build_plain(b)
        y: Snap = build_with_scratch(4)
        c: usize = churn()
        total: mutable usize = 0
        for k in x.nums |total|:
            total <- total + k
        if total != 14 or x.names.count != 4 or c != 6993 or x.names[2] != "gamma1":
            panic("plain factory struct field backing corrupted")
        if y.nums.count != 4 or y.nums[0] != 100 or y.nums[3] != 103:
            panic("scratch factory struct field backing corrupted")
`
	buildAndRunSoATest(t, "factory_struct_field_region", src, "bt")
}

// `static if` branches are selected after parsing; a container built in one still needs the
// function-body auto region.
func TestStaticIfBranchContainerGetsAutoRegion(t *testing.T) {
	src := `
def fill(prefix: mutable darray[u8]&) -> bool:
    can Memory.Allocate, Abort.Panic:
        static if ELISA_TARGET_OS_WINDOWS:
            return false
        static else:
            buf: mutable darray[u8] = []
            buf.resize(16.usize())
            for i in 0..<buf.count |prefix, buf|:
                prefix.push(buf[i])
            return true

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        p: mutable darray[u8] = []
        static if not ELISA_TARGET_OS_WINDOWS:
            if not fill(p) or p.count != 16:
                panic("static-if branch container")
`
	buildAndRunSoATest(t, "static_if_auto_region", src, "bt")
}

// A struct local produced by a CALL to a fresh-literal factory and then forwarded to a growing
// callee (`t = new_table(); register(&t, x); return t`) gets the caller's ambient region: the
// factory is region-polymorphic because its region-carrying fields stay at their fresh defaults.
func TestCallProducedStructLocalForwardedToGrower(t *testing.T) {
	src := `
struct Table:
    names: mutable darray[sview] = []
    count: mutable usize = 0

def new_table(n: usize) -> Table:
    can Memory.Allocate, Abort.Panic:
        t: mutable Table = Table{}
        t.count <- n
        return t

def register(t: mutable Table&, name: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        t.names.push(name)

def build(name: sview) -> Table:
    can Memory.Allocate, Abort.Panic:
        t: mutable Table = new_table(4)
        register(&t, name)
        return t

def churn() -> usize:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[usize] = []
        for i in 0..<1000 |junk|:
            junk.push(i * 7)
        return junk[999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        x: Table = build("hello")
        c: usize = churn()
        if x.names.count != 1 or x.names[0] != "hello" or c != 6993:
            panic("factory call-result struct corrupted")
`
	buildAndRunSoATest(t, "factory_call_struct_local", src, "bt")
}

// Nested literal arguments and scalar leaf-field assignments keep a factory fresh (new_struct_table shape).
func TestNestedLiteralFactoryLocalForwardedToGrower(t *testing.T) {
	src := `
struct Target:
    bits: u32
    debug: mutable bool = false
    names: mutable darray[sview] = []

struct Table:
    target: Target
    names: mutable darray[sview] = []

def new_table(bits: u32) -> Table:
    can Memory.Allocate, Abort.Panic:
        t: mutable Table = Table{target: Target{bits: bits}}
        t.target.debug <- true if bits > 64
        return t

def register(t: mutable Table&, name: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        t.names.push(name)
        t.target.names.push(name)

def build(name: sview) -> Table:
    can Memory.Allocate, Abort.Panic:
        t: mutable Table = new_table(64)
        register(&t, name)
        return t

def churn() -> usize:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[usize] = []
        for i in 0..<1000 |junk|:
            junk.push(i * 7)
        return junk[999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        x: Table = build("hello")
        c: usize = churn()
        if x.names.count != 1 or x.target.names[0] != "hello" or c != 6993:
            panic("nested factory struct corrupted")
`
	buildAndRunSoATest(t, "factory_nested_literal_local", src, "bt")
}

// A by-value copy of a container ELEMENT (last = xs[i]) whose container field is grown by a callee threads the container's region.
func TestElementCopyFieldForwardedToGrower(t *testing.T) {
	src := `
struct Path:
    names: mutable darray[sview] = []

struct Exits:
    paths: mutable darray[Path] = []

def grow(names: mutable darray[sview]&, v: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        names.push(v)

def update(exits: mutable Exits&, v: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        exits.paths.push(Path{})
        last: mutable Path = exits.paths[exits.paths.count - 1]
        grow(last.names, v)
        exits.paths[exits.paths.count - 1] <- last

def churn() -> usize:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[usize] = []
        for i in 0..<1000 |junk|:
            junk.push(i * 7)
        return junk[999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        e: mutable Exits = Exits{}
        update(e, "a")
        update(e, "b")
        c: usize = churn()
        if e.paths.count != 2 or e.paths[1].names[0] != "b" or c != 6993:
            panic("element copy growth corrupted")
`
	buildAndRunSoATest(t, "element_copy_field_grower", src, "bt")
}

// An element of a local darray[darray[T]] (&adj[u]) forwarded to a container-growing helper grows in the outer container's region.
func TestNestedDarrayElementForwardedToGrower(t *testing.T) {
	src := `
def f(row: mutable darray[i32]&, v: i32) -> void:
    can Memory.Allocate, Abort.Panic:
        row.push(v)

def direct() -> i32:
    can Memory.Allocate, Abort.Panic:
        adj: mutable darray[darray[i32]] = []
        adj.push([])
        adj[0].push(1)
        return adj[0][0]

def via_helper() -> i32:
    can Memory.Allocate, Abort.Panic:
        adj: mutable darray[darray[i32]] = []
        adj.push([])
        f(&adj[0], 5)
        return adj[0][0]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if direct() != 1 or via_helper() != 5:
            panic("nested darray growth wrong")
`
	buildAndRunSoATest(t, "TestNestedDarrayElementForwardedToGrower", src, "bt")
}

// A field of a local struct (&t.text, &board.free_rows) forwarded to a container-growing helper grows in the struct's region.
func TestLocalStructFieldForwardedToGrower(t *testing.T) {
	src := `
struct Template:
    text: mutable darray[u8] = []
    count: mutable usize = 0

struct StrMap:
    slots: mutable darray[sview] = []

struct Board:
    free_rows: mutable darray[bool] = []

def la_append_text(out: mutable darray[u8]&, s: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<s.len |out, s|:
            out.push(s[i])

def q2() -> usize:
    can Memory.Allocate, Abort.Panic:
        t: mutable Template = Template{text: [], count: 0}
        la_append_text(&t.text, "x")
        return t.text.count

def setn(arr: mutable darray[bool]&, n: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        arr.resize(n)

def q4() -> usize:
    can Memory.Allocate, Abort.Panic:
        board: mutable Board = Board{}
        setn(&board.free_rows, 8)
        return board.free_rows.count

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        if q2() != 1 or q4() != 8:
            panic("field container growth wrong")
`
	buildAndRunSoATest(t, "TestLocalStructFieldForwardedToGrower", src, "bt")
}

// A generic struct ref (`v: mutable Vector[T]&`) whose darray field is grown threads the caller's region, for both a literal and a generic-factory local.
func TestGenericStructFieldGrowthThroughRef(t *testing.T) {
	src := `

struct Vector[T]:
    items: mutable darray[T]
    first: mutable usize

def vec_new[T]() -> Vector[T]:
    return Vector[T]{items: [], first: 0}

def vec_append[T](v: mutable Vector[T]&, x: T) can[Memory.Allocate]:
    v.items.push(x)

def vec_size[T](v: Vector[T]&) -> usize:
    return v.items.count - v.first

def vec_at[T](v: Vector[T]&, i: usize) -> T can[Abort.Panic]:
    return v.items[v.first + i]

def vec_remove_first[T](v: mutable Vector[T]&) -> T can[Abort.Panic]:
    v.first <- v.first + 1
    return v.items[v.first - 1]

enum Shape:
    Circle(r: i64)
    Rect(w: i64, h: i64)

def area(s: Shape) -> i64:
    return match s:
        Shape.Circle(r): r * 3
        Shape.Rect(w, h): w * h

@test
def bt() -> void can[Memory.Allocate, Memory.Release, Abort.Panic]:
    v: mutable Vector[i64] = vec_new[i64]()
    for i in 0..<20 |v|:
        vec_append(&v, i)
    x: i64 = vec_remove_first(&v)
    shapes: mutable Vector[Shape] = vec_new[Shape]()
    vec_append(&shapes, Shape.Circle(2))
    vec_append(&shapes, Shape.Rect(3, 4))
    got: i64 = vec_size(&v).i64() + x + vec_at(&v, 3) + area(vec_at(&shapes, 1))
    if got != 35:
        panic("generic container growth wrong")
`
	buildAndRunSoATest(t, "TestGenericStructFieldGrowthThroughRef", src, "bt")
}

// A generic struct's field forwarded to a generic darray[T]& grower threads the region through both generic functions.
func TestGenericStructFieldForwardedToGenericGrower(t *testing.T) {
	src := `

def da_append[T](v: mutable darray[T]&, x: T) can[Memory.Allocate]:
    v.push(x)

struct Vector[T]:
    items: mutable darray[T]
    first: mutable usize

def vec_new[T]() -> Vector[T]:
    return Vector[T]{items: [], first: 0}

def vec_append[T](v: mutable Vector[T]&, x: T) can[Memory.Allocate]:
    da_append(&v.items, x)

def vec_size[T](v: Vector[T]&) -> usize:
    return v.items.count - v.first

def vec_at[T](v: Vector[T]&, i: usize) -> T can[Abort.Panic]:
    return v.items[v.first + i]

def vec_remove_first[T](v: mutable Vector[T]&) -> T can[Abort.Panic]:
    v.first <- v.first + 1
    return v.items[v.first - 1]

enum Shape:
    Circle(r: i64)
    Rect(w: i64, h: i64)

def area(s: Shape) -> i64:
    return match s:
        Shape.Circle(r): r * 3
        Shape.Rect(w, h): w * h

@test
def bt() -> void can[Memory.Allocate, Memory.Release, Abort.Panic]:
    v: mutable Vector[i64] = vec_new[i64]()
    for i in 0..<20 |v|:
        vec_append(&v, i)
    x: i64 = vec_remove_first(&v)
    shapes: mutable Vector[Shape] = vec_new[Shape]()
    vec_append(&shapes, Shape.Circle(2))
    vec_append(&shapes, Shape.Rect(3, 4))
    got: i64 = vec_size(&v).i64() + x + vec_at(&v, 3) + area(vec_at(&shapes, 1))
    if got != 35:
        panic("generic container growth wrong")
`
	buildAndRunSoATest(t, "TestGenericStructFieldForwardedToGenericGrower", src, "bt")
}

// A generic grower over darray[RbNode[K, V]]& works on a local and on a struct field (&t.nodes).
func TestGenericGrowerOnStructFieldContainer(t *testing.T) {
	src := `

struct CallSign:
    value: i32

struct Vec2:
    x: f64
    y: f64

def compare_to(a: CallSign, b: CallSign) -> i32:
    if a.value == b.value:
        return 0
    if a.value < b.value:
        return -1
    return 1

def compare_to(a: Vec2, b: Vec2) -> i32:
    if a.x < b.x:
        return -1
    if a.x > b.x:
        return 1
    return 0

struct RbNode[K, V]:
    key: K
    value: mutable V
    left: mutable i32

def rb_put[K, V](nodes: mutable darray[RbNode[K, V]]&, key: K, value: V) -> i32 can[Memory.Allocate, Abort.Panic]:
    for i in 0..<nodes.count |nodes|:
        if compare_to(key, nodes[i].key) == 0:
            nodes[i].value <- value
            return i.i32()
    nodes.push(RbNode[K, V]{key: key, value: value, left: -1})
    return (nodes.count - 1).i32()

struct Tree:
    nodes: mutable darray[RbNode[Vec2, i32]]
    root: mutable i32

@test
def bt() -> void can[Memory.Allocate, Memory.Release, Abort.Panic]:
    a: mutable darray[RbNode[CallSign, f64]] = []
    _ = rb_put(&a, CallSign{value: 3}, 1.5)
    _ = rb_put(&a, CallSign{value: 4}, 1.5)
    _ = rb_put(&a, CallSign{value: 3}, 2.5)
    t: mutable Tree = Tree{nodes: [], root: -1}
    _ = rb_put(&t.nodes, Vec2{x: 1.0, y: 2.0}, 7)
    got: i64 = a.count.i64() * 100 + t.nodes[0].value.i64()
    if got != 207:
        panic("generic container growth wrong")
`
	buildAndRunSoATest(t, "TestGenericGrowerOnStructFieldContainer", src, "bt")
}

// A generic struct literal local forwarded by ref to a field grower, alongside the concrete form.
func TestGenericStructLiteralLocalForwardedToGrower(t *testing.T) {
	src := `
struct Vector[T]:
    items: mutable darray[T]
    first: mutable usize

struct IVec:
    items: mutable darray[i64]
    first: mutable usize

def vec_append[T](v: mutable Vector[T]&, x: T) can[Memory.Allocate]:
    v.items.push(x)

def ivec_append(v: mutable IVec&, x: i64) can[Memory.Allocate]:
    v.items.push(x)

@test
def bt() -> void can[Memory.Allocate, Memory.Release, Abort.Panic]:
    w: mutable IVec = IVec{items: [], first: 0}
    ivec_append(&w, 3)
    v: mutable Vector[i64] = Vector[i64]{items: [], first: 0}
    vec_append(&v, 3)
    if v.items.count + w.items.count != 2:
        panic("bad")
`
	buildAndRunSoATest(t, "TestGenericStructLiteralLocalForwardedToGrower", src, "bt")
}

// The rehash shape `map.slots <- fresh` through the sole `mutable T&` param: the fresh backing is built in the caller's region (ambient region bound to __rg_map) and survives the call and heap churn.
func TestStructFieldReplacedByFreshLocalSurvivesCall(t *testing.T) {
	src := `
struct IntMap:
    slots: mutable darray[i64] = []

def rehash(map: mutable IntMap&, n: usize) -> void:
    can Memory.Allocate, Abort.Panic:
        fresh: mutable darray[i64] = []
        for i in 0..<n |fresh|:
            fresh.push(i.i64() * 7)
        map.slots <- fresh

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        total: mutable i64 = 0
        for k in 0..<200 |total|:
            junk: mutable darray[i64] = []
            for j in 0..<64 |junk|:
                junk.push(-1)
            total <- total + junk[3]
        return total

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        m: mutable IntMap = IntMap{}
        rehash(&m, 40)
        c: i64 = churn()
        rehash(&m, 100)
        d: i64 = churn()
        total: mutable i64 = 0
        for i in 0..<m.slots.count |total|:
            total <- total + m.slots[i]
        if total != 34650 or c + d != -400:
            panic("rehash backing corrupted")
`
	buildAndRunSoATest(t, "TestStructFieldReplacedByFreshLocalSurvivesCall", src, "bt")
}

// The sound forms next to the rejected stash-into-struct-ref UAF (see
// TestCallStoringLocalDarrayIntoStructRefFieldRejected): a read-only callee and a same-region
// target still compile and run correctly.
func TestStructRefCallStoreSafeFormsRun(t *testing.T) {
	src := `
struct Holder:
    xs: mutable darray[i64] = []
    count: mutable usize = 0

def note(h: mutable Holder&, xs: darray[i64]) -> void:
    h.count <- xs.count

def stash(h: mutable Holder&, xs: darray[i64]) -> void:
    h.xs <- xs

def fill(h: mutable Holder&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[i64] = []
        for i in 0..<50 |local|:
            local.push(i.i64())
        note(h, local)

def same_scope() -> usize:
    can Memory.Allocate, Abort.Panic:
        g: mutable Holder = Holder{}
        local: mutable darray[i64] = []
        local.push(3)
        stash(g, local)
        return g.xs.count

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        h: mutable Holder = Holder{}
        fill(&h)
        if h.count != 50 or same_scope() != 1:
            panic("bad")
`
	buildAndRunSoATest(t, "struct_ref_call_store_safe", src, "bt")
}

// SOUNDNESS (silent UAF before): a struct-literal local whose darray field is grown, then stored
// whole (`o.inner <- local`) or by field (`o.inner.xs <- local.xs`) into the sole by-ref param,
// printed 0 instead of 499500 -- the backing lived in the callee's auto region. The local is now
// built in the caller's region (ambient region bound to __rg_o, as for the rehash shape).
func TestStructLocalStoredIntoRefParamFieldSurvivesCall(t *testing.T) {
	src := `
struct Inner:
    xs: mutable darray[u64]

struct Outer:
    inner: mutable Inner

def fill(o: mutable Outer&) -> void can[Abort.Panic, Memory.Allocate]:
    local: mutable Inner = Inner{xs: []}
    for i in 0..<1000 |local|:
        local.xs.push(i.u64())
    o.inner <- local

def fill_field(o: mutable Outer&) -> void can[Abort.Panic, Memory.Allocate]:
    local: mutable Inner = Inner{xs: []}
    for i in 0..<1000 |local|:
        local.xs.push(i.u64())
    o.inner.xs <- local.xs

def churn() -> u64 can[Abort.Panic, Memory.Allocate]:
    ys: mutable darray[u64] = []
    for i in 0..<5000 |ys|:
        ys.push(7)
    return ys[10]

def sum(o: Outer&) -> u64:
    total: mutable u64 = 0
    for x in o.inner.xs |total|:
        total <- total + x
    return total

@test
def bt() -> void can[Abort.Panic, Memory.Allocate, Memory.Release]:
    o: mutable Outer = Outer{inner: Inner{xs: []}}
    fill(&o)
    c: u64 = churn()
    p: mutable Outer = Outer{inner: Inner{xs: []}}
    fill_field(&p)
    d: u64 = churn()
    if sum(&o) != 499500 or sum(&p) != 499500 or c + d != 14:
        panic("struct-local field store UAF")
`
	buildAndRunSoATest(t, "struct_local_into_ref_param_field", src, "bt")
}

// SOUNDNESS (silent UAF before, also present in stage1's bam_capture_loop_exit_path): a void
// grower that builds a struct-literal local, grows its container field, and pushes it into its
// only mutable container param left the pushed fields in the callee's auto region. The
// ambient region is now bound to the target container's region (a read-only container param
// does not count as a second target), so the local is built in the caller's arena.
func TestVoidGrowerPushesGrownStructLocalSurvivesCall(t *testing.T) {
	src := `
struct Path:
    names: mutable darray[i64] = []

def capture(paths: mutable darray[Path]&, src: darray[i64]&) -> void:
    can Memory.Allocate, Abort.Panic:
        path: mutable Path = Path{}
        path.names.extend(src)
        paths.push(path)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        t: mutable i64 = 0
        for k in 0..<200 |t|:
            junk: mutable darray[i64] = []
            for j in 0..<64 |junk|:
                junk.push(-1)
            t <- t + junk[1]
        return t

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        src: mutable darray[i64] = []
        for i in 0..<50 |src|:
            src.push(i.i64())
        paths: mutable darray[Path] = []
        capture(paths, src)
        capture(paths, src)
        c: i64 = churn()
        total: mutable i64 = 0
        for p in paths |total|:
            for x in p.names |total|:
                total <- total + x
        if total != 2450 or c != -200:
            panic("void grower struct local UAF")
`
	buildAndRunSoATest(t, "void_grower_struct_local_push", src, "bt")
}

// A single-container void grower pushing a region-poly result (`dst.push(make_item(0))`): the
// ambient region is bound to dst's region, so make_item receives the caller's arena and the
// pushed backing survives the call and heap churn.
func TestVoidGrowerRegionPolyResultPushRuns(t *testing.T) {
	src := `
def make_item(n: i64) -> darray[i64]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[i64] = []
        for i in 0..<50 |out|:
            out.push(n + i.i64())
        return out

def fill(dst: mutable darray[darray[i64]]&) -> void:
    can Memory.Allocate, Abort.Panic:
        dst.push(make_item(0))

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        t: mutable i64 = 0
        for k in 0..<200 |t|:
            junk: mutable darray[i64] = []
            for j in 0..<64 |junk|:
                junk.push(-1)
            t <- t + junk[1]
        return t

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        rows: mutable darray[darray[i64]] = []
        fill(rows)
        fill(rows)
        c: i64 = churn()
        total: mutable i64 = 0
        for r in rows |total|:
            for x in r |total|:
                total <- total + x
        if total != 2450 or c != -200:
            panic("region-poly result push UAF")
`
	buildAndRunSoATest(t, "void_grower_region_poly_push", src, "bt")
}

// A factory whose struct local is built by a CALL (`b: mutable T = t_new()`), then grown in place
// (directly, or through a `mutable T&` grower) and returned must adopt the caller's region like a
// literal-built one: before, the grower call failed region inference ("cannot infer region
// parameter") and direct field growth had "no region to grow into".
func TestCallBuiltStructFactoryGrowsIntoCallerRegion(t *testing.T) {
	src := `
struct Big:
    negative: mutable bool
    limbs: mutable darray[u64]

def big_from(v: u64) -> Big:
    can Memory.Allocate, Abort.Panic:
        b: mutable Big = Big{negative: false, limbs: []}
        b.limbs.push(v)
        return b

def big_grow(b: mutable Big&, n: u64) -> bool:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<n |b|:
            b.limbs.push(i + 10)
        return true

def make_direct(v: u64) -> Big:
    can Memory.Allocate, Abort.Panic:
        b: mutable Big = big_from(v)
        for i in 0..<40 |b|:
            b.limbs.push(i.u64())
        return b

def make_via_grower(v: u64) -> Big:
    can Memory.Allocate, Abort.Panic:
        b: mutable Big = big_from(v)
        _ = big_grow(&b, 40)
        return b

def churn() -> u64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[u64] = []
        for i in 0..<1000 |junk|:
            junk.push(i.u64() * 7)
        return junk[999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        x: Big = make_direct(5)
        y: Big = make_via_grower(6)
        c: u64 = churn()
        if x.limbs.count != 41 or x.limbs[0] != 5 or x.limbs[40] != 39 or c != 6993:
            panic("direct call-built factory backing corrupted")
        if y.limbs.count != 41 or y.limbs[0] != 6 or y.limbs[40] != 49:
            panic("grower call-built factory backing corrupted")
`
	buildAndRunSoATest(t, "call_built_struct_factory", src, "bt")
}

// `flow.normal <- snapshot(names)`: a factory's result stored into a field of a `mutable T&`
// param is built in the param's (caller's) region. Before, the factory allocated into the
// setter's auto region, so the store was rejected as an `in auto:` escape.
func TestFactoryResultStoredIntoRefParamFieldLivesInCallerRegion(t *testing.T) {
	src := `
struct Snapshot:
    names: mutable darray[i64] = []

struct Flow:
    normal: mutable Snapshot = Snapshot{}
    seen: mutable bool = false

def snapshot(source: darray[i64]&) -> Snapshot:
    can Memory.Allocate, Abort.Panic:
        out: mutable Snapshot = Snapshot{}
        for v in source |out|:
            out.names.push(v * 2)
        return out

def set_normal(flow: mutable Flow&, source: darray[i64]&) -> void:
    can Memory.Allocate, Abort.Panic:
        flow.normal <- snapshot(source)
        flow.seen <- true

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<1000 |junk|:
            junk.push(i.i64() * 7)
        return junk[999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        source: mutable darray[i64] = []
        for i in 0..<40 |source|:
            source.push(i.i64())
        flow: mutable Flow = Flow{}
        set_normal(&flow, &source)
        c: i64 = churn()
        if not flow.seen or flow.normal.names.count != 40 or flow.normal.names[39] != 78 or c != 6993:
            panic("snapshot stored into the flow was freed with the setter's region")
`
	buildAndRunSoATest(t, "factory_into_ref_param_field", src, "bt")
}
