//go:build cgo

package backend

import (
	"strings"
	"testing"

	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

func parseAndAnalyzeBackendTest(t *testing.T, filename string, src string) *semantic.Result {
	t.Helper()
	l := lexer.New(filename, []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile(filename)
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.Analyze(file)
	if errs := result.Errors(); len(errs) > 0 {
		t.Fatalf("semantic errors:\n%s", strings.Join(errs, "\n"))
	}
	return result
}

func generateLLVMIRWithPackedABIForTest(result *semantic.Result, abi packedEnumABIMode) (string, error) {
	g, err := newLLVMGenerator(result)
	if err != nil {
		return "", err
	}
	defer g.dispose()
	g.packedEnumABI = abi
	if err := g.emitModule(); err != nil {
		return "", err
	}
	if err := g.verify(); err != nil {
		return "", err
	}
	return g.printModule(), nil
}

func TestGenerateLLVMIRLowersPackedEnumsAsWordHandlesInAlternateABI(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def differs(left: Expr, right: Expr) -> bool:
	return left != right

def fold() -> int:
	region scratch(256u)
	store: Expr.Store = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		return match node:
			Expr.Lit(value):
				value + node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_word_handle.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	checks := []string{
		"%Expr__Store = type { ptr, i64, ptr }",
		"define i1 @differs(i64",
		"icmp ne i64",
		"call ptr @ctx_packed_store_state_new(ptr",
		"call i64 @ctx_packed_store_alloc(ptr %packed.alloc.store.arena, i64 %packed.alloc.store.row_bytes, ptr %packed.alloc.store.state)",
		"call ptr @ctx_packed_store_decode(ptr %packed.decode.store.arena, i64",
		"extractvalue %Expr__Store",
		"packed.decode.store.arena",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"define i1 @differs(ptr", "icmp ne ptr", "call i64 @ctx_packed_store_encode(", "ptrtoint ptr %packed.alloc to i64", "inttoptr i64"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected alternate packed ABI to lower values as integer handles and avoid %q, got:\n%s", bad, output)
		}
	}
}
