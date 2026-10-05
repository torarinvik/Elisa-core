package semantic

import (
	"strings"
	"testing"
)

// Regression tests for the local-borrow escape false-positive reductions and the negatives that
// must keep rejecting. Positive cases must analyze without errors; negative cases must report a
// region escape.

type localBorrowEscapeCase struct {
	name string
	src  string
	ok   bool
}

var localBorrowEscapeCases = []localBorrowEscapeCase{
	// A header-only darray[sview] argument holds no bytes a view can point into: passing it beside a filled container does not taint that container.
	{name: "HeaderOnlyArgument", ok: true, src: `enum Ex:
    Leaf(name: sview, kids: darray[i64])
    Empty

def scan(body: darray[Ex]&, binders: mutable darray[sview]&, places: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    for item in body |places|:
        places.push(item)

def collect(body: darray[Ex]&, out: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    binders: mutable darray[sview] = []
    binders.push("a")
    places: mutable darray[Ex] = []
    scan(body, binders, places)
    index: mutable usize = 0
    while index < places.count |index|:
        out.push(places[index])
        index <- index + 1

def main() -> i32:
    return 0
`},
	// The same argument stored as a struct field is a real escape of the local header.
	{name: "HeaderOnlyArgumentStoredHeaderEscapes", ok: false, src: `struct H:
    xs: darray[sview]

def scan(binders: darray[sview], places: mutable darray[H]&) -> void can[Memory.Allocate, Abort.Panic]:
    places.push(H{xs: binders})

def collect(out: mutable darray[H]&) -> void can[Memory.Allocate, Abort.Panic]:
    binders: mutable darray[sview] = []
    binders.push("a")
    places: mutable darray[H] = []
    scan(binders, places)
    index: mutable usize = 0
    while index < places.count |index|:
        out.push(places[index])
        index <- index + 1

def main() -> i32:
    return 0
`},
	// xs <- call(...) replaces the local container's element state with the callee's return summary.
	{name: "CallAssignKeepsCalleeProvenance", ok: true, src: `struct Diag:
    name: sview
    expected: sview

struct Table:
    owners: darray[sview]
    names: darray[sview]
    diags: mutable darray[Diag]

def refs(table: Table&, owner: sview) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        collected: mutable darray[sview] = []
        while i < table.owners.count |i: usize = 0|:
            collected.push(table.names[i]) if table.owners[i] == owner
            i <- i + 1.usize()
        return collected

def check(owner: sview, table: lmut Table) -> void:
    can Memory.Allocate, Abort.Panic:
        required_effects: mutable darray[sview] = []
        required_effects <- refs(table, owner)
        for required in required_effects |table, owner|:
            table.diags <- table.diags.push(Diag{name: owner, expected: required})

def main() -> i32:
    return 0
`},
	// The same, reassigned inside a loop from caller-owned data.
	{name: "CallAssignInLoopKeepsCalleeProvenance", ok: true, src: `struct Diag:
    expected: sview

def pick(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview] = []
        out.push(names[0])
        return out

def check(names: darray[sview]&, diags: mutable darray[Diag]&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: darray[u8] = [65, 66]
        local: mutable darray[sview] = []
        local.push(buf.as_sview())
        required_effects: mutable darray[sview] = pick(names)
        while i < 3 |i: usize = 0, required_effects, diags|:
            diags.push(Diag{expected: required_effects[0]})
            required_effects <- pick(names)
            i <- i + 1.usize()

def main() -> i32:
    return 0
`},
	// xs <- call(local) inherits the local's provenance.
	{name: "CallAssignFromLocalEscapes", ok: false, src: `struct Diag:
    expected: sview

def pick(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview] = []
        out.push(names[0])
        return out

def check(diags: mutable darray[Diag]&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: darray[u8] = [65, 66]
        local: mutable darray[sview] = []
        local.push(buf.as_sview())
        required_effects: mutable darray[sview] = []
        required_effects <- pick(local)
        for required in required_effects |diags|:
            diags.push(Diag{expected: required})

def main() -> i32:
    return 0
`},
	// A loop reassignment from a local breaks the loop-entry assumption.
	{name: "CallAssignFromLocalInLoopEscapes", ok: false, src: `struct Diag:
    expected: sview

def pick(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview] = []
        out.push(names[0])
        return out

def check(names: darray[sview]&, diags: mutable darray[Diag]&) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: darray[u8] = [65, 66]
        local: mutable darray[sview] = []
        local.push(buf.as_sview())
        required_effects: mutable darray[sview] = pick(names)
        while i < 3 |i: usize = 0, required_effects, diags|:
            diags.push(Diag{expected: required_effects[0]})
            required_effects <- pick(local)
            i <- i + 1.usize()

def main() -> i32:
    return 0
`},
	// A ternary initializer takes the merge of its branches; an empty literal branch contributes nothing.
	{name: "TernaryInitKeepsBranchProvenance", ok: true, src: `struct Ann:
    region: sview

def mk(xs: darray[sview]&) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview] = []
        out.push(xs[0])
        return out

def record(names: darray[sview]&, anns: mutable darray[Ann]&, line: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        regions: darray[sview] = [] if line > 0 else mk(names)
        for r in regions:
            anns.push(Ann{region: r})

def main() -> i32:
    return 0
`},
	// A ternary branch built from a local keeps that local's provenance.
	{name: "TernaryInitFromLocalEscapes", ok: false, src: `struct Ann:
    region: sview

def mk(xs: darray[sview]&) -> darray[sview]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[sview] = []
        out.push(xs[0])
        return out

def record(anns: mutable darray[Ann]&, line: u32) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: darray[u8] = [65]
        loc: mutable darray[sview] = []
        loc.push(buf.as_sview())
        regions: darray[sview] = [] if line > 0 else mk(loc)
        for r in regions:
            anns.push(Ann{region: r})

def main() -> i32:
    return 0
`},
	// The pre-body scan predicts the UFCS rewrite recv.f(args) -> f(recv, args), so the filled container is judged at its real parameter index.
	{name: "UFCSScanJudgesTheRewrittenArgument", ok: true, src: `struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def parse_one(parser: lmut P, items: mutable darray[Prm]&) -> bool:
    can Memory.Allocate, Abort.Panic:
        items.push(Prm{name: parser.source, has_default: false})
        parser.position <- parser.position + 1
        return parser.position < 3

def parse_list(parser: lmut P, items: mutable darray[Prm]&) -> bool:
    can Memory.Allocate, Abort.Panic:
        while parser.position < 5 |parser, items|:
            rebind more: bool, parser = parser.parse_one(items)
            break if not more
        return true

def handler(parser: lmut P) -> void:
    can Memory.Allocate, Abort.Panic:
        captures: mutable darray[Prm] = []
        rebind ignored: bool, parser = parser.parse_list(captures)
        for capture in captures |parser|:
            parser.params <- parser.params.push(capture)

def main() -> i32:
    return 0
`},
	// A UFCS callee that fills the container from a view of a caller local escapes.
	{name: "UFCSScanFillFromOtherArgumentEscapes", ok: false, src: `struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def parse_named(parser: lmut P, items: mutable darray[Prm]&, name: sview) -> bool:
    can Memory.Allocate, Abort.Panic:
        items.push(Prm{name: name, has_default: false})
        parser.position <- parser.position + 1
        return parser.position < 3

def handler(parser: lmut P) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: darray[u8] = [65, 66]
        captures: mutable darray[Prm] = []
        rebind ignored: bool, parser = parser.parse_named(captures, buf.as_sview())
        for capture in captures |parser|:
            parser.params <- parser.params.push(capture)

def main() -> i32:
    return 0
`},
	// A callee that pushes a view of one argument container into another taints the filled container with the local.
	{name: "FillFromOtherContainerArgumentEscapes", ok: false, src: `struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def fill[@r](parser: lmut P, items: mutable darray[Prm]& @r, bytes: mutable darray[u8]& @r) -> bool:
    can Memory.Allocate, Abort.Panic:
        bytes.push(65)
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

def main() -> i32:
    return 0
`},
	// The fixed shape of the handler rewrite: both containers are allocated in the caller's region @r.
	{name: "FillInCallerRegionIsAllowed", ok: true, src: `struct Prm:
    name: sview
    has_default: bool

struct P:
    source: sview
    position: mutable usize
    params: mutable darray[Prm]

def fill[@r](parser: lmut P, items: mutable darray[Prm]& @r, bytes: mutable darray[u8]& @r) -> bool:
    can Memory.Allocate, Abort.Panic:
        bytes.push(65)
        items.push(Prm{name: bytes.as_sview(), has_default: false})
        parser.position <- parser.position + 1
        return true

def handler[@r](parser: mutable P& @r) -> void:
    can Memory.Allocate, Abort.Panic:
        buf: mutable darray[u8] @r = []
        captures: mutable darray[Prm] @r = []
        rebind ignored: bool, parser = parser.fill(captures, buf)
        for capture in captures |parser|:
            parser.params <- parser.params.push(capture)

def main() -> i32:
    return 0
`},
	// A view of a local routed through a helper returning a view is still the local's.
	{name: "ViewPathThroughHelperEscapes", ok: false, src: `struct D:
    name: sview
    expected: sview

struct T:
    diags: mutable darray[D]
    text: mutable darray[u8]

def path_text(names: darray[sview]&, table: lmut T) -> sview:
    can Memory.Allocate, Abort.Panic:
        return names[0]

def place_text(name: sview, table: lmut T) -> sview:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = []
        buf: darray[u8] = [65, 66]
        names.push(buf.as_sview())
        rebind joined: sview, table = path_text(names, table)
        return joined

def report(name: sview, table: lmut T) -> void:
    can Memory.Allocate, Abort.Panic:
        rebind owner: sview, table = place_text(name, table)
        table.diags <- table.diags.push(D{name: name, expected: owner}) if owner != ""

def main() -> i32:
    can Memory.Allocate, Abort.Panic:
        t: mutable T = T{diags: [], text: []}
        t <- report("a", t)
        return t.diags.count.i32()
`},
	// The same with an unrelated scalar argument.
	{name: "ViewPathThroughHelperWithScalarEscapes", ok: false, src: `struct D:
    name: sview
    expected: sview

struct T:
    diags: mutable darray[D]
    text: mutable darray[u8]

def path_text(names: darray[sview]&, n: usize, table: lmut T) -> sview:
    can Memory.Allocate, Abort.Panic:
        return names[0]

def place_text(name: sview, table: lmut T) -> sview:
    can Memory.Allocate, Abort.Panic:
        names: mutable darray[sview] = []
        buf: darray[u8] = [65, 66]
        names.push(buf.as_sview())
        rebind joined: sview, table = path_text(names, names.count, table)
        return joined

def report(name: sview, table: lmut T) -> void:
    can Memory.Allocate, Abort.Panic:
        rebind owner: sview, table = place_text(name, table)
        table.diags <- table.diags.push(D{name: name, expected: owner}) if owner != ""

def main() -> i32:
    can Memory.Allocate, Abort.Panic:
        t: mutable T = T{diags: [], text: []}
        t <- report("a", t)
        return t.diags.count.i32()
`},
	// A void walker pushing elements built from its argument into another argument is fine when the argument is caller-owned.
	{name: "ReturnElementSummaryThroughVoidWalker", ok: true, src: `enum Ex:
    Leaf(name: sview, kids: darray[i64])
    Empty

def walk(names: darray[sview]&, exprs: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    for name in names |exprs|:
        exprs.push(Ex.Leaf(name, []))

def outer(nesting: darray[sview]&, exprs: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    child: mutable darray[sview] = [entry for entry in nesting]
    child.push("label")
    walk(child, exprs)

def main() -> i32:
    return 0
`},
}

