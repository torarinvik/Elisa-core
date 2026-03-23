//go:build cgo

package backend

import (
	"strings"
	"testing"

	"llcontext/src/lexer"
	"llcontext/src/parser"
	"llcontext/src/semantic"
)

const parallelForConcurrencyPrelude = `extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]
extern pool_shutdown(pool: any ThreadPool&) -> void can[Pool.Shutdown]

def pool_submit1[A, R](pool: any ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending] can[Pool.Submit, Memory.Allocate, Abort.Panic]:
	task: Task[R, Pending] = zeroed
	return move task

def task_group_new() -> TaskGroup:
	group: TaskGroup = zeroed
	return move group

def task_group_add[R](group: any TaskGroup&, task: Task[R, Pending]) -> void can[Memory.Allocate, Abort.Panic]:
	_ = move task

def task_group_wait_all(group: any TaskGroup&) -> void can[Pool.WaitAll]:
	pass
`

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

func packedABITestName(abi packedEnumABIMode) string {
	switch abi {
	case packedEnumABIRowHandle:
		return "row_handle"
	case packedEnumABIWordHandle:
		return "word_handle"
	case packedEnumABIIndexSOA:
		return "index_soa"
	default:
		return "packed_abi_unknown"
	}
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

func TestGenerateLLVMIRLowersPackedTailPayloadsWithVariableAllocInWordHandleABI(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(count: 3u, items: source_items[0u:3u])
		return match node:
			Expr.Block(count: _, items: items):
				items[0u] + items[2u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_word_handle.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	checks := []string{
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_result(",
		"call ptr @arena_memcpy(",
		"packed.tail.view.len",
		"packed.tail.view.elem_size",
		"call ptr @ctx_packed_store_decode(",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result(",
		"call i64 @ctx_packed_store_read_word(",
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed tail payload lowering to avoid %q, got:\n%s", bad, output)
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

func TestGenerateLLVMIRUsesIndexReadHelperForMultiFieldPackedPayloadMatch(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_payload_words_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected mixed packed payload match in index-soa mode to use ctx_packed_store_read_index_tag for dispatch, got:\n%s", output)
	}
	readWordCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readWordCalls != 2 {
		t.Fatalf("expected two direct index payload word reads for Pair.Both payload in index-soa mode, got %d helper calls:\n%s", readWordCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for direct multi-field payload reads in index-soa mode, got %d decode calls:\n%s", decodeCalls, output)
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

func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPackedPayloadMatchInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payload_decode_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen packed payload match in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected frozen packed payload match in index-soa mode to use one direct payload word read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed payload match in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexReadHelpersForMixedFrozenPackedPayloadMatchInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_payload_decode_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected mixed frozen packed payload match in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 1 {
		t.Fatalf("expected mixed frozen packed payload match in index-soa mode to use one direct payload word read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected mixed frozen packed payload match in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexReadHelperForRepeatedCommonFieldReads(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated packed common-field reads in index-soa mode to lower through ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if !strings.Contains(output, "call void @ctx_packed_store_record_prefix_words(") {
		t.Fatalf("expected index-soa constructor lowering to record fixed prefix words, got:\n%s", output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for repeated packed common-field reads in index-soa mode, got %d decode calls:\n%s", decodeCalls, output)
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

func TestGenerateLLVMIRUsesIndexWordReadForFrozenRepeatedCommonFieldReads(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_frozen_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated frozen packed common-field reads in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got %d decode calls:\n%s", decodeCalls, output)
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

func TestGenerateLLVMIRUsesIndexWordReadForFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_field_cache_direct_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexWordReadForFrozenHelperWrappedRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_field_cache_direct_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: Expr

repr(c) struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_common_frozen_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return wrapped.items[0u].node.span + wrapped.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_field_cache_direct.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to decode once, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRUsesIndexWordReadForHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: Expr

repr(c) struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_common_frozen_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return wrapped.items[0u].node.span + wrapped.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_field_cache_direct_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRAvoidsDecodeForFrozenHelperWrappedCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_field_cache_reassign_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-wrapped frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRInvalidatesFrozenHelperIndexedDecodeCacheAfterFieldAssignment(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

repr(c) struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_common_frozen_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.items[0u].node.span
	wrapped.items[0u].node <- other
	_ = frozen
	return first + wrapped.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_field_cache_reassign.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected helper-indexed frozen packed common-field reads separated by field reassignment to decode twice, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_word, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment to keep reusing one decoded-store arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment to keep reusing one decoded-store state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRAvoidsDecodeForFrozenHelperIndexedCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

repr(c) struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_common_frozen_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.items[0u].node.span
	wrapped.items[0u].node <- other
	_ = frozen
	return first + wrapped.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_field_cache_reassign_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForNestedRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return wrapped.meta.items[0u].node.span + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_helper_indexed_field_cache_direct.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to decode once, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexWordReadForNestedRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return wrapped.meta.items[0u].node.span + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_helper_indexed_field_cache_direct_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForNestedWildcardRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_wild_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return wrapped.meta.items[0u].node.span + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_helper_indexed_field_cache_direct.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to decode once, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint to reuse a single decode state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRUsesIndexWordReadForNestedWildcardRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_wild_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return wrapped.meta.items[0u].node.span + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_helper_indexed_field_cache_direct_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRInvalidatesNestedWildcardRebasedHelperIndexedFrozenDecodeCacheAfterFieldAssignment(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_wild_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.meta.items[0u].node.span
	wrapped.meta.items[0u].node <- other
	_ = frozen
	return first + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_helper_indexed_field_cache_reassign.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads separated by field reassignment to decode twice, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_word, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment to keep reusing one decoded-store arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment to keep reusing one decoded-store state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRAvoidsDecodeForNestedWildcardRebasedHelperIndexedFrozenCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_wild_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.meta.items[0u].node.span
	wrapped.meta.items[0u].node <- other
	_ = frozen
	return first + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_helper_indexed_field_cache_reassign_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRInvalidatesNestedRebasedHelperIndexedFrozenDecodeCacheAfterFieldAssignment(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.meta.items[0u].node.span
	wrapped.meta.items[0u].node <- other
	_ = frozen
	return first + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_helper_indexed_field_cache_reassign.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads separated by field reassignment to decode twice, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_word, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
	arenaExtracts := strings.Count(output, "packed.decode.store.arena = extractvalue %Expr__Store")
	if arenaExtracts != 1 {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment to keep reusing one decoded-store arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := strings.Count(output, "packed.decode.store.state = extractvalue %Expr__Store")
	if stateExtracts != 1 {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment to keep reusing one decoded-store state extractvalue, got %d extracts:\n%s", stateExtracts, output)
	}
}

func TestGenerateLLVMIRAvoidsDecodeForNestedRebasedHelperIndexedFrozenCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_common_frozen_nested_helper_indexed_reassign() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	items: array[Box, 2] = [Box(new[store] Expr.Lit(span: 3, value: 1)), Box(new[store] Expr.Lit(span: 7, value: 5))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	other: Expr = new[store] Expr.Lit(span: 11, value: 9)
	frozen: Expr.Store[Frozen] = freeze(move store)
	first: int = wrapped.meta.items[0u].node.span
	wrapped.meta.items[0u].node <- other
	_ = frozen
	return first + wrapped.meta.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_helper_indexed_field_cache_reassign_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested rebased helper-indexed frozen packed common-field reads after reassignment in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexReadHelpersForMixedFrozenRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_field_cache_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated mixed frozen packed common-field reads in index-soa mode to use ctx_packed_store_read_index_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected repeated mixed frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexReadHelpersForMixedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_matched_payload_field_cache_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesSingleDecodeForNestedRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_nested_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	items: array[Box, 2] = [Box(new[store] Expr.Int(span: 2, value: 1)), Box(new[store] Expr.Wrap(span: 9, child: held))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match wrapped.meta.items[0u].node in frozen:
		Expr.Wrap(child: child_alias):
			child_alias.span + child_alias.span
		Expr.Int(value: _):
			0
		Expr.Hold(value: _):
			1
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_rebased_helper_indexed_matched_payload_field_cache.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus repeated mixed child common-field reads to use exactly two decodes, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected nested rebased helper-indexed repeated mixed child common-field reads recovered through frozen match to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus mixed child common-field reads to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_matched_payload_field_cache_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForNestedRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_nested_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	items: array[Box, 2] = [Box(new[store] Expr.Int(span: 2, value: 1)), Box(new[store] Expr.Wrap(span: 9, child: held))]
	wrapped: Wrapper = wrap_submeta(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match wrapped.meta.items[0u].node in frozen:
		Expr.Wrap(child: child_alias):
			child_alias.span + child_alias.span
		Expr.Int(value: _):
			0
		Expr.Hold(value: _):
			1
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_rebased_helper_indexed_matched_payload_field_cache_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForNestedWildcardRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReads(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_nested_wild_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	items: array[Box, 2] = [Box(new[store] Expr.Int(span: 2, value: 1)), Box(new[store] Expr.Wrap(span: 9, child: held))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match wrapped.meta.items[0u].node in frozen:
		Expr.Wrap(child: child_alias):
			child_alias.span + child_alias.span
		Expr.Int(value: _):
			0
		Expr.Hold(value: _):
			1
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_rebased_helper_indexed_matched_payload_field_cache.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 2 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus repeated mixed child common-field reads to use exactly two decodes, got %d decode calls:\n%s", decodeCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_word(")
	if readCalls != 0 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated mixed child common-field reads recovered through frozen match to avoid ctx_packed_store_read_word after decode, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus mixed child common-field reads to avoid ctx_packed_store_read_tag after eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForNestedWildcardRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

repr(c) struct Box:
	node: Expr

repr(c) struct Meta:
	items: view[Box]

repr(c) struct Wrapper:
	meta: Meta

@borrows_return_field_rebased(meta.items[*].node, src[*].node)
extern wrap_submeta_nodes_wild(src: view[Box], start: usize, end: usize) -> Wrapper

def fold_nested_wild_helper_indexed_child_common_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: scratch i32& = new[scratch] 7i32
	held: Expr = new[store] Expr.Hold(span: 5, value: local_ref)
	items: array[Box, 2] = [Box(new[store] Expr.Int(span: 2, value: 1)), Box(new[store] Expr.Wrap(span: 9, child: held))]
	wrapped: Wrapper = wrap_submeta_nodes_wild(items[1u:2u], 0u, 1u)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match wrapped.meta.items[0u].node in frozen:
		Expr.Wrap(child: child_alias):
			child_alias.span + child_alias.span
		Expr.Int(value: _):
			0
		Expr.Hold(value: _):
			1
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_wildcard_rebased_helper_indexed_matched_payload_field_cache_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 3 {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to use direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested wildcard rebased helper-indexed frozen outer match plus repeated mixed child common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPayloadlessPackedMatchWithMatchedValueFieldReadsInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payloadless_match_fields_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads in index-soa mode to use two direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads in index-soa mode to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexTagReadHelperForPayloadlessPackedMatch(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tag_read_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected payloadless packed match in index-soa mode to read tag through ctx_packed_store_read_index_tag, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 0 {
		t.Fatalf("expected payloadless packed match in index-soa mode to avoid ctx_packed_store_read_index_word, got %d helper calls:\n%s", readCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for payloadless packed match in index-soa mode, got %d decode calls:\n%s", decodeCalls, output)
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

func TestGenerateLLVMIRUsesSingleDecodeForFrozenPackedOpenStmt(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_open() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	open node in frozen as Expr.Lit(value: value):
		return value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_open_stmt.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected frozen packed open stmt to use a single eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
	if !strings.Contains(output, "declare void @llvm.trap()") || !strings.Contains(output, "call void @llvm.trap()") {
		t.Fatalf("expected packed open stmt to lower mismatch handling through llvm.trap, got:\n%s", output)
	}
	if !strings.Contains(output, "unreachable") {
		t.Fatalf("expected packed open stmt mismatch block to end in unreachable, got:\n%s", output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPackedOpenStmtInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_open() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	open node in frozen as Expr.Lit(value: value):
		return value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_open_stmt_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen packed open stmt in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected frozen packed open stmt in index-soa mode to use direct prefix word reads, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed open stmt in index-soa mode to avoid eager decode, got:\n%s", output)
	}
	if !strings.Contains(output, "declare void @llvm.trap()") || !strings.Contains(output, "call void @llvm.trap()") {
		t.Fatalf("expected packed open stmt mismatch handling to remain trap-based, got:\n%s", output)
	}
}

func TestGenerateLLVMIRAvoidsEagerDecodeForFrozenMixedPackedMatchInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match node in frozen:
		Expr.Lit(value):
			value + node.span
		Expr.End:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected frozen mixed packed match in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected frozen mixed packed match in index-soa mode to use direct prefix word reads, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen mixed packed match in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSingleDecodeForFrozenPackedViewStmt(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_view() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	view node in frozen as Expr.Lit(lit):
		return lit.value + lit.span + lit.value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_view_stmt.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected frozen packed view stmt to use a single eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected frozen packed view stmt field reads to reuse the decoded row instead of ctx_packed_store_read_word, got:\n%s", output)
	}
	if !strings.Contains(output, "declare void @llvm.trap()") || !strings.Contains(output, "call void @llvm.trap()") {
		t.Fatalf("expected packed view stmt to lower mismatch handling through llvm.trap, got:\n%s", output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPackedViewStmtInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_view() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	view node in frozen as Expr.Lit(lit):
		return lit.value + lit.span + lit.value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_view_stmt_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 3 {
		t.Fatalf("expected frozen packed view stmt in index-soa mode to use three direct index word reads, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed view stmt in index-soa mode to avoid eager decode, got:\n%s", output)
	}
	if !strings.Contains(output, "declare void @llvm.trap()") || !strings.Contains(output, "call void @llvm.trap()") {
		t.Fatalf("expected packed view stmt mismatch handling to remain trap-based, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexTailViewFastPathForFrozenPackedViewStmtInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def fold_view() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	source_items: array[int, 3] = [1, 2, 3]
	node: Expr = new[store] Expr.Block(count: 3u, items: source_items[0u:3u])
	frozen: Expr.Store[Frozen] = freeze(move store)
	view node in frozen as Expr.Block(block):
		return block.items[0u] + block.items[2u]
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_view_stmt_tail_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"call i64 @ctx_packed_store_read_index_word(",
		"packed.payload.tail.data",
		"packed.payload.tail.len",
		"packed.payload.tail.elem_size",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected frozen packed tail view stmt in index-soa mode to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed tail view stmt in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedStoreCountAndIndexForWordHandle(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def walk(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 3)
		right: Expr = new Expr.Int(span: 2, value: 4)
		_ = new Expr.Add(span: 3, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	total: mutable int = 0
	index: mutable usize = 0u
	while index < frozen.count:
		node: Expr = frozen[index]
		if node in frozen as Expr.Int(value: value):
			total <- total + value + node.span
		index <- index + 1u
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_count_index_word.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i64 @ctx_packed_store_count(", "call i64 @ctx_packed_store_word_handle_at(", "packed.store.count.state", "packed.store.index.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedStoreCountAndIndexForRowHandle(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def first(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(value: 7)
	frozen: Expr.Store[Frozen] = freeze(move store)
	node: Expr = frozen[0u]
	match node in frozen:
		Expr.Int(value: value):
			if frozen.count > 0u:
				return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_count_index_row.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIRowHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call void @ctx_packed_store_record_row_ptr(", "call i64 @ctx_packed_store_count(", "call ptr @ctx_packed_store_row_ptr_at("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedStoreCountAndIndexForIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def walk(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 3)
		right: Expr = new Expr.Int(span: 2, value: 4)
		_ = new Expr.Add(span: 3, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	total: mutable int = 0
	index: mutable usize = 0u
	while index < frozen.count:
		node: Expr = frozen[index]
		if node in frozen as Expr.Int(value: value):
			total <- total + value + node.span
		index <- index + 1u
	return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_count_index_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"%PackedStoreIndexAllocResult = type { ptr, i32 }",
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_index_result(",
		"call i64 @ctx_packed_store_count(",
		"call i32 @ctx_packed_store_index_at(",
		"call i32 @ctx_packed_store_read_index_tag(",
		"call i64 @ctx_packed_store_read_index_word(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected output to avoid decode_index on the index-SOA fast path, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedStoreSliceForWordHandle(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def walk(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 3)
		right: Expr = new Expr.Int(span: 2, value: 4)
		_ = new Expr.Add(span: 3, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	chunk: dview[Expr] = frozen[1u:frozen.count]
	if chunk.len > 0u:
		node: Expr = chunk[0u]
		if node in frozen as Expr.Int(value: value):
			return value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_slice_word.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call %DynArrayView @ctx_packed_store_view(", "packed.store.view.state", "call i64 @ctx_packed_store_count("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedStoreSliceForRowHandle(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def first(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(value: 7)
	frozen: Expr.Store[Frozen] = freeze(move store)
	chunk: dview[Expr] = frozen[0u:frozen.count]
	if chunk.len > 0u:
		node: Expr = chunk[0u]
		match node in frozen:
			Expr.Int(value: value):
				return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_slice_row.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIRowHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call %DynArrayView @ctx_packed_store_view(", "call void @ctx_packed_store_record_row_ptr("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedStoreSliceForIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def walk(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(span: 1, value: 3)
		right: Expr = new Expr.Int(span: 2, value: 4)
		_ = new Expr.Add(span: 3, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	chunk: dview[Expr] = frozen[1u:frozen.count]
	if chunk.len > 0u:
		node: Expr = chunk[0u]
		if node in frozen as Expr.Int(value: value):
			return value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_slice_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"call %DynArrayView @ctx_packed_store_indices_view(",
		"call i64 @ctx_packed_store_count(",
		"call i32 @ctx_packed_store_read_index_tag(",
		"call i64 @ctx_packed_store_read_index_word(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected output to avoid decode_index for sliced index-SOA reads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForMixedPackedMatchInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_index_soa_tag_read_mixed.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected mixed packed match in index-soa mode to avoid eager decode on the fast path, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexTailViewFastPathInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(count: 3u, items: source_items[0u:3u])
		return match node:
			Expr.Block(count: _, items: items):
				items[0u] + items[2u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word(", "packed.payload.tail.data", "packed.payload.tail.len", "packed.payload.tail.elem_size"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected index-soa tail payload match to avoid full decode on the fast path, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersPackedStoreTagsViewForWordHandle(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def first_tag(owner: Arena) -> Expr.Tag:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(value: 1)
		right: Expr = new Expr.Int(value: 2)
		_ = new Expr.Add(left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	tags: dview[Expr.Tag] = frozen.tags
	return tags[0u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_tags_word.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"ctx_packed_store_tags_view", "call i64 @ctx_packed_store_count("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"call ptr @ctx_packed_store_decode(", "call i32 @ctx_packed_store_read_tag("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed store tag view lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedStoreTagsViewForRowHandle(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def first_tag(owner: Arena) -> Expr.Tag:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(value: 7)
	frozen: Expr.Store[Frozen] = freeze(move store)
	tags: dview[Expr.Tag] = frozen.tags
	return tags[0u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_tags_row.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIRowHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"ctx_packed_store_tags_view", "call i64 @ctx_packed_store_count("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"call ptr @ctx_packed_store_decode(", "call i32 @ctx_packed_store_read_tag("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed store tag view lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersPackedStoreTagsViewForIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def first_tag(owner: Arena) -> Expr.Tag:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(value: 1)
		right: Expr = new Expr.Int(value: 2)
		_ = new Expr.Add(left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	tags: dview[Expr.Tag] = frozen.tags
	return tags[0u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_store_tags_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"ctx_packed_store_tags_view", "call i64 @ctx_packed_store_count("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"call ptr @ctx_packed_store_decode(", "call i32 @ctx_packed_store_read_tag("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected packed store tag view lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersParallelForOverFrozenPackedStoreForWordHandle(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(span: 1, value: 3)
		_ = new Expr.Int(span: 2, value: 4)
	frozen: Expr.Store[Frozen] = freeze(move store)
	pool workers(2u):
		parallel for node in frozen:
			if node in frozen as Expr.Int(value: value):
				_ = value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_parallel_for_word.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"@__parallel_for_0_worker", "@pool_submit1__", "@task_group_add__", "@task_group_wait_all(", "call i64 @ctx_packed_store_count("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersParallelForOverFrozenPackedStoreForIndexSOA(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(span: 1, value: 3)
		_ = new Expr.Int(span: 2, value: 4)
	frozen: Expr.Store[Frozen] = freeze(move store)
	pool workers(2u):
		parallel for node in frozen:
			if node in frozen as Expr.Int(value: value):
				_ = value + node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_parallel_for_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"@__parallel_for_0_worker", "@pool_submit1__", "@task_group_add__", "@task_group_wait_all(", "call i64 @ctx_packed_store_count(", "call i32 @ctx_packed_store_read_index_tag(", "call i64 @ctx_packed_store_read_index_word("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected index-soa parallel-for over frozen packed store to avoid eager decode on the fast path, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersParallelForOverPackedTagViewForWordHandle(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(value: 1)
		right: Expr = new Expr.Int(value: 2)
		_ = new Expr.Add(left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	tags: dview[Expr.Tag] = frozen.tags
	pool workers(2u):
		parallel for tag at i in tags:
			if tag == Expr.Tag.Add:
				_ = i
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_parallel_for_tags_word.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"@__parallel_for_0_worker", "ctx_packed_store_tags_view", "@pool_submit1__", "@task_group_wait_all("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersParallelForOverPackedTagViewAcrossPackedABIs(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Int(value: 1)
		right: Expr = new Expr.Int(value: 2)
		_ = new Expr.Add(left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	tags: dview[Expr.Tag] = frozen.tags
	pool workers(2u):
		parallel for tag at i in tags:
			if tag == Expr.Tag.Add:
				_ = i
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_parallel_for_tags_parity.llcontext", src)
	cases := []struct {
		abi         packedEnumABIMode
		mustContain []string
	}{
		{
			abi:         packedEnumABIWordHandle,
			mustContain: []string{"ctx_packed_store_tags_view", "@__parallel_for_0_worker", "@pool_submit1__", "@task_group_wait_all("},
		},
		{
			abi:         packedEnumABIIndexSOA,
			mustContain: []string{"ctx_packed_store_tags_view", "@__parallel_for_0_worker", "@pool_submit1__", "@task_group_wait_all("},
		},
		{
			abi:         packedEnumABIRowHandle,
			mustContain: []string{"ctx_packed_store_tags_view", "@__parallel_for_0_worker", "@pool_submit1__", "@task_group_wait_all(", "call void @ctx_packed_store_record_row_ptr("},
		},
	}
	for _, tc := range cases {
		t.Run(packedABITestName(tc.abi), func(t *testing.T) {
			output, err := generateLLVMIRWithPackedABIForTest(result, tc.abi)
			if err != nil {
				t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
			}
			for _, check := range tc.mustContain {
				if !strings.Contains(output, check) {
					t.Fatalf("expected output to contain %q, got:\n%s", check, output)
				}
			}
			for _, bad := range []string{"call ptr @ctx_packed_store_decode(", "call i32 @ctx_packed_store_read_tag("} {
				if strings.Contains(output, bad) {
					t.Fatalf("expected packed tag-view parallel-for lowering to avoid %q, got:\n%s", bad, output)
				}
			}
		})
	}
}

func TestGenerateLLVMIRLowersParallelForOverFrozenPackedStoreForRowHandle(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	Int(value: int)

def visit(owner: Arena) -> int can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Int(value: 3)
	frozen: Expr.Store[Frozen] = freeze(move store)
	pool workers(2u):
		parallel for node in frozen:
			pass
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_parallel_for_row.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIRowHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{"@__parallel_for_0_worker", "@pool_submit1__", "@task_group_wait_all(", "call void @ctx_packed_store_record_row_ptr(", "call i64 @ctx_packed_store_count("} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
