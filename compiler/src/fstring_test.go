//go:build cgo

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

// f-string interpolation (Stage A): `f"a{x}b"` lexes to one TOKEN_FSTRING_LIT, the parser desugars
// it to a `__fstr("a", x, "b")` builtin call, the analyzer types it dstr (string-like parts only,
// Memory.Allocate + Abort.Panic effects), and the backend lowers it to one presized ctx_fstr_alloc
// plus one ctx_fstr_append per part.

func parseFStringSource(t *testing.T, src string) *ast.File {
	t.Helper()
	l := lexer.New("fstr_test.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("fstr_test.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	return file
}

// The desugar produces __fstr with interleaved literal chunks and embedded expressions, decoding
// escapes and `{{`/`}}` in chunks; a no-interpolation f-string collapses to a plain string literal.
func TestFStringParseDesugar(t *testing.T) {
	file := parseFStringSource(t, `
def f(name: sview) -> dstr:
    return f"a{name}b{{c}}"
`)
	fn := file.Decls[0].(*ast.FuncDecl)
	ret := fn.Body[len(fn.Body)-1].(*ast.ReturnStmt)
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("f-string must desugar to a call, got %T", ret.Value)
	}
	if ident, ok := call.Func.(*ast.Ident); !ok || ident.Name != "__fstr" {
		t.Fatalf("desugar target must be __fstr, got %v", call.Func)
	}
	if len(call.Args) != 3 {
		t.Fatalf("expected 3 parts (chunk, expr, chunk), got %d", len(call.Args))
	}
	if lit := call.Args[0].(*ast.StringLit); lit.Value != "a" {
		t.Fatalf("first chunk must be %q, got %q", "a", lit.Value)
	}
	if id := call.Args[1].(*ast.Ident); id.Name != "name" {
		t.Fatalf("embedded expr must be `name`, got %v", call.Args[1])
	}
	if lit := call.Args[2].(*ast.StringLit); lit.Value != "b{c}" {
		t.Fatalf("last chunk must decode {{}} to %q, got %q", "b{c}", lit.Value)
	}

	// docs/119 gap #3: EVERY f-string lowers through __fstr, so `f"…"` has one type (the
	// owned formatted string) whether or not it interpolates — no more static-literal
	// shortcut for the no-interpolation case (which typed as `static u8&` and broke
	// match/if-expression arm unification against interpolated arms).
	file2 := parseFStringSource(t, `
def g() -> dstr:
    return f"plain"
`)
	fn2 := file2.Decls[0].(*ast.FuncDecl)
	ret2 := fn2.Body[len(fn2.Body)-1].(*ast.ReturnStmt)
	call2, ok := ret2.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("no-interpolation f-string must still lower to a __fstr call, got %T", ret2.Value)
	}
	if id, ok := call2.Func.(*ast.Ident); !ok || id.Name != "__fstr" {
		t.Fatalf("no-interpolation f-string must call __fstr, got %v", call2.Func)
	}
	if len(call2.Args) != 1 {
		t.Fatalf("no-interpolation f-string must have one literal part, got %d", len(call2.Args))
	}
	if lit, ok := call2.Args[0].(*ast.StringLit); !ok || lit.Value != "plain" {
		t.Fatalf("the single part must be the literal %q, got %v", "plain", call2.Args[0])
	}
}

// The analyzer types __fstr as dstr and rejects a non-string-like part with a clear diagnostic.
func TestFStringSemanticTypesAndRejection(t *testing.T) {
	okFile := parseFStringSource(t, `
def f(name: sview) -> dstr:
    can Memory.Allocate, Abort.Panic:
        return f"hello {name}"
`)
	result := semantic.Analyze(okFile)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("string-like f-string must analyze cleanly, got: %v", errs)
	}

	badFile := parseFStringSource(t, `
def g(n: i64) -> dstr:
    can Memory.Allocate, Abort.Panic:
        return f"n is {n}"
`)
	badResult := semantic.Analyze(badFile)
	joined := strings.Join(badResult.Errors(), "\n")
	if !strings.Contains(joined, "string-like") {
		t.Fatalf("an i64 interpolation must be rejected with the string-like diagnostic, got: %v", badResult.Errors())
	}
}

// End-to-end: compile and RUN f-string programs natively; assert byte-exact content, including a
// nested f-string produced by a callee and literal-brace escapes.
func TestRunCLIFStringNativeRuntime(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	repoRoot := repoRootFromMainTest(t)
	fixtureDir := t.TempDir()
	fixturePath := filepath.Join(fixtureDir, "fstring_native.elisa")
	runtimePath := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa")
	runtimeInclude, err := filepath.Rel(fixtureDir, runtimePath)
	if err != nil {
		t.Fatalf("failed to compute runtime include path: %v", err)
	}
	runtimeInclude = filepath.ToSlash(runtimeInclude)
	src := fmt.Sprintf(`# include %q

def build(name: sview, val: sview) -> dstr:
    can Memory.Allocate, Abort.Panic:
        return f"[{name}={val}]"

@test
def fstring_bytes_exact() -> void:
    can Memory.Allocate, Abort.Panic:
        b: dstr = build("ab", "42")
        assert b.count == 7
        assert b[0] == 91
        assert b[1] == 97
        assert b[2] == 98
        assert b[3] == 61
        assert b[4] == 52
        assert b[5] == 50
        assert b[6] == 93

@test
def fstring_braces_empty_nested() -> void:
    can Memory.Allocate, Abort.Panic:
        e: dstr = f"{{x}}"
        assert e.count == 3
        assert e[0] == 123
        assert e[2] == 125
        d: dstr = f"a{build("q", "z")}b"
        assert d.count == 7
        assert d[1] == 91
`, runtimeInclude)
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	var stdout, stderr bytes.Buffer
	exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("f-string native test run failed (exit %d):\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "passed=2") {
		t.Fatalf("expected both f-string tests to pass, got:\n%s", stdout.String())
	}
}
