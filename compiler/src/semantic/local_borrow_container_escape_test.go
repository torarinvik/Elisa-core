//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A reference to a function-local escaping through a CONTAINER or an effect block. Before the fix
// all of these were accepted: `out.push(&x)` into a caller's darray read garbage after the return
// (exit 1 where the non-escaping control exits 5), and every aggregate return nested in a
// `can ...:` block skipped the return-flow summary entirely.

const localBorrowEscapePrelude = `struct H:
    r: i64&

`

func TestLocalBorrowContainerEscapeRejected(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"push_into_out_param": {`def f(out: mutable darray[i64&]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out.push(&x)
`, "darray push stores a reference to function-local storage"},
		"push_struct_into_out_param": {`def f(out: mutable darray[H]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out.push(H{r: &x})
`, "darray push stores a reference to function-local storage"},
		"return_pushed_local": {`def f() -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out: mutable darray[i64&] = []
        out.push(&x)
        return out
`, "contains a reference into function-local storage"},
		"return_list_literal": {`def f() -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out: mutable darray[i64&] = [&x]
        return out
`, "contains a reference into function-local storage"},
		"store_struct_into_out_field": {`struct O:
    h: mutable H

def f(out: mutable O&) -> void:
    x: i64 = 5
    out.h <- H{r: &x}
`, "storing an aggregate that contains a reference to function-local storage"},
		"store_bound_struct_into_out_field": {`struct O:
    h: mutable H

def f(out: mutable O&) -> void:
    x: i64 = 5
    h: H = H{r: &x}
    out.h <- h
`, "storing an aggregate that contains a reference to function-local storage"},
		"push_through_loop_over_local_refs": {`def f(out: mutable darray[i64&]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        xs: mutable darray[i64&] = []
        xs.push(&x)
        for r in xs:
            out.push(r)
`, "darray push stores a reference to function-local storage"},
		"push_through_loop_over_local_structs": {`def f(out: mutable darray[H]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        hs: mutable darray[H] = []
        hs.push(H{r: &x})
        for h in hs:
            out.push(h)
`, "darray push stores a reference to function-local storage"},
		"push_ref_read_from_local_holder": {`def ref_of(h: H&) -> i64&:
    return h.r

def f(out: mutable darray[i64&]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        h: H = H{r: &x}
        out.push(ref_of(h))
`, "darray push stores a reference to function-local storage"},
		"return_address_of_local_through_helper": {`struct A:
    owner: sview

def addr_of(a: A&) -> A&:
    return a

def f() -> A&:
    local: A = A{owner: "x"}
    return addr_of(local)
`, "contains a reference into function-local storage"},
		"return_local_filled_by_helper": {`def add(xs: mutable darray[i64&]&, r: i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        xs.push(r)

def f() -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out: mutable darray[i64&] = []
        add(out, &x)
        return out
`, "contains a reference into function-local storage"},
		"return_local_filled_by_helper_through_value_wrapper": {`def add(xs: mutable darray[i64&]&, r: i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        xs.push(r)

def wrap(xs: darray[i64&]) -> darray[i64&]:
    return xs

def f() -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out: mutable darray[i64&] = []
        add(out, &x)
        return wrap(out)
`, "contains a reference into function-local storage"},
		"return_local_pushed_through_value_wrapper": {`def wrap(xs: darray[i64&]) -> darray[i64&]:
    return xs

def f() -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out: mutable darray[i64&] = []
        out.push(&x)
        return wrap(out)
`, "contains a reference into function-local storage"},
		// The loop's next iteration pushes `&x` into `hs`, which the earlier-in-body read `y` sees.
		"push_value_read_before_later_store": {`def f(out: mutable darray[H]&, p: i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        hs: mutable darray[H] = []
        hs.push(H{r: p})
        while true:
            y: H = hs[0]
            out.push(y)
            hs.push(H{r: &x})
            if hs.count > 3:
                break
`, "darray push stores a reference to function-local storage"},
		"push_holder_after_field_store": {`struct M:
    r: mutable i64&

def f(out: mutable darray[M]&, p: i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        h: mutable M = M{r: p}
        h.r <- &x
        out.push(h)
`, "darray push stores a reference to function-local storage"},
		"push_slice_of_local_buffer": {`def f(out: mutable darray[view[u8]]&) -> void:
    can Memory.Allocate, Abort.Panic:
        bufs: mutable darray[darray[u8]] = []
        b: mutable darray[u8] = []
        b.push(u8(1))
        bufs.push(b)
        for x in bufs:
            out.push(x[0:1])
`, "darray push stores a reference to function-local storage"},
		"return_loop_binding_over_local_holders": {`def f(z: i64&) -> H:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        hs: mutable darray[H] = []
        hs.push(H{r: &x})
        for h in hs:
            return h
        return H{r: z}
`, "contains a reference into function-local storage"},
		"push_through_helper_filled_by_implicit_borrow": {`def add(xs: mutable darray[i64&]&, r: i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        xs.push(r)

def f(out: mutable darray[i64&]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        tmp: mutable darray[i64&] = []
        add(tmp, x)
        for r in tmp:
            out.push(r)
`, "darray push stores a reference to function-local storage"},
		"return_struct_in_can_block": {`def f() -> H:
    can Abort.Panic:
        x: i64 = 5
        return H{r: &x}
`, "contains a reference into function-local storage"},
		"return_value_form_loop_accumulator": {`def f(xs: darray[i64]&) -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        acc: mutable darray[i64&] = []
        for v in xs |acc| -> acc:
            acc.push(&x)
        return acc
`, "contains a reference into function-local storage"},
		"push_rebindable_ref_local": {`def f(out: mutable darray[i64&]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        r: mutable i64& = &x
        out.push(r)
`, "darray push stores a reference to function-local storage"},
		"push_ref_local_rebound_to_local": {`def f(out: mutable darray[i64&]&, y: mutable i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        r: mutable i64& = y
        r <- &x
        out.push(r)
`, "darray push stores a reference to function-local storage"},
		"push_ref_local_rebound_in_value_form_loop": {`def f(xs: darray[i64]&, out: mutable darray[i64&]&, y: mutable i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        r: mutable i64& = y
        for v in xs |r| -> r:
            r <- &x
        out.push(r)
`, "darray push stores a reference to function-local storage"},
	}
	for name, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "local_borrow_escape_"+name+".elisa", localBorrowEscapePrelude+c.body)
		all := strings.Join(result.Errors(), "\n")
		if !strings.Contains(all, c.want) {
			t.Fatalf("%s: a local borrow escaping through a container must be rejected with %q, got: %s", name, c.want, all)
		}
	}
}