func localBorrowEscapeErrors(t *testing.T, name, src string) []string {
	t.Helper()
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, name+".elisa", src)
	return result.Errors()
}

func isLocalBorrowEscapeError(err string) bool {
	return strings.Contains(err, "longer-lived region") || strings.Contains(err, "dangling") || strings.Contains(err, "local region") ||
		strings.Contains(err, "on a later iteration")
}

func TestLocalBorrowEscapeFalsePositiveReductions(t *testing.T) {
	for _, tc := range localBorrowEscapeCases {
		t.Run(tc.name, func(t *testing.T) {
			errs := localBorrowEscapeErrors(t, tc.name, tc.src)
			if tc.ok {
				if len(errs) != 0 {
					t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
				}
				return
			}
			if len(errs) == 0 {
				t.Fatalf("must be rejected as a region escape")
			}
			for _, err := range errs {
				if !isLocalBorrowEscapeError(err) {
					t.Fatalf("negative must fail only on the escape, got: %s", err)
				}
			}
		})
	}
}

// These cases were previously known misses: a short-lived view was copied through an aggregate
// element into a longer-lived output. The call-site store checker now rejects both.
var localBorrowEscapeNewlyDetected = []localBorrowEscapeCase{
	// A local view pushed into a copied container before the walker fills the out-param.
	{name: "re1n", ok: false, src: `enum Ex:
    Leaf(name: sview, kids: darray[i64])
    Empty

def walk(names: darray[sview]&, exprs: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    for name in names |exprs|:
        exprs.push(Ex.Leaf(name, []))

def outer(nesting: darray[sview]&, exprs: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    child: mutable darray[sview] = [entry for entry in nesting]
    buf: darray[u8] = [65, 66]
    child.push(buf.as_sview())
    walk(child, exprs)

def main() -> i32:
    return 0
`},
	// The same without the comprehension copy.
	{name: "re2n", ok: false, src: `enum Ex:
    Leaf(name: sview)
    Empty

def walk(names: darray[sview]&, exprs: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    exprs.push(Ex.Leaf(names[0]))

def outer(exprs: mutable darray[Ex]&) -> void can[Memory.Allocate, Abort.Panic]:
    buf: darray[u8] = [65, 66]
    child: mutable darray[sview] = []
    child.push(buf.as_sview())
    walk(child, exprs)

def main() -> i32:
    return 0
`},
}

func TestLocalBorrowEscapeNewlyDetected(t *testing.T) {
	for _, tc := range localBorrowEscapeNewlyDetected {
		t.Run(tc.name, func(t *testing.T) {
			errs := localBorrowEscapeErrors(t, tc.name, tc.src)
			for _, err := range errs {
				if !isLocalBorrowEscapeError(err) {
					t.Fatalf("newly detected escape must fail only on a region error, got: %s", err)
				}
			}
			if len(errs) == 0 {
				t.Fatalf("previously missed short-lived view escape must now be rejected")
			}
		})
	}
}
