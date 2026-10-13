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
