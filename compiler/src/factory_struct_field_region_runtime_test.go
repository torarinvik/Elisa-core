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
