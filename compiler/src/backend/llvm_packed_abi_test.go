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
	store: Expr.Store[Local] = Expr.Store(scratch)
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
		"%PackedStoreAllocResult = type { ptr, i64 }",
		"define i1 @differs(i64",
		"icmp ne i64",
		"declare ptr @ctx_packed_store_state_new(ptr, i64)",
		"call ptr @ctx_packed_store_state_new(ptr",
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result(ptr %packed.alloc.store.arena, ptr %packed.alloc.store.state)",
		"call ptr @ctx_packed_store_decode(ptr %packed.decode.store.arena, i64",
		"ptr %packed.decode.store.state)",
		"extractvalue %Expr__Store",
		"extractvalue %PackedStoreAllocResult",
		"packed.decode.store.arena",
		"packed.decode.store.state",
		"store %Expr { i32 0, i64 7, [1 x i64] zeroinitializer }, ptr %packed.alloc.ptr",
		"store i64 5, ptr %enum.payload.ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected only one reused packed-match decode after constructor alloc returns a writable row directly, got %d decode calls:\n%s", decodeCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected mixed packed match with matched-value field access to reuse a full decode instead of tag-read helper, got:\n%s", output)
	}
	for _, bad := range []string{"define i1 @differs(ptr", "icmp ne ptr", "call i64 @ctx_packed_store_alloc(", "call ptr @arena_alloc(", "ptrtoint ptr %packed.alloc to i64", "inttoptr i64", "call %PackedStoreAllocResult @ctx_packed_store_alloc_result("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected alternate packed ABI to lower values as integer handles and avoid %q, got:\n%s", bad, output)
		}
	}
	for _, bad := range []string{"packed.enum.tag.ptr", "packed.enum.common.ptr"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed constructor lowering to aggregate-store the row prefix instead of using %q field stores, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRUsesTagReadHelperForMixedPackedMatchWithoutMatchedValueBodyAccess(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	End

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
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
	if !strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected one-word packed payload match to read payload through ctx_packed_store_read_word, got:\n%s", output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode when constructor allocation and one-word packed payload reads both stay on fast paths, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateLLVMIRUsesWordReadHelperForMultiFieldPackedPayloadMatch(t *testing.T) {
	src := `packed enum Pair:
	Both(left: int, right: int)
	End

def sum_pair() -> int:
	region scratch(256u)
	store: Pair.Store[Local] = Pair.Store(scratch)
	in store:
		node: Pair = new Pair.Both(left: 2, right: 3)
		return match node:
			Pair.Both(left: left, right: right):
				left + right
			Pair.End:
				0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_payload_words.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected mixed packed payload match to use ctx_packed_store_read_tag for dispatch, got:\n%s", output)
	}
	readWordCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readWordCalls != 2 {
		t.Fatalf("expected two direct payload word reads for Pair.Both payload, got %d helper calls:\n%s", readWordCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for direct multi-field payload reads after constructor alloc returns a writable row directly, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForFrozenPackedPayloadMatch(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	End

def fold_frozen() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match node in frozen:
		Expr.Lit(value):
			value
		Expr.End:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payload_decode.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected frozen packed payload match to use a single eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected frozen packed payload match to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected frozen packed payload match to avoid ctx_packed_store_read_word after eager decode, got:\n%s", output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected frozen packed payload eager decode to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForMixedFrozenPackedPayloadMatch(t *testing.T) {
	src := `packed enum Expr:
	Hold(value: any i32&)
	End

def fold_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	node: Expr = new[store] Expr.Hold(value: local_ref)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match node in frozen:
		Expr.Hold(value):
			1
		Expr.End:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_payload_decode.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected mixed frozen packed payload match to use a single eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected mixed frozen packed payload match to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected mixed frozen packed payload match to avoid ctx_packed_store_read_word after eager decode, got:\n%s", output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected mixed frozen packed payload eager decode to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRUsesWordReadHelperForRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
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
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for repeated packed common-field reads after constructor alloc returns a writable row directly, got %d decode calls:\n%s", decodeCalls, output)
	}
	for _, check := range []string{"packed.common.store.arena", "packed.common.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	arenaExtracts := strings.Count(output, "packed.common.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected repeated packed common-field reads in one block to reuse a single arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.common.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected repeated packed common-field reads in one block to reuse a single state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForFrozenRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold_common_frozen() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	in frozen:
		return node.span + node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_field_cache.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected repeated frozen packed common-field reads to decode once, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected repeated frozen packed common-field reads to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected repeated frozen packed common-field reads in one block to reuse a single decode arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected repeated frozen packed common-field reads in one block to reuse a single decode state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold_common_frozen_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return node.span + node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_field_cache_direct.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint to decode once, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForFrozenHelperWrappedRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def fold_common_frozen_wrapped_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	boxed: Box = wrap_node(node)
	return boxed.node.span + boxed.node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_field_cache_direct.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads outside an explicit frozen checkpoint to decode once, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads outside an explicit frozen checkpoint to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRInvalidatesFrozenHelperWrappedDecodeCacheAfterFieldAssignment(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

def fold_common_frozen_wrapped_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	boxed: Box = Box(new[store] Expr.Lit(span: 7, value: 5))
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = boxed.node.span
	boxed.node <- other
	_ = frozen
	return first + boxed.node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_field_cache_reassign.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads separated by field reassignment to decode twice, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_word, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment to keep reusing one decoded-store arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment to keep reusing one decoded-store state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForMixedFrozenRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Hold(value: any i32&)
	End

def fold_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	node: Expr = new[store] Expr.Hold(span: 7, value: local_ref)
	frozen: Expr.Store[Frozen] = freeze(move store)
	in frozen:
		return node.span + node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_field_cache.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected repeated mixed frozen packed common-field reads to decode once, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected repeated mixed frozen packed common-field reads to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected repeated mixed frozen packed common-field reads in one block to reuse a single decode arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected repeated mixed frozen packed common-field reads in one block to reuse a single decode state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForMixedFrozenMatchedPayloadRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

def fold_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	node: Expr = new[store] Expr.Wrap(span: 9, child: held)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match node in frozen:
		Expr.Wrap(child: child_alias):
			child_alias.span + child_alias.span
		Expr.Int(value: _):
			0
		Expr.Hold(value: _):
			1
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_matched_payload_field_cache.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected frozen outer match plus repeated mixed child common-field reads to use exactly two decodes, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected repeated mixed child common-field reads recovered through frozen match to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected frozen outer match plus mixed child common-field reads to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

repr(c) struct Box:
	node: Expr

repr(c) struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	node: Expr = new[store] Expr.Wrap(span: 9, child: held)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match wrapped.items[0u].node in frozen:
		Expr.Wrap(child: child_alias):
			child_alias.span + child_alias.span
		Expr.Int(value: _):
			0
		Expr.Hold(value: _):
			1
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_matched_payload_field_cache.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected helper-indexed frozen outer match plus repeated mixed child common-field reads to use exactly two decodes, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected helper-indexed repeated mixed child common-field reads recovered through frozen match to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected helper-indexed frozen outer match plus mixed child common-field reads to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForFrozenPayloadlessPackedMatchWithMatchedValueFieldReads(t *testing.T) {
	src := `packed enum Flag:
	common:
		span: int
	Yes
	No

def choose() -> int:
	region scratch(256u)
	store: Flag.Store[Local] = Flag.Store(scratch)
	node: Flag = new[store] Flag.Yes(span: 7)
	frozen: Flag.Store[Frozen] = freeze(move store)
	return match node in frozen:
		Flag.Yes:
			node.span + node.span
		Flag.No:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payloadless_match_fields.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads to use a single decode, got %d decode calls:\n%s", decodeCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads to avoid ctx_packed_store_read_word after eager decode, got:\n%s", output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
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
	store: Flag.Store[Local] = Flag.Store(scratch)
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
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for payloadless packed match after constructor alloc returns a writable row directly, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateOptimizedLLVMIRKeepsPackedAllocResultOutOfLineForWordHandle(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		return match node:
			Expr.Lit(value):
				value + node.span

export func fold_export() -> int = fold
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_word_handle_opt.llcontext", src)
	output, err := GenerateLLVMIRWithOptAndPackedABI(result, OptimizationLevel3, PackedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedABI returned error: %v", err)
	}

	if !strings.Contains(output, "call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result(") {
		t.Fatalf("expected optimized word-handle IR to keep ctx_packed_store_alloc_fixed_result as an out-of-line helper call, got:\n%s", output)
	}
	if strings.Contains(output, "ctx_packed_store_alloc_fixed_result.exit") {
		t.Fatalf("expected optimized word-handle IR to avoid inlining ctx_packed_store_alloc_fixed_result slow-path labels into callers, got:\n%s", output)
	}
}
