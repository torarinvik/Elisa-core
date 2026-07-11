//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"strings"
	"testing"
)

func parseFoldTestFile(t *testing.T, src string) *ast.File {
	t.Helper()
	l := lexer.New("fold.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("fold.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return file
}

// classifyBodyIsTableLookup reports whether a folded function body is exactly
// `return <ident>[<ident>]` — the rewritten lookup shape.
func classifyBodyIsTableLookup(fn *ast.FuncDecl) bool {
	if len(fn.Body) != 1 {
		return false
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		return false
	}
	idx, ok := ret.Value.(*ast.IndexExpr)
	if !ok {
		return false
	}
	_, objIsIdent := idx.Object.(*ast.Ident)
	_, idxIsIdent := idx.Index.(*ast.Ident)
	return objIsIdent && idxIsIdent
}

const foldClassifierSrc = `
const enum NumberClass of u8:
    Digit
    ExpMark
    HexAlpha
    Dot
    Sign
    Other

def classify(c: char) -> NumberClass:
    return when c:
        '0'..='9' -> NumberClass.Digit
        'e' | 'E' -> NumberClass.ExpMark
        'a'..='d' | 'f' | 'A'..='D' | 'F' -> NumberClass.HexAlpha
        '.' -> NumberClass.Dot
        '+' | '-' -> NumberClass.Sign
        _ -> NumberClass.Other
`

func TestFoldClassifierRecognizedAndRewritten(t *testing.T) {
	file := parseFoldTestFile(t, foldClassifierSrc)
	folded := foldCharClassifierTables(file)
	if len(folded) != 1 || folded[0] != "classify" {
		t.Fatalf("expected [classify] folded, got %v", folded)
	}
	// The body must now be a table lookup, and a 256-entry const table must exist.
	var fn *ast.FuncDecl
	var table *ast.ConstDecl
	for _, d := range file.Decls {
		switch n := d.(type) {
		case *ast.FuncDecl:
			if n.Name == "classify" {
				fn = n
			}
		case *ast.ConstDecl:
			if n.Name == "__classtable_classify" {
				table = n
			}
		}
	}
	if fn == nil || !classifyBodyIsTableLookup(fn) {
		t.Fatalf("classify body was not rewritten to a table lookup: %#v", fn)
	}
	if table == nil {
		t.Fatal("no __classtable_classify const decl synthesized")
	}
	lit, ok := table.Value.(*ast.ListLitExpr)
	if !ok || len(lit.Elems) != 256 {
		t.Fatalf("table must be a 256-element list literal, got %#v", table.Value)
	}
	// Spot-check the truth table: '0' (48) -> Digit, '.' (46) -> Dot, 'x' (120) -> Other.
	expect := map[int]string{'0': "Digit", '9': "Digit", '.': "Dot", '+': "Sign", 'e': "ExpMark", 'f': "HexAlpha", 'x': "Other", 0: "Other"}
	for idx, want := range expect {
		fe, ok := lit.Elems[idx].(*ast.FieldExpr)
		if !ok || fe.Field != want {
			t.Errorf("table[%d] = %#v, want member %q", idx, lit.Elems[idx], want)
		}
	}
}

// A guard makes an arm non-statically-evaluable → the classifier must NOT fold.
func TestFoldClassifierBailsOnGuard(t *testing.T) {
	src := `
const enum Cls of u8:
    A
    B

def classify(c: char) -> Cls:
    return match c:
        '0'..='9' if true:
            Cls.A
        _:
            Cls.B
`
	file := parseFoldTestFile(t, src)
	if folded := foldCharClassifierTables(file); len(folded) != 0 {
		t.Fatalf("guarded classifier must not fold, got %v", folded)
	}
}

// A non-char parameter is not a char classifier → no fold.
func TestFoldClassifierBailsOnNonChar(t *testing.T) {
	src := `
const enum Cls of u8:
    A
    B

def classify(c: i64) -> Cls:
    return when c:
        0 -> Cls.A
        _ -> Cls.B
`
	file := parseFoldTestFile(t, src)
	if folded := foldCharClassifierTables(file); len(folded) != 0 {
		t.Fatalf("non-char classifier must not fold, got %v", folded)
	}
}

// The -Wperf foldability lint: a classifier-shaped function (char param, const-enum return)
// that dispatches by a branch chain instead of folding draws a warning by default and a hard
// error under -Wperf. Here a guard blocks the fold.
func TestUnfoldableClassifierPerfLint(t *testing.T) {
	src := `
const enum Cls of u8:
    A
    B

def classify(c: char) -> Cls:
    return match c:
        '0'..='9' if true:
            Cls.A
        _:
            Cls.B
`
	warn := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unfoldable.elisa", src, AnalyzeOptions{})
	if !strings.Contains(allDiagnostics(warn), "not foldable to a lookup table") {
		t.Fatalf("expected foldability warning, got:\n%s", allDiagnostics(warn))
	}
	if len(warn.Errors()) != 0 {
		t.Fatalf("default foldability friction should be a warning, got errors:\n%v", warn.Errors())
	}
	strict := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "unfoldable_strict.elisa", src, AnalyzeOptions{EnforcePerfLints: true})
	if len(strict.Errors()) == 0 {
		t.Fatal("foldability friction should be an error under -Wperf")
	}
}

// A FOLDABLE classifier draws NO lint — it was rewritten to a table lookup before analysis.
func TestFoldableClassifierNoPerfLint(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "foldable_clean.elisa", foldClassifierSrc, AnalyzeOptions{EnforcePerfLints: true})
	if strings.Contains(allDiagnostics(result), "foldable") {
		t.Fatalf("a folded classifier must draw no foldability lint, got:\n%s", allDiagnostics(result))
	}
	if len(result.Errors()) != 0 {
		t.Fatalf("folded classifier should be clean under -Wperf, got:\n%v", result.Errors())
	}
}

// A classifier declared inside an `extend`/`module` block (a NamespaceDecl) still folds —
// this is the pilot's real shape (classify_number_char lives in `extend Lexer:`).
func TestFoldClassifierInsideNamespace(t *testing.T) {
	src := `
module M:
    const enum Cls of u8:
        A
        B

    def classify(c: char) -> Cls:
        return when c:
            'a' -> Cls.A
            _ -> Cls.B
`
	file := parseFoldTestFile(t, src)
	if folded := foldCharClassifierTables(file); len(folded) != 1 {
		t.Fatalf("namespaced classifier must fold, got %v", folded)
	}
}
