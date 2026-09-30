//go:build cgo

package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

// fillMayAdopt links a function to every function its body NAMES. A binding that shares a
// function's name is not that function: counting the mention made walk_affine_statements (its
// `for statement in statements` binder) reach the parser's `statement` and, through it, a void
// grower, so every fill it did was distrusted. Each case names the probed function `w` and seeds
// `grow` as the only void grower; `want` is whether `w` may adopt a caller's arena.

const fillMayAdoptScopePrelude = `enum E:
    A(v: i64)
    B

def grow(out: mutable darray[i64]&) -> void:
    out.push(1 + 1)

`

func fillMayAdoptForTest(t *testing.T, src string) bool {
	t.Helper()
	l := lexer.New("fill_may_adopt_scope.elisa", []byte(fillMayAdoptPreludeFor()+src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("fill_may_adopt_scope.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	a := &Analyzer{file: file, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}}
	var probed *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		a.funcDeclSymbols[fn] = &Symbol{Name: fn.Name, Kind: SymbolFunc}
		switch fn.Name {
		case "grow":
			fn.AmbientGrownContainerRegion = "__rg_out"
		case "w":
			probed = fn
		}
	}
	if probed == nil {
		t.Fatalf("no function `w` in: %s", src)
	}
	return a.fillMayAdopt(probed)
}

func TestFillMayAdoptBindingsShadowFunctionNames(t *testing.T) {
	cases := map[string]string{
		"param": `def w(grow: i64) -> i64:
    return grow
`,
		"iter_for_binder": `def w(xs: darray[i64]) -> i64:
    total: mutable i64 = 0
    for grow in xs |total|:
        total <- total + grow
    return total
`,
		"numeric_for_binder": `def w() -> i64:
    total: mutable i64 = 0
    for grow in 0..<4 |total|:
        total <- total + grow
    return total
`,
		"local_after_declaration": `def w() -> i64:
    grow: i64 = 1
    return grow
`,
		"match_arm_binder": `def w(e: E) -> i64:
    match e:
        E.A(grow):
            return grow
        E.B:
            return 0
`,
		"match_expr_arm_binder": `def w(e: E) -> i64:
    return match e:
        E.A(grow): grow
        E.B: 0
`,
		"fstring": `def w(n: i64) -> void:
    can Memory.Allocate:
        s: dstr = f"x{n}"
`,
	}
	for name, body := range cases {
		if fillMayAdoptForTest(t, body) {
			t.Fatalf("%s: a binding named like a void grower must not make `w` adopt", name)
		}
	}
}

func TestFillMayAdoptKeepsRealFunctionMentions(t *testing.T) {
	cases := map[string]string{
		"direct_call": `def w(out: mutable darray[i64]&) -> void:
    grow(out)
`,
		// The mention precedes the local, so it still names the function.
		"mention_before_local": `def w(out: mutable darray[i64]&) -> void:
    grow(out)
    grow: i64 = 1
`,
		// The local's block ended; the later mention names the function again.
		"mention_after_local_scope": `def w(b: bool, out: mutable darray[i64]&) -> void:
    if b:
        grow: i64 = 1
    grow(out)
`,
		// The binder's scope is the loop; the source expression is outside it.
		"loop_source_is_outside_binder": `def w(out: mutable darray[i64]&) -> void:
    for grow in [grow] |out|:
        grow(out)
`,
		"transitive": `def mid(out: mutable darray[i64]&) -> void:
    grow(out)

def w(out: mutable darray[i64]&) -> void:
    mid(out)
`,
	}
	for name, body := range cases {
		if !fillMayAdoptForTest(t, body) {
			t.Fatalf("%s: a real mention of a void grower must make `w` adopt", name)
		}
	}
}

// A function-valued binding called by a grower's name is an unknown callee, not that grower's
// summary: it may be any function.
func TestFillMayAdoptBindingCalleeIsUnknown(t *testing.T) {
	src := `def other(out: mutable darray[i64]&) -> void:
    pass

def w(fs: darray[fn(mutable darray[i64]&) -> void], out: mutable darray[i64]&) -> void:
    for grow in fs |out|:
        grow(out)
`
	if !fillMayAdoptForTest(t, src) {
		t.Fatalf("calling a loop binder must be treated as an unknown callee")
	}
}

// A container declared inside a loop is fresh on each iteration, so pushes into it do not carry
// element provenance around that loop, only around loops nested inside the declaration.
func TestLoopDeclaredContainerElements(t *testing.T) {
	allowed := `enum Ex:
    Name(text: sview)
    Pair(left: sview, right: sview)

enum St:
    Expr(expression: Ex, line: u32)
    Nop(line: u32)

def collect(expression: Ex, out: mutable darray[sview]&, depth: i32) -> void:
    can Memory.Allocate, Abort.Panic:
        if depth > 0:
            collect(expression, out, depth - 1)
            return
        match expression:
            Ex.Name(text):
                out.push(text)
            Ex.Pair(left, right):
                out.push(left)
                out.push(right)

def walk(statements: darray[St], consumed: mutable darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for statement in statements |consumed|:
            match statement:
                St.Expr(expression, line):
                    moves: mutable darray[sview] = []
                    collect(expression, moves, 2)
                    for m in moves |consumed|:
                        consumed.push(m)
                St.Nop(line):
                    pass
`
	result := analyzeFunctionAnalysisTestSource(t, "loop_declared_container_ok.elisa", allowed)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("a per-iteration container filled from parameters must be allowed, got: %s", strings.Join(errs, "\n"))
	}

	// The container outlives the INNER loop: a view pushed in one inner iteration is read in the
	// next, after the f-string it points into was freed.
	rejected := `def g(dst: mutable darray[sview]&) -> void:
    can Memory.Allocate:
        for i in 0..<2 |dst|:
            xs: mutable darray[sview] = []
            for j in 0..<2 |xs, dst|:
                if j == 1:
                    dst.push(xs[0])
                s: dstr = f"A{"b"}"
                xs.push(s.as_sview())
`
	bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "loop_declared_container_bad.elisa", rejected)
	if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "is stored into longer-lived region") && !strings.Contains(all, "on a later iteration") {
		t.Fatalf("a view carried around the inner loop must be rejected, got: %s", all)
	}
}

