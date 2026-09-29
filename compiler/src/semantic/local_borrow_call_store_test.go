package semantic

import (
	"fmt"
	"strings"
	"testing"
)

// Calls that store into a writable argument, results that point into a reference argument's
// referent, and value-form loops in declaration position. Every rejected case was accepted before
// (a dangling reference at runtime); every allowed case is a shape the self-hosted compiler uses.

func TestLocalBorrowCallStoreAllowed(t *testing.T) {
	cases := map[string]string{
		"darray_field_param_contents": `struct Holder:
    buf: mutable darray[u8]

def fill(h: Holder&, out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        out.push(&h.buf[0])

def g(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        h: mutable Holder = Holder{buf: [1, 2, 3]}
        fill(&h, out)

def rd(p: u8&) -> u8:
    return p

def main() -> i32:
    can Memory.Allocate:
        out: mutable darray[u8&] = []
        g(&out)
        x: u8 = rd(out[0])
        return x.i32()
`,
		"darray_local_contents": `struct Holder:
    buf: mutable darray[u8]

def fill(h: Holder&, out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        out.push(&h.buf[0])

def g() -> darray[u8&]:
    can Memory.Allocate:
        h: mutable Holder = Holder{buf: [1, 2, 3]}
        out: mutable darray[u8&] = []
        fill(&h, &out)
        return out

def rd(p: u8&) -> u8:
    return p

def main() -> i32:
    can Memory.Allocate:
        out: darray[u8&] = g()
        x: u8 = rd(out[0])
        return x.i32()
`,
		"darray_param_passthrough": `struct Holder:
    buf: mutable darray[u8]

def fill(h: Holder&, out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        out.push(&h.buf[0])

def rd(p: u8&) -> u8:
    return p

def main() -> i32:
    can Memory.Allocate:
        h: mutable Holder = Holder{buf: [1, 2, 3]}
        out: mutable darray[u8&] = []
        fill(&h, &out)
        x: u8 = rd(out[0])
        return x.i32()
`,
		"while_is_match_binder": `enum Stmt:
    If(cond: sview, body: darray[Stmt], line: u32)
    Pass

struct Table:
    names: mutable darray[sview]

def visit(statements: darray[Stmt], table: lmut Table) -> void:
    can Memory.Allocate:
        pass

def walk(first: Stmt, table: lmut Table) -> void:
    can Memory.Allocate:
        current: mutable Stmt? = first
        while current is stmt:
            match stmt:
                Stmt.If(c, b, l):
                    table <- visit(b, table)
                    current = first
                _:
                    current = first
`,
		"push_back_into_itself": `struct Param:
    name: sview

enum Decl:
    Func(name: sview, params: darray[Param], body: darray[sview])
    Other

struct Table:
    names: mutable darray[sview]

def walk(body: darray[sview], names: darray[sview], table: lmut Table) -> void:
    can Memory.Allocate:
        pass

def check(declarations: darray[Decl], table: lmut Table) -> void:
    can Memory.Allocate:
        for declaration in declarations |table|:
            match declaration:
                Decl.Func(name, params, body):
                    local_names: mutable darray[sview] = []
                    for param in params:
                        if param.name != "":
                            local_names <- local_names.push(param.name)
                    table <- walk(body, local_names, table)
                _:
                    pass
`,
		"dict_put_back_into_itself": `struct Ann:
    name: sview
    line: u32

struct Src:
    heads: mutable dict[u64, u32]
    lines: mutable darray[u32]
    owners: mutable darray[sview]

struct Index:
    owners: mutable darray[sview]

def add(index: mutable Index&, owner: sview, member: sview) -> void:
    can Memory.Allocate:
        index.owners.push(owner)

def build(annotations: darray[Ann]&, index: mutable Index&) -> void:
    can Memory.Allocate:
        sources: mutable Src = Src{heads: {}, lines: [], owners: []}
        for annotation in annotations |sources|:
            sources.lines.push(annotation.line)
            sources.owners.push(annotation.name)
            sources.heads <- sources.heads.put(annotation.line.u64(), 0)
        for annotation in annotations |annotations, sources, index|:
            row: mutable u32 = 0
            while row < 3 |row, annotations, sources, annotation, index|:
                if sources.lines[row.usize()] == annotation.line:
                    add(index, sources.owners[row.usize()], annotation.name)
                row <- row + 1
`,
		"snapshot_of_local_by_ref": `struct Arm:
    guard: sview
    body: darray[sview]

enum Expr:
    Match(scrutinee: sview, arms: darray[Arm], line: u32)
    Leaf(name: sview)

struct Table:
    names: mutable darray[sview]

def snap(n: darray[sview]&) -> mutable darray[sview]:
    can Memory.Allocate:
        s: mutable darray[sview] = []
        s.extend(n)
        return s

def meet(n: mutable darray[sview]&, c: darray[sview]) -> void:
    pass

def check(e: sview, nuls: mutable darray[sview]&, table: lmut Table) -> void:
    pass

def walk(expression: Expr, nuls: mutable darray[sview]&, table: lmut Table) -> void:
    can Memory.Allocate:
        match expression:
            Expr.Match(scrutinee, arms, line):
                incoming: darray[sview] = snap(nuls)
                joined: mutable darray[sview] = snap(nuls)
                for arm in arms |table, incoming, joined|:
                    arm_nuls: mutable darray[sview] = snap(incoming)
                    table <- check(arm.guard, arm_nuls, table)
                    meet(joined, arm_nuls)
                meet(nuls, joined)
            _:
                pass

def main() -> i32:
    return 0
`,
		"value_loop_over_local_declaration": `struct Param:
    name: sview
    line: u32

struct P:
    source: sview
    pos: usize

def parse_list(parser: lmut P, items: mutable darray[Param]&, line: u32) -> bool:
    can Memory.Allocate:
        items.push(Param{name: parser.source, line: line})
        return true

def accept(parser: lmut P) -> bool:
    return true

def handler(parser: lmut P, line: u32) -> u32:
    can Memory.Allocate:
        captures: mutable darray[Param] = []
        rebind ok: bool, parser = parser.accept()
        if ok:
            rebind v: bool, parser = parser.parse_list(captures, line)
        n: u32 =
            for c in captures |count: u32 = 0| -> count:
                count <- count + 1
        return n

def main() -> i32:
    return 0
`,
		"lmut_rebind_result_pushed": `struct Tok:
    kind: u8
    start: usize

struct Decl:
    name: sview

struct P:
    source: sview
    tokens: mutable darray[Tok]
    pos: usize

def decl(parser: lmut P) -> Decl?:
    return Decl{name: parser.source}

def push_opt(parser: lmut P, out: mutable darray[Decl]&, node: Decl?) -> void:
    can Memory.Allocate:
        if node is d:
            out.push(d)

def dispatch(parser: lmut P, out: mutable darray[Decl]&, k: u32) -> bool:
    can Memory.Allocate:
        node: mutable Decl? = null
        match k:
            1:
                rebind parsed_node: Decl?, parser = parser.decl()
                node <- parsed_node
            _:
                return false
        parser <- parser.push_opt(out, node)
        return true

def main() -> i32:
    return 0
`,
	}
	for name, src := range cases {
		result := analyzeFunctionAnalysisTestSource(t, "local_borrow_call_store_ok_"+name+".elisa", src)
		if errs := result.Errors(); len(errs) != 0 {
			t.Fatalf("%s: must be allowed, got: %s", name, strings.Join(errs, "\n"))
		}
	}
}

