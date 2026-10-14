package main

import "testing"

// A function whose only writable target is a struct param (here `parser: lmut P`) and that
// stores a body-built value into it binds its ambient region to that param's region: `buf` and
// `captures` are allocated in the caller's region, so the views pushed into parser.params stay
// valid after the call (formerly rejected as a local-region escape).
func TestAmbientAdoptsBodyLocalStoredIntoSoleStructTarget(t *testing.T) {
	src := `
struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def fill[@r](parser: lmut P, items: mutable darray[Prm]& @r, bytes: mutable darray[u8]& @r) -> bool:
    can Memory.Allocate, Abort.Panic:
        for i in 0..<64 |bytes|:
            bytes.push(65 + (i % 26).u8())
        items.push(Prm{name: bytes.as_sview(), has_default: false})
        parser.position <- parser.position + 1
        return true

def handler(parser: lmut P) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] = []
        captures: mutable darray[Prm] = []
        rebind ignored: bool, parser = parser.fill(captures, buf)
        for capture in captures |parser|:
            parser.params <- parser.params.push(capture)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        p: mutable P = P{source: "", position: 0, params: []}
        rebind p = handler(p)
        c: i64 = churn()
        if p.params.count != 1 or p.params[0].name.len != 64 or c != 27993:
            panic("shape")
        if p.params[0].name[0] != 65 or p.params[0].name[63] != 65 + 11:
            panic("view bytes freed")
`
	buildAndRunSoATest(t, "ambient_adopt_lmut_views", src, "bt")
}

// The void-grower form with a struct target: a local struct built in the body and pushed into the
// sole writable target's container lives in the caller's region.
func TestAmbientAdoptsLocalStructPushedIntoStructTarget(t *testing.T) {
	src := `
struct Path:
    names: mutable darray[i64] = []

struct Log:
    paths: mutable darray[Path] = []

def record(log: mutable Log&, xs: darray[i64]&) -> void:
    can Memory.Allocate:
        p: mutable Path = Path{}
        p.names.extend(xs)
        log.paths.push(p)

def churn() -> i64:
    can Memory.Allocate, Abort.Panic:
        junk: mutable darray[i64] = []
        for i in 0..<4000 |junk|:
            junk.push(i.i64() * 7)
        return junk[3999]

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        log: mutable Log = Log{}
        src: mutable darray[i64] = []
        for i in 0..<50 |src|:
            src.push(i.i64())
        record(&log, &src)
        record(&log, &src)
        c: i64 = churn()
        if log.paths.count != 2 or log.paths[1].names.count != 50 or log.paths[0].names[49] != 49 or log.paths[1].names[49] != 49 or c != 27993:
            panic("recorded path freed with record's region")
`
	buildAndRunSoATest(t, "ambient_adopt_struct_push", src, "bt")
}
