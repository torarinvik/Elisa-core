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
		"declare ptr @ctx_packed_store_state_new(ptr, i64)",
		"call ptr @ctx_packed_store_state_new(ptr",
		"call i64 @ctx_packed_store_alloc(ptr %packed.alloc.store.arena, i64 %packed.alloc.store.row_bytes, ptr %packed.alloc.store.state)",
		"call ptr @ctx_packed_store_decode(ptr %packed.decode.store.arena, i64",
		"ptr %packed.decode.store.state)",
		"extractvalue %Expr__Store",
		"packed.decode.store.arena",
		"packed.decode.store.state",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected constructor lowering plus one reused packed-match decode, got %d decode calls:\n%s", decodeCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected mixed packed match with matched-value field access to reuse a full decode instead of tag-read helper, got:\n%s", output)
	}
	for _, bad := range []string{"define i1 @differs(ptr", "icmp ne ptr", "call i64 @ctx_packed_store_encode(", "call ptr @arena_alloc(", "ptrtoint ptr %packed.alloc to i64", "inttoptr i64"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected alternate packed ABI to lower values as integer handles and avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRUsesTagReadHelperForMixedPackedMatchWithoutMatchedValueBodyAccess(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	End

def fold() -> int:
	region scratch(256u)
	store: Expr.Store = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(value: 5)
		return match node:
			Expr.Lit(value):
				value
			Expr.End:
				0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tag_read_mixed.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected mixed packed match without matched-value field access to use ctx_packed_store_read_tag, got:\n%s", output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected constructor lowering plus one matched payload decode, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateLLVMIRUsesWordReadHelperForRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		return node.span + node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated packed common-field reads to lower through ctx_packed_store_read_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected only constructor-time full decode for repeated packed common-field reads, got %d decode calls:\n%s", decodeCalls, output)
	}
	for _, check := range []string{"packed.common.store.arena", "packed.common.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRUsesTagReadHelperForPayloadlessPackedMatch(t *testing.T) {
	src := `packed enum Flag:
	Yes
	No

def choose() -> int:
	region scratch(256u)
	store: Flag.Store = Flag.Store(scratch)
	in store:
		node: Flag = new Flag.Yes
		return match node:
			Flag.Yes:
				1
			Flag.No:
				0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tag_read.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected payloadless packed match to read tag through ctx_packed_store_read_tag, got:\n%s", output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected only constructor-time full decode for payloadless packed match, got %d decode calls:\n%s", decodeCalls, output)
	}
}