// A function returning `g(args)` inherits g's return-element summary instantiated against its
// arguments, so forwarding the caller's own elements stays accepted while every path that hands
// back views of a frame-local string is rejected at the caller's store.
func TestReturnElementSummaryThroughCalls(t *testing.T) {
	const copyInner = `def inner(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate:
        roots: mutable darray[sview] = []
        for n in names |roots|:
            roots.push(n)
        return roots

`
	const caller = `
def g(names: darray[sview]&, dst: mutable darray[sview]&) -> void:
    can Memory.Allocate:
        rs: darray[sview] = outer(names)
        for r in rs:
            dst.push(r)
`
	allowed := copyInner + `def outer(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate:
        return inner(names)
` + caller
	result := analyzeFunctionAnalysisTestSource(t, "return_element_call_ok.elisa", allowed)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("forwarding the caller's elements through a call must be allowed, got: %s", strings.Join(errs, "\n"))
	}

	rejected := map[string]string{
		// The callee has no summary: its elements view its own local string.
		"callee_returns_local_views": `def inner(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate:
        roots: mutable darray[sview] = []
        s: dstr = f"x{names[0]}"
        roots.push(s.as_sview())
        return roots

def outer(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate:
        return inner(names)
` + caller,
		// The callee's summary is its parameter; the argument is a local container of local views.
		"local_argument_instantiated": copyInner + `def outer(names: darray[sview]&) -> darray[sview]:
    can Memory.Allocate:
        local: mutable darray[sview] = []
        s: dstr = f"x{names[0]}"
        local.push(s.as_sview())
        return inner(local)
` + caller,
	}
	for name, src := range rejected {
		bad := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "return_element_call_"+name+".elisa", src)
		if all := strings.Join(bad.Errors(), "\n"); !strings.Contains(all, "is stored into longer-lived region") {
			t.Fatalf("%s: views of a frame-local string reaching the caller's store must be rejected, got: %s", name, all)
		}
	}
}

