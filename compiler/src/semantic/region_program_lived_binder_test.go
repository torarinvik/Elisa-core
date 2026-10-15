package semantic

import "testing"

// docs/128 §8 binder provenance. A local flow filled from an AST binder and merged into a
// caller's flow is accepted only when the binder is a handle into a declared program-lived
// store whose payloads are all program-lived (bqa). It is still rejected when the store is
// not declared (bqb), when a payload is not annotated program-lived (bqc), or when the local
// also holds a view of a local dstr (bqd).
func TestProgramLivedBinderProvenance(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"bqa", `global region source

enum Node layout(handle: u32) @source:
    pass

enum Expr is Node:
    Name(name: sview @source)
    Pair(left: Expr, right: Expr)

struct Flow:
    names: mutable darray[sview]

def fill(e: Expr, f: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Name(n):
                f.names.push(n)
            Expr.Pair(l, r):
                return

def merge(out: mutable Flow&, child: Flow&) -> void:
    can Memory.Allocate:
        for n in child.names:
            out.names.push(n)

def walk(e: Expr, out: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Pair(l, r):
                child: mutable Flow = Flow{names: []}
                fill(l, child)
                merge(out, child)
            Expr.Name(n):
                return
`, nil},
		{"bqb", `global region source

enum Node layout(handle: u32):
    pass

enum Expr is Node:
    Name(name: sview)
    Pair(left: Expr, right: Expr)

struct Flow:
    names: mutable darray[sview]

def fill(e: Expr, f: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Name(n):
                f.names.push(n)
            Expr.Pair(l, r):
                return

def merge(out: mutable Flow&, child: Flow&) -> void:
    can Memory.Allocate:
        for n in child.names:
            out.names.push(n)

def walk(e: Expr, out: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Pair(l, r):
                child: mutable Flow = Flow{names: []}
                fill(l, child)
                merge(out, child)
            Expr.Name(n):
                return
`, []string{":32:"}},
		{"bqc", `global region source

enum Node layout(handle: u32) @source:
    pass

enum Expr is Node:
    Name(name: sview)
    Pair(left: Expr, right: Expr)

struct Flow:
    names: mutable darray[sview]

def fill(e: Expr, f: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Name(n):
                f.names.push(n)
            Expr.Pair(l, r):
                return

def merge(out: mutable Flow&, child: Flow&) -> void:
    can Memory.Allocate:
        for n in child.names:
            out.names.push(n)

def walk(e: Expr, out: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Pair(l, r):
                child: mutable Flow = Flow{names: []}
                fill(l, child)
                merge(out, child)
            Expr.Name(n):
                return
`, []string{":32:"}},
		{"bqd", `global region source

enum Node layout(handle: u32) @source:
    pass

enum Expr is Node:
    Name(name: sview @source)
    Pair(left: Expr, right: Expr)

struct Flow:
    names: mutable darray[sview]

def fill(e: Expr, f: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Name(n):
                f.names.push(n)
            Expr.Pair(l, r):
                return

def merge(out: mutable Flow&, child: Flow&) -> void:
    can Memory.Allocate:
        for n in child.names:
            out.names.push(n)

def walk(e: Expr, out: mutable Flow&) -> void:
    can Memory.Allocate:
        match e:
            Expr.Pair(l, r):
                child: mutable Flow = Flow{names: []}
                fill(l, child)
                d: mutable dstr = []
                d.push(65)
                child.names.push(d.as_sview())
                merge(out, child)
            Expr.Name(n):
                return
`, []string{":35:"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
