package parser

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// docs/122 §5 pattern features: arm-header guards, range patterns, bound list rest,
// as-bindings, struct/variant rest `_`. Parser-level shape tests.

func firstMatchStmt(t *testing.T, file *ast.File) *ast.MatchStmt {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		for _, stmt := range fn.Body {
			if m, ok := stmt.(*ast.MatchStmt); ok {
				return m
			}
		}
	}
	t.Fatalf("no match statement found")
	return nil
}

const docs122EnumHeader = `enum Tok:
    Ident(name: cstr)
    Keyword(name: cstr)
    Num(value: i64, width: i64)

`

func TestParseMatchArmGuard(t *testing.T) {
	src := docs122EnumHeader + `def f(t: Tok) -> i64:
    match t:
        Tok.Num(value: v, width: w) if v > 10:
            return v
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	if m.Arms[0].Guard == nil {
		t.Fatalf("expected guard on first arm")
	}
	if m.Arms[1].Guard != nil {
		t.Fatalf("wildcard arm must have no guard")
	}
}

func TestParseMatchArmGuardSharedAcrossAlternatives(t *testing.T) {
	src := docs122EnumHeader + `def f(t: Tok) -> i64:
    match t:
        Tok.Ident(name: n) | Tok.Keyword(name: n) if n == "x":
            return 1
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	if len(m.Arms) != 3 {
		t.Fatalf("expected 3 arms (2 fanned + wildcard), got %d", len(m.Arms))
	}
	if m.Arms[0].Guard == nil || m.Arms[1].Guard == nil {
		t.Fatalf("fanned alternatives must share the guard")
	}
}

func TestParseMatchRangePattern(t *testing.T) {
	src := `def f(c: char) -> i64:
    match c:
        'a'..<'z':
            return 1
        '0'..='9':
            return 2
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	lo, ok := m.Arms[0].Pattern.(*ast.MatchRangePattern)
	if !ok {
		t.Fatalf("expected range pattern, got %T", m.Arms[0].Pattern)
	}
	if lo.Inclusive {
		t.Fatalf("..< must be exclusive")
	}
	hi, ok := m.Arms[1].Pattern.(*ast.MatchRangePattern)
	if !ok {
		t.Fatalf("expected range pattern, got %T", m.Arms[1].Pattern)
	}
	if !hi.Inclusive {
		t.Fatalf("..= must be inclusive")
	}
}

func TestParseMatchRangeBareDotDotDiagnosed(t *testing.T) {
	src := `def f(c: char) -> i64:
    match c:
        'a'..'z':
            return 1
        _:
            return 0
    return 0
`
	_, errs := parseSourceFile(t, src)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "..<") {
		t.Fatalf("expected bare `..` range diagnostic steering to ..</..=, got %v", errs)
	}
}

func TestParseListRestBinding(t *testing.T) {
	src := `def f(xs: darray[i64]) -> i64:
    match xs:
        [first, ...rest]:
            return first
        [only, ...]:
            return only
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	list := m.Arms[0].Pattern.(*ast.MatchListPattern)
	rest, ok := list.Elems[len(list.Elems)-1].(*ast.MatchRestPattern)
	if !ok || rest.Name != "rest" {
		t.Fatalf("expected bound rest 'rest', got %T %+v", list.Elems[len(list.Elems)-1], rest)
	}
	list2 := m.Arms[1].Pattern.(*ast.MatchListPattern)
	rest2 := list2.Elems[len(list2.Elems)-1].(*ast.MatchRestPattern)
	if rest2.Name != "" {
		t.Fatalf("bare ... must stay unnamed, got %q", rest2.Name)
	}
}

func TestParseVariantRestMarker(t *testing.T) {
	src := docs122EnumHeader + `def f(t: Tok) -> i64:
    match t:
        Tok.Num(value: v, _):
            return v
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	v := m.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if !v.Rest {
		t.Fatalf("expected Rest=true on named-args variant pattern with trailing _")
	}
	if len(v.Args) != 1 || v.Args[0].Name != "value" {
		t.Fatalf("rest marker must be detached from args, got %+v", v.Args)
	}
}

func TestParsePositionalWildcardStaysPerField(t *testing.T) {
	src := docs122EnumHeader + `def f(t: Tok) -> i64:
    match t:
        Tok.Num(v, _):
            return v
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	v := m.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if v.Rest {
		t.Fatalf("pure-positional trailing _ must stay a one-field wildcard, not rest")
	}
	if len(v.Args) != 2 {
		t.Fatalf("expected 2 positional args, got %d", len(v.Args))
	}
}

func TestParseStructBraceRestMarker(t *testing.T) {
	src := `struct Ev:
    key: i64
    x: i64
    y: i64

def f(e: Ev) -> i64:
    match e:
        Ev{key: k, _}:
            return k
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	s := m.Arms[0].Pattern.(*ast.MatchStructPattern)
	if !s.Rest {
		t.Fatalf("expected Rest=true on brace struct pattern with trailing _")
	}
	if len(s.Args) != 1 || s.Args[0].Name != "key" {
		t.Fatalf("rest marker must be detached from args, got %+v", s.Args)
	}
}

func TestParseVariantAsBinding(t *testing.T) {
	src := docs122EnumHeader + `def f(t: Tok) -> i64:
    match t:
        Tok.Num(value: v, width: w) as whole:
            return v
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	v := m.Arms[0].Pattern.(*ast.MatchVariantPattern)
	if v.As != "whole" {
		t.Fatalf("expected as-binding 'whole', got %q", v.As)
	}
}

func TestParseNestedAsBinding(t *testing.T) {
	src := `enum Expr:
    Lit(value: i64)
    Add(left: Expr, right: Expr)

def f(e: Expr) -> i64:
    match e:
        Expr.Add(left: Expr.Lit(value: v) as lhs, right: r):
            return v
        _:
            return 0
    return 0
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	m := firstMatchStmt(t, file)
	outer := m.Arms[0].Pattern.(*ast.MatchVariantPattern)
	inner := outer.Args[0].Pattern.(*ast.MatchVariantPattern)
	if inner.As != "lhs" {
		t.Fatalf("expected nested as-binding 'lhs', got %q", inner.As)
	}
}

func TestParseMatchExprArmGuard(t *testing.T) {
	src := `def f(n: i64) -> i64:
    r: i64 = match n:
        0: 100
        v if v > 10: 200
        _: 0
    return r
`
	file, errs := parseSourceFile(t, src)
	if len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	decl := fn.Body[0].(*ast.VarDeclStmt)
	me := decl.Value.(*ast.MatchExpr)
	if me.Arms[1].Guard == nil {
		t.Fatalf("expected guard on bind arm of match expression")
	}
}
