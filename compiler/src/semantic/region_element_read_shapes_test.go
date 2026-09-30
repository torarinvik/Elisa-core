package semantic

import (
	"strings"
	"testing"
)

// A local `darray[sview]` filled by a callee that also received a local `darray[u32]`: the u32
// container's own storage holds no bytes an sview could view, so the sview elements read back
// out of it are no shorter-lived than the source they were copied from.
func TestRegionSviewFillBesideScalarContainerAllowed(t *testing.T) {
	src := `struct Param:
    name: sview

enum Decl:
    Func(name: sview, params: darray[Param], line: u32)
    Other(line: u32)

struct Diag:
    name: sview
    line: u32

struct Table:
    diagnostics: mutable darray[Diag]

def fill(names: mutable darray[sview]&, lines: mutable darray[u32]&) -> void can[Memory.Allocate]:
    can Memory.Allocate:
        names.push("a")
        lines.push(1)

def run(declarations: darray[Decl], table: lmut Table) -> void can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        for declaration in declarations |table| -> table:
            match declaration:
                Decl.Func(name, params, line):
                    names: mutable darray[sview] = []
                    lines: mutable darray[u32] = []
                    for param in params |names|:
                        names.push(param.name)
                    fill(names, lines)
                    while probe < names.count |probe: usize = 0, table| -> table:
                        table.diagnostics <- table.diagnostics.push(Diag{name: names[probe], line: lines[probe]})
                        probe <- probe + 1
                _:
                    pass

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "sview_fill_scalar_container_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}

// By-value reads of a tracked local container (`names[i]`) passed as a call argument or chosen by
// a ternary are element copies like a struct-literal field: they must keep the container's
// tracked element provenance instead of falling back to the container's own local region.
func TestRegionElementReadAsCallArgAndTernaryAllowed(t *testing.T) {
	src := `enum Expr:
    Bin(left: Expr, line: u32)
    Name(name: sview)

struct Diag:
    name: sview
    line: u32
    expected: sview
    actual: sview

struct Table:
    diagnostics: mutable darray[Diag]

def idx(names: darray[sview], name: sview) -> i64:
    return -1

def refs(body: darray[Expr], name: sview) -> bool:
    return false

def collect(expression: Expr, names: mutable darray[sview]&, types: mutable darray[sview]&, table: Table&) -> void can[Memory.Allocate]:
    can Memory.Allocate:
        match expression:
            Expr.Name(name):
                names.push(name)
                types.push("t")
            _:
                pass

def run(condition: Expr, body: darray[Expr], table: lmut Table) -> void can[Memory.Allocate, Abort.Panic]:
    can Memory.Allocate, Abort.Panic:
        match condition:
            Expr.Bin(left, line):
                ln: mutable darray[sview] = []
                lt: mutable darray[sview] = []
                rn: mutable darray[sview] = []
                rt: mutable darray[sview] = []
                collect(left, ln, lt, table)
                collect(left, rn, rt, table)
                while i < ln.count |table, i: usize = 0| -> table:
                    ri: i64 = idx(rn, ln[i])
                    missing: bool = ri < 0
                    right_type: sview = "" if missing else rt[ri.usize()]
                    mismatch: bool = missing or lt[i] != right_type
                    used: bool = refs(body, ln[i])
                    table.diagnostics <- table.diagnostics.push(Diag{name: ln[i], line: line, expected: lt[i], actual: right_type}) if mismatch and used
                    i <- i + 1
            _:
                pass

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "element_read_call_arg_ok.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}
