package semantic

import "testing"

// docs/128 §8: a local whose declared type reaches only program-lived bytes depends on no local
// region or parameter, whatever produced it -- its binding was checked against the program region
// when it was stored. The accepted cases below were false positives; the rejected ones pin that
// a shorter-lived value still cannot reach program-lived storage.
func TestProgramLivedLocalProvenance(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		// A program-lived binder read out of a local filled by a call that saw a parameter.
		{"call_filled_binder", `global region source

extern slice_of(src: u8&, len: usize) -> sview

struct Ann:
    name: sview @source
    region: sview @source

struct P:
    src: u8& @source
    anns: mutable darray[Ann] @source
    scratch: mutable darray[sview]

def arg(p: P&, out: mutable darray[sview @source]&) -> void:
    can Memory.Allocate:
        out.push(slice_of(p.src, 1))

def args(p: P&, n: u32) -> darray[sview @source] @source:
    can Memory.Allocate:
        found: mutable darray[sview @source] @source = []
        arg(p, found)
        return found

def go(p: lmut P, n: u32) -> void:
    can Memory.Allocate:
        p.scratch <- p.scratch.push("x")
        regions: mutable darray[sview @source] @source = []
        if n > 0:
            regions <- args(p, n)
        for r in regions:
            p.anns <- p.anns.push(Ann{name: "x", region: r})
`, nil},
		// A view-only struct built from a program-region darray local: neither the darray's
		// state nor its own (program-region) storage is a dependency.
		{"program_darray_view_dep", `global region source

struct Binding:
    elem: sview
    mode: u32

struct Scope:
    elems: mutable darray[sview]

def first(names: darray[sview @source] @source) -> Binding:
    return Binding{elem: names[0], mode: 1}

def put(scope: mutable Scope&, b: Binding) -> void:
    can Memory.Allocate:
        scope.elems <- scope.elems.push(b.elem)

def walk(scope: mutable Scope&) -> void:
    can Memory.Allocate:
        names: mutable darray[sview @source] @source = []
        names.push("a")
        b: Binding = first(names)
        put(scope, b)
`, nil},
		// The same shape over a local darray of a local string's views is still rejected.
		{"local_darray_view_dep", `global region source

struct Binding:
    elem: sview
    mode: u32

struct Scope:
    elems: mutable darray[sview]

def first(names: darray[sview]) -> Binding:
    return Binding{elem: names[0], mode: 1}

def put(scope: mutable Scope&, b: Binding) -> void:
    can Memory.Allocate:
        scope.elems <- scope.elems.push(b.elem)

def walk(scope: mutable Scope&) -> void:
    can Memory.Allocate:
        d: mutable dstr = []
        d.push(65)
        names: mutable darray[sview] = []
        names.push(d.as_sview())
        b: Binding = first(names)
        put(scope, b)
`, []string{":24:"}},
		// A program-lived binding cannot be initialised from a local string's view.
		{"local_view_into_program_local", `global region source

struct Ann:
    name: sview @source

struct P:
    anns: mutable darray[Ann] @source

def bad(p: lmut P) -> void:
    can Memory.Allocate:
        d: mutable dstr = []
        d.push(65)
        v: sview @source = d.as_sview()
        p.anns <- p.anns.push(Ann{name: v})
`, []string{":13:"}},
		// A program-lived field read through a parameter whose struct region is inferred.
		{"program_field_of_param", `global region source

struct Ann:
    name: sview @source

struct P:
    current: sview @source
    anns: mutable darray[Ann] @source
    scratch: mutable darray[sview]

def go(p: lmut P) -> void:
    can Memory.Allocate:
        p.scratch <- p.scratch.push("x")
        p.anns <- p.anns.push(Ann{name: p.current})

def go2(p: mutable P&) -> void:
    can Memory.Allocate:
        p.scratch.push("x")
        p.anns.push(Ann{name: p.current})
`, nil},
		// The same field without the program region keeps the parameter's region.
		{"plain_field_of_param", `global region source

struct Ann:
    name: sview @source

struct P:
    current: sview
    anns: mutable darray[Ann] @source
    scratch: mutable darray[sview]

def go(p: lmut P) -> void:
    can Memory.Allocate:
        p.scratch <- p.scratch.push("x")
        p.anns <- p.anns.push(Ann{name: p.current})

def go2(p: mutable P&) -> void:
    can Memory.Allocate:
        p.scratch.push("x")
        p.anns.push(Ann{name: p.current})
`, []string{":14:42", ":14:31", ":19:32", ":19:21"}},
		// A view sliced from a program-lived reference field (`u8& @source`) read through a
		// parameter, and the same with a plain `u8&` field (still rejected).
		{"program_ref_field_view", `global region source

struct Tok:
    start: u32
    len: u32

struct Ann:
    name: sview @source

struct P:
    src: u8& @source
    anns: mutable darray[Ann] @source
    scratch: mutable darray[sview]

def text(t: Tok&, src: u8&, n: usize) -> sview:
    trusted Unsafe.PointerCast:
        return src[0:n]

def go(p: lmut P, t: Tok) -> void:
    can Memory.Allocate:
        p.scratch <- p.scratch.push("x")
        p.anns <- p.anns.push(Ann{name: text(t, p.src, 1)})
`, nil},
		{"plain_ref_field_view", `global region source

struct Tok:
    start: u32
    len: u32

struct Ann:
    name: sview @source

struct P:
    src: u8&
    anns: mutable darray[Ann] @source
    scratch: mutable darray[sview]

def text(t: Tok&, src: u8&, n: usize) -> sview:
    trusted Unsafe.PointerCast:
        return src[0:n]

def go(p: lmut P, t: Tok) -> void:
    can Memory.Allocate:
        p.scratch <- p.scratch.push("x")
        p.anns <- p.anns.push(Ann{name: text(t, p.src, 1)})
`, []string{":22:45", ":22:31"}},
		// A declared-store constructor whose payload view comes from a program-lived reference.
		{"declared_ctor_payload_view", `global region source

enum Node layout(handle: u32) @source:
    pass

enum Expr is Node:
    Name(name: sview @source)
    Call(args: darray[Expr] @source)

def text(src: u8&, n: usize) -> sview:
    trusted Unsafe.PointerCast:
        return src[0:n]

def collect(src: u8& @source) -> darray[Expr] @source:
    can Memory.Allocate:
        out: mutable darray[Expr] @source = []
        out.push((Expr.Name(text(src, 1))))
        return out
`, nil},
		// Elements of a program-lived type pushed inside a loop that also reads them.
		{"loop_pushed_program_elements", `global region source

enum Node layout(handle: u32) @source:
    pass

enum Stmt is Node:
    Leaf(name: sview @source)
    If(then_body: darray[Stmt] @source, else_body: darray[Stmt] @source)

def count_leaves(statements: darray[Stmt]) -> usize:
    can Memory.Allocate:
        found: mutable usize = 0
        pending: mutable darray[Stmt] = []
        for statement in statements |pending|:
            pending.push(statement)
        while index < pending.count |index: usize = 0, pending, found|:
            statement: Stmt = pending[index]
            index <- index + 1
            match statement:
                Stmt.Leaf(name):
                    found <- found + 1
                Stmt.If(then_body, else_body):
                    for nested in then_body |pending|:
                        pending.push(nested)
                    for nested in else_body |pending|:
                        pending.push(nested)
        return found
`, nil},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
