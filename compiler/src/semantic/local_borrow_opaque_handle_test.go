package semantic

import (
	"strings"
	"testing"
)

// An `extern` handle type (LLVMValueRef and friends) is a foreign pointer the language cannot
// dereference. A struct holding a darray of handles must not read as "may hold a borrow of any
// local": the self-hosted compiler's StructTable does, and passing it next to a local darray
// through a call was rejected as a dangling store.
func TestLocalBorrowOpaqueHandleHolderAllowed(t *testing.T) {
	src := `extern H

struct Table:
    names: mutable darray[sview]
    handles: mutable darray[H]

def add_name(dst: mutable darray[sview]&, n: sview) -> void can[Memory.Allocate]:
    can Memory.Allocate:
        dst.push(n)

def collect(t: mutable Table&, deps: mutable darray[sview]&) -> bool can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        add_name(deps, t.names[0])
        return true

def outer(t: mutable Table&) -> bool can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        deps: mutable darray[sview] = []
        ok: bool = collect(t, deps)
        return ok

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "opaque_handle_holder_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// A value whose type holds no reference, view or closure is a copy: a `ValueType{kind, bits}`
// computed from reference arguments carries no borrow of them, so passing it beside a writable
// parameter is not a store of a local.
func TestLocalBorrowBorrowFreeValueAllowed(t *testing.T) {
	src := `extern H

struct Shape:
    kind: u32
    bits: u32

struct Table:
    names: mutable darray[sview]
    shapes: mutable darray[Shape]
    handles: mutable darray[H]

def shape_of(names: darray[sview]&) -> Shape:
    return Shape{kind: 1, bits: 8}

def record(t: mutable Table&, shape: Shape, names: mutable darray[sview]&) -> void can[Memory.Allocate]:
    can Memory.Allocate:
        t.shapes.push(shape)

def outer(t: mutable Table&) -> void can[Memory.Allocate]:
    can Memory.Allocate:
        local: mutable darray[sview] = []
        s: Shape = shape_of(local)
        record(t, s, local)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "borrow_free_value_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// The binder shared by the alternatives of `e is A(items) or e is B(items)` is a payload of the
// parameter `e`, not a fresh frame local: iterating it and passing the elements beside a
// writable parameter stores nothing local.
func TestLocalBorrowOrPatternBinderAllowed(t *testing.T) {
	src := `enum Expr:
    Leaf(name: sview)
    Arr(items: darray[Expr], line: u32)
    Tup(items: darray[Expr], line: u32)

struct Table:
    names: mutable darray[sview]

def scan(e: Expr, flags: mutable darray[bool]&, t: mutable Table&) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        if e is Expr.Arr(items, l) or e is Expr.Tup(items, l2):
            for x in items |flags, t|:
                scan(x, flags, t)
            return
        if e is Expr.Leaf(n):
            t.names.push(n)

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "or_pattern_binder_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}