func TestLocalBorrowContainerEscapeAllowed(t *testing.T) {
	cases := map[string]string{
		// Forwarding a caller-owned reference into the caller's container is fine.
		"push_param_ref": `def f(out: mutable darray[i64&]&, x: i64&) -> void:
    can Memory.Allocate, Abort.Panic:
        out.push(x)
`,
		// The caller's container keeps the POINTEE's value, not the reference.
		"push_value_of_local_borrow": `def f(out: mutable darray[i64]&) -> void:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        h: H = H{r: &x}
        out.push(h.r)
`,
		// A rebindable reference local that only ever points into caller storage.
		"push_rebindable_ref_into_param": `def f(xs: mutable darray[i64]&, out: mutable darray[i64&]&) -> void:
    can Memory.Allocate, Abort.Panic:
        r: mutable i64& = &xs[0]
        out.push(r)
`,
		// A value-form loop accumulating owned values, drained after the loop.
		"value_form_loop_owned_accumulator": `def f(xs: darray[i64]&, out: mutable darray[i64]&) -> void:
    can Memory.Allocate, Abort.Panic:
        acc: mutable darray[i64] = []
        for v in xs |acc| -> acc:
            acc.push(v)
        for a in acc:
            out.push(a)
`,
		// A local container of local borrows that never leaves the frame.
		"local_only": `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        out: mutable darray[i64&] = []
        out.push(&x)
        return out[0]
`,
		// Rebinding a local aggregate to one holding a local borrow never leaves the frame.
		"local_aggregate_rebind": `def f() -> i64:
    x: i64 = 5
    y: i64 = 6
    h: mutable H = H{r: &x}
    h <- H{r: &y}
    return h.r
`,
		// A view READ through a local's address points wherever the view points (the caller's
		// data), not into the local: `name_of(local)` implicitly passes `&local`.
		"view_read_through_local_copy": `struct A:
    owner: sview

def name_of(a: A&) -> sview:
    can Abort.Panic:
        return a.owner

def f(items: darray[A]&) -> sview:
    can Abort.Panic:
        local: A = items[0]
        return name_of(local)
`,
		"ref_read_through_local_holder_of_param": `def ref_of(h: H&) -> i64&:
    return h.r

def f(x: i64&) -> i64&:
    h: H = H{r: x}
    return ref_of(h)
`,
		// A loop binding over a caller's container holds only what the container holds.
		"push_view_read_through_loop_binding": `struct A:
    owner: sview

struct D:
    name: sview

struct T:
    diags: mutable darray[D]

def name_of(a: A&) -> sview:
    return a.owner

def f(items: darray[A]&, t: lmut T) -> void:
    can Memory.Allocate, Abort.Panic:
        for a in items |t|:
            t.diags <- t.diags.push(D{name: name_of(a)})
`,
		// A local darray handed BY VALUE to a helper that returns it holds only what was stored.
		"return_local_darray_through_value_wrapper": `def wrap(xs: darray[i64&]) -> darray[i64&]:
    return xs

def f(x: i64&) -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[i64&] = []
        out.push(x)
        return wrap(out)
`,
		// A read-only reference argument reaches a writable one only through what it HOLDS: a
		// `darray[sview]` cannot hold a borrow into a `darray[sview]`.
		"helper_copies_views_out_of_local_darray": `def collect(src: darray[sview]&, out: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for s in src:
            out.push(s)

def f(names: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        local: mutable darray[sview] = []
        local.push("a")
        found: mutable darray[sview] = []
        collect(&local, &found)
        for s in found:
            names.push(s)
`,
		// elisa-proof's package reader: a `(known, value)` tuple of views into caller data.
		"tuple_of_caller_views": `def pick(ok: bool, fallback: sview) -> (known: bool, value: sview):
    empty: sview = ""
    text: sview = fallback if ok else empty
    return (false, empty) if not ok
    return (true, text)
`,
		// A discarded builtin-method result aliases nothing.
		"push_then_return_through_value_wrapper": `def wrap(xs: darray[i64&]) -> darray[i64&]:
    return xs

def f(x: i64&) -> darray[i64&]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[i64&] = []
        out.push(x)
        out.push(x)
        return wrap(out)
`,
		// Returning a struct holding a caller borrow from inside an effect block.
		"return_param_borrow_in_can_block": `def f(x: i64&) -> H:
    can Abort.Panic:
        return H{r: x}
`, // A value-form loop in one match arm threads the lmut table; a later arm's view read out
		// of the table is still the caller's storage (arms join, they do not run in sequence).
		"lmut_table_view_after_sibling_value_form_loop": `struct D:
    name: sview
    line: u32

struct T:
    diagnostics: mutable darray[D]
    names: darray[sview]

enum E:
    Ident(name: sview, line: u32)
    List(items: darray[E])

def walk(e: E, table: lmut T) -> void:
    can Memory.Allocate, Abort.Panic:
        match e:
            E.List(items):
                for item in items |table| -> table:
                    table <- walk(item, table)
            E.Ident(name, line):
                first: sview = table.names[0]
                table.diagnostics <- table.diagnostics.push(D{name: first, line: line})
`,
		// The loop binder of a value-form loop is modeled like any other loop binder.
		"lmut_table_view_after_value_form_loop": `struct D:
    name: sview
    line: u32

struct T:
    diagnostics: mutable darray[D]
    names: darray[sview]

enum E:
    Ident(name: sview, line: u32)
    List(items: darray[E])

def walk(e: E, table: lmut T) -> void:
    can Memory.Allocate, Abort.Panic:
        match e:
            E.List(items):
                for item in items |table| -> table:
                    table <- walk(item, table)
                first: sview = table.names[0]
                table.diagnostics <- table.diagnostics.push(D{name: first, line: 1})
            E.Ident(name, line):
                pass
`,
	}
	for name, body := range cases {
		result := analyzeFunctionAnalysisTestSource(t, "local_borrow_ok_"+name+".elisa", localBorrowEscapePrelude+body)
		if errs := result.Errors(); len(errs) != 0 {
			t.Fatalf("%s: must be allowed, got: %s", name, strings.Join(errs, "\n"))
		}
	}
}

