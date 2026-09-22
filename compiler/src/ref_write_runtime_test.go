package main

import (
	"strings"
	"testing"
)

// `r <- v` through a reference: a pointer value rebinds a rebindable reference, anything else
// is stored through it. These pin what each spelling DOES at runtime, beside the analyzer tests
// in semantic/ref_write_capability_test.go that pin which spellings compile.
const refWriteThroughBody = `
struct Node:
    value: mutable i64

error MemoryError:
    OutOfMemory

def copy_i64(r: mutable i64&, s: i64&) -> i64:
    r <- s
    return r

def copy_i32(r: mutable i32&, s: i32&) -> void:
    r <- s

def get_rw(ok: bool, n: mutable Node&) -> mutable Node& error[MemoryError]:
    if not ok:
        raise MemoryError.OutOfMemory
    return n

def get_ro(ok: bool, n: mutable Node&) -> Node& error[MemoryError]:
    r: Node& = try get_rw(ok, n)
    return r

def bump(ok: bool, n: mutable Node&) -> i64 error[MemoryError]:
    w: mutable Node& = try get_rw(ok, n)
    w.value <- w.value + 10
    return w.value

def generic_size[T](x: mutable T&) -> usize:
    return size_of(T)

@test
def legacy_binding_writes_through() -> void:
    v: mutable i64 = 1
    x: mutable i64& = &v
    x <- 5
    if v != 5:
        panic("a legacy writable binding did not write through")

@test
def legacy_binding_rebinds_then_writes() -> void:
    v: mutable i64 = 1
    w: mutable i64 = 7
    x: mutable i64& = &v
    x <- &w
    x <- 9
    if v != 1 or w != 9:
        panic("a pointer store must rebind and a value store must write the new referent")

@test
def reference_argument_copies_the_value() -> void:
    a: mutable i64 = 1
    b: i64 = 2
    if copy_i64(&a, &b) != 2 or a != 2:
        panic("storing a reference through a writable i64 reference must copy the referent")
    c: mutable i32 = 1
    d: mutable i32 = 2
    e: mutable i32 = 3
    copy_i32(&d, &c)
    if c != 1 or d != 1 or e != 3:
        panic("storing a reference through a writable i32 reference must copy the referent")

@test
def binding_qualifier_and_ref_capability_are_independent() -> void:
    first: i64 = 5
    second: mutable i64 = 7
    mutable readonly_view: i64& = &first
    readonly_view <- &second
    writable_view: mutable i64& = &second
    writable_view <- 9
    if second != 9 or first != 5 or readonly_view != 9:
        panic("rebinding a read-only view or writing a writable one went to the wrong place")

@test
def fallible_writable_return_writes_through() -> void:
    n: mutable Node = Node{value: 5}
    a: i64 = try bump(true, &n) else -1
    b: i64 = try bump(false, &n) else -1
    c: Node& = try get_ro(true, &n) else &n
    if a != 15 or b != -1 or c.value != 15 or n.value != 15:
        panic("a mutable T& error[E] result did not write through to the caller's value")

# A reference argument to a generic T& parameter binds T to its referent; &slot binds T to the
# reference type and passes the slot. A NULLABLE reference argument used to pass its slot while
# binding T to the referent -- the callee read the pointer bits as a Node -- and is now rejected
# (semantic/nullable_scalar_value_read_test.go).
@test
def generic_reference_parameter_binds_referent_or_slot() -> void:
    v: mutable i32 = 3
    r: mutable i32& = &v
    q: mutable i32&? = &v
    if generic_size(r) != 4 or generic_size(&q) != 8 or generic_size(v) != 4:
        panic("a generic T& parameter bound the wrong type for a reference argument")
`

func TestRefWriteThroughRuntimeSemantics(t *testing.T) {
	exit, stdout, stderr := runLmutProgram(t, "ref_write_through", refWriteThroughBody)
	if exit != 0 || !strings.Contains(stdout, "failed=0") {
		t.Fatalf("reference write-through program did not pass cleanly: exit=%d\nstdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	}
	for _, name := range []string{
		"legacy_binding_writes_through",
		"legacy_binding_rebinds_then_writes",
		"reference_argument_copies_the_value",
		"binding_qualifier_and_ref_capability_are_independent",
		"fallible_writable_return_writes_through",
		"generic_reference_parameter_binds_referent_or_slot",
	} {
		if !strings.Contains(stdout, "[       OK ] "+name) {
			t.Fatalf("test %s did not run and pass:\n%s", name, stdout)
		}
	}
}