func TestLocalBorrowCallStoreRejected(t *testing.T) {
	cases := map[string]struct {
		src   string
		lines []int
	}{
		"callee_pushes_holder_interior": {`struct Holder:
    buf: mutable u8[3]

def fill(h: Holder&, out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        out.push(&h.buf[0])

def g(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        h: mutable Holder = Holder{buf: [1, 2, 3]}
        fill(&h, out)

def rd(p: u8&) -> u8:
    return p

def main() -> i32:
    can Memory.Allocate:
        out: mutable darray[u8&] = []
        g(&out)
        x: u8 = rd(out[0])
        return x.i32()
`, []int{11}},
		"callee_fills_returned_local": {`struct Holder:
    buf: mutable u8[3]

def fill(h: Holder&, out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        out.push(&h.buf[0])

def g() -> darray[u8&]:
    can Memory.Allocate:
        h: mutable Holder = Holder{buf: [1, 2, 3]}
        out: mutable darray[u8&] = []
        fill(&h, &out)
        return out

def rd(p: u8&) -> u8:
    return p

def main() -> i32:
    can Memory.Allocate:
        out: darray[u8&] = g()
        x: u8 = rd(out[0])
        return x.i32()
`, []int{13}},
		"while_is_binder_over_local": {`def g(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        buf: mutable u8[3] = [1, 2, 3]
        cur: mutable u8&? = &buf[0]
        while cur is p:
            out.push(p)
            cur = null

def main() -> i32:
    can Memory.Allocate:
        out: mutable darray[u8&] = []
        g(&out)
        return 0
`, []int{6}},
		"push_back_then_escape": {`def keep(xs: darray[u8&], out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        out.push(xs[0])

def g(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        buf: mutable u8[3] = [1, 2, 3]
        xs: mutable darray[u8&] = []
        xs <- xs.push(&buf[0])
        keep(xs, out)

def h() -> darray[u8&]:
    can Memory.Allocate:
        buf: mutable u8[3] = [1, 2, 3]
        xs: mutable darray[u8&] = []
        xs <- xs.push(&buf[0])
        return xs
`, []int{10, 17}},
		"dict_put_then_owner_escape": {`struct Src:
    heads: mutable dict[u64, u32]
    owners: mutable darray[u8&]

struct Index:
    owners: mutable darray[u8&]

def add(index: mutable Index&, owner: u8&) -> void:
    can Memory.Allocate:
        index.owners.push(owner)

def build(index: mutable Index&) -> void:
    can Memory.Allocate:
        buf: mutable u8[3] = [1, 2, 3]
        sources: mutable Src = Src{heads: {}, owners: []}
        sources.owners.push(&buf[0])
        sources.heads <- sources.heads.put(1, 0)
        add(index, sources.owners[0])
`, []int{18}},
		"ref_param_interior_result": {`def first(a: u8[4]&) -> u8&:
    return &a[0]

def ident(n: darray[sview]&) -> darray[sview]&:
    return n

def bad1(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        buf: u8[4] = [1, 2, 3, 4]
        out.push(first(buf))

def bad2() -> darray[sview]&:
    can Memory.Allocate:
        xs: darray[sview] = []
        return ident(xs)

def bad3() -> u8&:
    buf: u8[4] = [1, 2, 3, 4]
    return first(buf)

def main() -> i32:
    return 0
`, []int{10, 15, 19}},
		"fixed_array_interior_result": {`def first(a: u8[4]&) -> u8&:
    return &a[0]

def bad3() -> u8&:
    buf: u8[4] = [1, 2, 3, 4]
    return first(&buf)

def bad4() -> u8&:
    buf: u8[4] = [1, 2, 3, 4]
    r: u8& = first(&buf)
    return r

def main() -> i32:
    return 0
`, []int{6, 11}},
		"ref_identity_of_filled_local": {`def ident(n: darray[sview]&) -> darray[sview]&:
    return n

def bad(p: sview) -> darray[sview]&:
    can Memory.Allocate:
        xs: mutable darray[sview] = []
        xs.push(p)
        return ident(xs)

def main() -> i32:
    return 0
`, []int{8}},
		"value_loop_stores_then_escape": {`def keep(out: mutable darray[u8&]&, xs: darray[u8&]) -> void:
    can Memory.Allocate:
        out.extend(xs)

def bad1(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        buf: u8[4] = [1, 2, 3, 4]
        tmp: mutable darray[u8&] = []
        n: u32 =
            for i in [1, 2] |tmp, acc: u32 = 0| -> acc:
                tmp.push(&buf[0])
                acc <- acc + 1
        keep(out, tmp)

def bad2(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        buf: u8[4] = [1, 2, 3, 4]
        tmp: mutable darray[u8&] = []
        tmp.push(&buf[0])
        n: u32 =
            for i in [1, 2] |out, acc: u32 = 0| -> acc:
                keep(out, tmp)
                acc <- acc + 1

def bad3() -> darray[u8&]:
    can Memory.Allocate:
        buf: u8[4] = [1, 2, 3, 4]
        tmp: mutable darray[u8&] = []
        n: u32 =
            for i in [1, 2] |tmp, acc: u32 = 0| -> acc:
                tmp.push(&buf[0])
                acc <- acc + 1
        return tmp

def main() -> i32:
    return 0
`, []int{13, 22, 33}},
		"darray_buffer_interior_result": {`def firstp(xs: darray[u8]&) -> u8&:
    return &xs[0]

def bad1() -> u8&:
    can Memory.Allocate:
        xs: mutable darray[u8] = [1, 2]
        return &xs[0]

def bad2() -> u8&:
    can Memory.Allocate:
        xs: mutable darray[u8] = [1, 2]
        return firstp(xs)

def bad3() -> u8&:
    can Memory.Allocate:
        xs: mutable darray[u8] = [1, 2]
        r: u8& = firstp(xs)
        return r

def main() -> i32:
    return 0
`, []int{7, 12, 18}},
		"discarded_call_stores_holder_interior": {`struct Holder:
    buf: mutable u8[3]

def fill(h: Holder&, out: mutable darray[u8&]&) -> u32:
    can Memory.Allocate:
        out.push(&h.buf[0])
        return 1

def g(out: mutable darray[u8&]&) -> void:
    can Memory.Allocate:
        h: mutable Holder = Holder{buf: [1, 2, 3]}
        _ = fill(&h, out)

def main() -> i32:
    can Memory.Allocate:
        out: mutable darray[u8&] = []
        g(&out)
        return 0
`, []int{12}},
		"local_darray_contents_via_ref_callee": {`def pick(v: darray[u8&]&) -> u8&:
    return v[0]

def fill(v: mutable darray[u8&]&, b: u8&) -> void:
    can Memory.Allocate:
        v.push(b)

def bad1(g: u8&) -> u8&:
    can Memory.Allocate:
        b: u8 = 7
        xs: mutable darray[u8&] = []
        xs.push(&b)
        return pick(&xs)

def bad2(g: u8&) -> u8&:
    can Memory.Allocate:
        b: u8 = 7
        xs: mutable darray[u8&] = []
        fill(&xs, &b)
        return pick(&xs)

def ok1(g: u8&) -> u8&:
    can Memory.Allocate:
        xs: mutable darray[u8&] = []
        xs.push(g)
        return pick(&xs)

def main() -> i32:
    can Memory.Allocate:
        g: u8 = 1
        r: u8& = ok1(&g)
        return 0
`, []int{13, 20}},
	}
	for name, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "local_borrow_call_store_bad_"+name+".elisa", c.src)
		errs := result.Errors()
		for _, line := range c.lines {
			found := false
			for _, err := range errs {
				if strings.Contains(err, fmt.Sprintf(".elisa:%d:", line)) && (strings.Contains(err, "function-local storage") || strings.Contains(err, "region dependency facts include local region")) {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: want line %d rejected as a function-local borrow escape, got: %s", name, line, strings.Join(errs, "\n"))
			}
		}
	}
}