// A function's own recursive call, a builtin method that only reads its argument, and another
// `darray[sview]` argument (whose elements, not its storage, can reach a `darray[sview]`) do not
// make a function a source of fresh caller-arena data, so the fills built from them stay tracked.
func TestFillFreshnessIgnoresSelfBuiltinAndSviewDarrayArgs(t *testing.T) {
	cases := map[string]string{
		"self_call": `def collect(names: darray[sview]&, out: mutable darray[sview]&, depth: i32) -> void:
    can Memory.Allocate:
        if depth > 0:
            collect(names, out, depth - 1)
            return
        for n in names |out|:
            out.push(n)

def walk(names: darray[sview]&, consumed: mutable darray[sview]&) -> void:
    can Memory.Allocate:
        moves: mutable darray[sview] = []
        collect(names, moves, 2)
        for m in moves |consumed|:
            consumed.push(m)
`,
		"builtin_method_argument": `def step(names: darray[sview]&, consumed: mutable darray[sview]&, table: mutable darray[sview]&) -> void:
    can Memory.Allocate:
        repeated: mutable darray[sview] = []
        repeated.extend(consumed)
        for r in repeated |table|:
            table.push(r)
`,
		"sview_darray_argument": `def fill(names: darray[sview]&, seen: darray[sview]&, out: mutable darray[sview]&) -> void:
    can Memory.Allocate:
        for n in names |out|:
            out.push(n)

def walk(names: darray[sview]&, dst: mutable darray[sview]&) -> void:
    can Memory.Allocate:
        seen: mutable darray[sview] = []
        local: mutable darray[sview] = []
        fill(names, seen, local)
        for l in local |dst|:
            dst.push(l)
`,
	}
	for name, src := range cases {
		result := analyzeFunctionAnalysisTestSource(t, "fill_freshness_"+name+".elisa", src)
		if errs := result.Errors(); len(errs) != 0 {
			t.Fatalf("%s: must be accepted, got: %s", name, strings.Join(errs, "\n"))
		}
	}
}

// A void grower that only moves values it was handed (no constructors, no concatenation, no
// unknown callee) cannot put caller-arena data into its container, so it must not seed the
// closure; one that builds fresh data still does.
func TestFillMayAdoptPureGrowerDoesNotSeed(t *testing.T) {
	pure := strings.Replace(fillMayAdoptScopePrelude, "out.push(1 + 1)", "out.push(1)", 1)
	if fillMayAdoptWithPrelude(t, pure, "def w(out: mutable darray[i64]&) -> void:\n    grow(out)\n") {
		t.Fatalf("a grower that only pushes a scalar must not make its caller adopt")
	}
	if !fillMayAdoptWithPrelude(t, fillMayAdoptScopePrelude, "def w(out: mutable darray[i64]&) -> void:\n    grow(out)\n") {
		t.Fatalf("a grower that builds fresh data must make its caller adopt")
	}
}

func fillMayAdoptWithPrelude(t *testing.T, prelude, src string) bool {
	t.Helper()
	saved := fillMayAdoptScopePreludeOverride
	fillMayAdoptScopePreludeOverride = prelude
	defer func() { fillMayAdoptScopePreludeOverride = saved }()
	return fillMayAdoptForTest(t, src)
}

var fillMayAdoptScopePreludeOverride string

func fillMayAdoptPreludeFor() string {
	if fillMayAdoptScopePreludeOverride != "" {
		return fillMayAdoptScopePreludeOverride
	}
	return fillMayAdoptScopePrelude
}