// Each return is judged by what IT returns: the loop binding over local holders escapes, the
// later `H{r: z}` of a caller borrow does not.
func TestLocalBorrowEscapeBlamesOnlyTheEscapingReturn(t *testing.T) {
	source := localBorrowEscapePrelude + `def f(z: i64&) -> H:
    can Memory.Allocate, Abort.Panic:
        x: i64 = 5
        hs: mutable darray[H] = []
        hs.push(H{r: &x})
        for h in hs:
            return h
        return H{r: z}
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "local_borrow_escape_per_return.elisa", source)
	errs := result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":10:") {
		t.Fatalf("want exactly one error, on the loop-binding return (line 10), got: %s", strings.Join(errs, "\n"))
	}
}

// A reference local bound to a local owner (`al: mutable A& = &owner`) does not by itself make the
// owner hold a frame borrow: only stores THROUGH it land in the owner. Before, declaring `al`
// poisoned `owner`, rejecting json_parser's `internal.arena <- arena_owner`.
const localBorrowRefAliasPrelude = `struct R:
    n: i64
struct A:
    begin: mutable R&?
    k: mutable i64
struct H:
    a: mutable A

`

func TestLocalBorrowRefAliasOwnerAllowed(t *testing.T) {
	cases := map[string]string{
		"alias_declared_only": `def f(h: mutable H&, r: mutable R&) -> void:
    owner: mutable A = A{begin: r, k: 0}
    al: mutable A& = &owner
    h.a <- owner
`,
		"alias_writes_scalar": `def f(h: mutable H&, r: mutable R&) -> void:
    owner: mutable A = A{begin: r, k: 0}
    al: mutable A& = &owner
    al.k <- 4
    h.a <- owner
`,
		// Arena-shaped: only `heap` references cannot hold a frame borrow, whatever else the call
		// that receives the alias is handed.
		"heap_only_owner_passed_with_local": `struct P:
    first: mutable heap R&?
struct Q:
    p: mutable P
def fill(p: mutable P&, s: mutable A&) -> void:
    s.k <- 1
def f(q: mutable Q&) -> void:
    owner: mutable P = P{first: null}
    al: mutable P& = &owner
    s: mutable A = A{begin: null, k: 0}
    fill(al, &s)
    q.p <- owner
`,
	}
	for name, body := range cases {
		result := analyzeFunctionAnalysisTestSource(t, "local_borrow_ref_alias_ok_"+name+".elisa", localBorrowRefAliasPrelude+body)
		if errs := result.Errors(); len(errs) != 0 {
			t.Fatalf("%s: must be allowed, got: %s", name, strings.Join(errs, "\n"))
		}
	}
}

// A frame borrow stored through the alias still reaches the owner's store, before or after it.
func TestLocalBorrowRefAliasOwnerRejected(t *testing.T) {
	cases := map[string]struct {
		body string
		line string
	}{
		"store_through_alias": {`def f(h: mutable H&, r: mutable R&) -> void:
    loc: mutable R = R{n: 2}
    owner: mutable A = A{begin: r, k: 0}
    al: mutable A& = &owner
    al.begin <- &loc
    h.a <- owner
`, ":14:"},
		"store_through_alias_next_iteration": {`def f(h: mutable H&, r: mutable R&) -> void:
    loc: mutable R = R{n: 2}
    owner: mutable A = A{begin: r, k: 0}
    al: mutable A& = &owner
    for i in 0 ..< 2:
        h.a <- owner
        al.begin <- &loc
`, ":14:"},
		"alias_of_alias": {`def g(x: mutable A&, l: mutable R&) -> void:
    x.k <- l.n
def f(h: mutable H&, r: mutable R&) -> void:
    loc: mutable R = R{n: 2}
    owner: mutable A = A{begin: r, k: 0}
    al: mutable A& = &owner
    a2: mutable A& = al
    a2.begin <- &loc
    h.a <- owner
`, ":17:"},
	}
	for name, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "local_borrow_ref_alias_bad_"+name+".elisa", localBorrowRefAliasPrelude+c.body)
		errs := result.Errors()
		found := false
		for _, err := range errs {
			if strings.Contains(err, c.line) && strings.Contains(err, "storing an aggregate that contains a reference to function-local storage") {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: want the owner store (line %s) rejected, got: %s", name, c.line, strings.Join(errs, "\n"))
		}
	}
}
