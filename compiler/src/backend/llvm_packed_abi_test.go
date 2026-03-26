//go:build cgo

package backend

import (
	"fmt"
	"regexp"
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

func countStoreFieldExtracts(output string, storeType string, fieldIndex int) int {
	pattern := fmt.Sprintf(`extractvalue %%%s %%[^,]+, %d`, storeType, fieldIndex)
	re := regexp.MustCompile(pattern)
	return len(re.FindAllStringIndex(output, -1))
}

func generateLLVMIRWithPackedABIForTest(result *semantic.Result, abi packedEnumABIMode) (string, error) {
	g, err := newLLVMGenerator(result)
	if err != nil {
		return "", err
	}
	defer g.dispose()
	switch abi {
	case packedEnumABIRowHandle:
		g.packedProfile = mustLegacyPackedLoweringProfile(PackedEnumABIRowHandle)
	case packedEnumABIWordHandle:
		g.packedProfile = mustLegacyPackedLoweringProfile(PackedEnumABIWordHandle)
	case packedEnumABIIndexSOA:
		g.packedProfile = mustLegacyPackedLoweringProfile(PackedEnumABIIndexSOA)
	case packedEnumABIVariantSparse:
		g.packedProfile = mustLegacyPackedLoweringProfile(PackedEnumABIVariantSparse)
	default:
		return "", fmt.Errorf("unsupported packed enum ABI mode %d", abi)
	}
	g.packedEnumABI = abi
	if err := g.emitModule(); err != nil {
		return "", err
	}
	if err := g.verify(); err != nil {
		return "", err
	}
	return g.printModule(), nil
}

func generateLLVMIRWithDefaultPackedLoweringForTest(result *semantic.Result) (string, error) {
	return GenerateLLVMIRWithOpt(result, OptimizationLevel0)
}

func packedABITestName(abi packedEnumABIMode) string {
	switch abi {
	case packedEnumABIRowHandle:
		return "row_handle"
	case packedEnumABIWordHandle:
		return "word_handle"
	case packedEnumABIIndexSOA:
		return "index_soa"
	case packedEnumABIVariantSparse:
		return "variant_sparse"
	default:
		return "packed_abi_unknown"
	}
}

func TestGenerateLLVMIRRecordsCanonicalPackedLoweringMetadataByDefault(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(value: 5)
		return match node:
			Expr.Lit(value):
				value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_metadata_default.llcontext", src)
	if _, err := generateLLVMIRWithDefaultPackedLoweringForTest(result); err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if result.PackedLowering.Contract != string(PackedLoweringContractCanonicalCompilerGraph) {
		t.Fatalf("expected canonical packed lowering contract metadata, got %q", result.PackedLowering.Contract)
	}
	if result.PackedLowering.CanonicalPackedLowering != string(PackedEnumABIVariantSparse) {
		t.Fatalf("expected canonical packed lowering metadata to record %q, got %q", PackedEnumABIVariantSparse, result.PackedLowering.CanonicalPackedLowering)
	}
	if result.PackedLowering.UsesLegacyOverride {
		t.Fatalf("expected canonical packed lowering metadata not to mark a legacy override, got %+v", result.PackedLowering)
	}
	if result.PackedLowering.PublicationReadonlyGateStoreState != "Frozen" {
		t.Fatalf("expected frozen publication gate metadata, got %q", result.PackedLowering.PublicationReadonlyGateStoreState)
	}
	if !result.PackedLowering.OnePackedEnumOneHandleInvariant {
		t.Fatalf("expected one-packed-enum/one-handle invariant metadata to be recorded")
	}
}

func TestGenerateLLVMIRRecordsLegacyPackedLoweringMetadataForOverride(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(value: 5)
		return match node:
			Expr.Lit(value):
				value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_metadata_legacy.llcontext", src)
	if _, err := GenerateLLVMIRWithOptAndPackedABI(result, OptimizationLevel0, PackedEnumABIWordHandle); err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedABI returned error: %v", err)
	}
	if result.PackedLowering.Contract != string(PackedLoweringContractLegacyOverride) {
		t.Fatalf("expected legacy override packed lowering metadata, got %q", result.PackedLowering.Contract)
	}
	if !result.PackedLowering.UsesLegacyOverride {
		t.Fatalf("expected legacy override metadata to mark the override, got %+v", result.PackedLowering)
	}
	if result.PackedLowering.LegacyOverride != string(PackedEnumABIWordHandle) {
		t.Fatalf("expected legacy override metadata %q, got %q", PackedEnumABIWordHandle, result.PackedLowering.LegacyOverride)
	}
}

func TestGenerateLLVMIRUsesEnumPackedABIOverrideWithoutGlobalOverride(t *testing.T) {
	src := `@packed_abi(dense_fixed)
packed enum Pair:
	Left(value: int)
	Right(value: int)

def fold(node: Pair, store: Pair.Store[Frozen]) -> int:
	if node in store as Pair.Left(value: value):
		return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_enum_override.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "ctx_packed_store_read_index_tag") {
		t.Fatalf("expected enum-level @packed_abi(dense_fixed) override to use dense fixed-width index tag reads, got:\n%s", output)
	}
	if strings.Contains(output, "ctx_packed_store_read_variant_sparse_tag") {
		t.Fatalf("expected enum-level @packed_abi(dense_fixed) override to avoid canonical variant-sparse tag reads, got:\n%s", output)
	}
	if result.PackedLowering.Contract != string(PackedLoweringContractCanonicalCompilerGraph) {
		t.Fatalf("expected result metadata to remain canonical without a global override, got %q", result.PackedLowering.Contract)
	}
	enumType, ok := result.NamedTypes["Pair"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Pair named type to resolve to semantic.EnumType, got %#v", result.NamedTypes["Pair"])
	}
	if !enumType.HasPackedABIOverride || enumType.PackedABIOverride != string(PackedEnumABIDenseFixed) {
		t.Fatalf("expected semantic enum type to record dense-fixed override, got %+v", enumType)
	}
	if result.PackedLowering.UsesLegacyOverride {
		t.Fatalf("expected enum-level override not to mark result metadata as a legacy global override")
	}
}

func TestGenerateLLVMIRGlobalPackedABIOverrideBeatsEnumOverride(t *testing.T) {
	src := `@packed_abi(dense_fixed)
packed enum Pair:
	Left(value: int)
	Right(value: int)

def differs(left: Pair, right: Pair) -> bool:
	return left != right
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_enum_override_global_wins.llcontext", src)
	output, err := GenerateLLVMIRWithOptAndPackedABI(result, OptimizationLevel0, PackedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedABI returned error: %v", err)
	}
	if !strings.Contains(output, "define i1 @differs(i64") {
		t.Fatalf("expected global word-handle override to win over enum annotation, got:\n%s", output)
	}
	if strings.Contains(output, "ctx_packed_store_read_index_tag") {
		t.Fatalf("expected global word-handle override to suppress enum-level dense-fixed helpers, got:\n%s", output)
	}
	if !result.PackedLowering.UsesLegacyOverride || result.PackedLowering.LegacyOverride != string(PackedEnumABIWordHandle) {
		t.Fatalf("expected result metadata to record the global word-handle override, got %+v", result.PackedLowering)
	}
}

func TestGenerateLLVMIRUsesEnumPackedPrefixOverrideForDenseStoreConstruction(t *testing.T) {
	src := `@packed_abi(dense_fixed)
@packed_prefix(common_only)
packed enum Pair:
	common:
		span: int
	Both(left: int, right: int)
	End

def sum_pair() -> int:
	region scratch(256u)
	store: Pair.Store[Local] = Pair.Store(scratch)
	in store:
		node: Pair = new Pair.Both(span: 7, left: 2, right: 3)
		return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_prefix_override.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "call ptr @ctx_packed_store_state_new_with_prefix_words(") {
		t.Fatalf("expected enum-level @packed_prefix(common_only) override to use dense custom-prefix state helper, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_state_new_variant_sparse(") {
		t.Fatalf("expected enum-level dense overrides to avoid canonical variant-sparse store initialization, got:\n%s", output)
	}
	enumType, ok := result.NamedTypes["Pair"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Pair named type to resolve to semantic.EnumType, got %#v", result.NamedTypes["Pair"])
	}
	if !enumType.HasPackedPrefixOverride || enumType.PackedPrefixOverride != "common-only" {
		t.Fatalf("expected semantic enum type to record common-only packed prefix override, got %+v", enumType)
	}
}

func TestGenerateLLVMIRUsesEnumPackedProfileBuildHeavy(t *testing.T) {
	src := `@packed_profile(build_heavy)
packed enum Pair:
	common:
		span: int
	Both(left: int, right: int)
	End

def sum_pair() -> int:
	region scratch(256u)
	store: Pair.Store[Local] = Pair.Store(scratch)
	in store:
		node: Pair = new Pair.Both(span: 7, left: 2, right: 3)
		return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_profile_build_heavy.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "call ptr @ctx_packed_store_state_new_with_prefix_words(") {
		t.Fatalf("expected @packed_profile(build_heavy) to use dense custom-prefix state helper, got:\n%s", output)
	}
	if !strings.Contains(output, "ctx_packed_store_read_index_word") {
		t.Fatalf("expected @packed_profile(build_heavy) to select dense/index packed lowering helpers, got:\n%s", output)
	}
	enumType, ok := result.NamedTypes["Pair"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Pair named type to resolve to semantic.EnumType, got %#v", result.NamedTypes["Pair"])
	}
	if !enumType.HasPackedProfile || enumType.PackedProfile != "build-heavy" {
		t.Fatalf("expected semantic enum type to record build-heavy packed profile, got %+v", enumType)
	}
	if !enumType.HasPackedABIOverride || enumType.PackedABIOverride != string(PackedEnumABIDenseFixed) {
		t.Fatalf("expected build-heavy packed profile to default to dense-fixed ABI, got %+v", enumType)
	}
	if !enumType.HasPackedPrefixOverride || enumType.PackedPrefixOverride != "common-only" {
		t.Fatalf("expected build-heavy packed profile to default to common-only prefix, got %+v", enumType)
	}
	if result.PackedLowering.UsesLegacyOverride {
		t.Fatalf("expected enum-level packed profile not to mark result metadata as a legacy global override")
	}
}

func TestGenerateLLVMIRExplicitPackedABIOverridesPackedProfile(t *testing.T) {
	src := `@packed_profile(retained_reads)
@packed_abi(variant_sparse)
packed enum Pair:
	common:
		span: int
	Both(left: int, right: int)
	End

def sum_pair() -> int:
	region scratch(256u)
	store: Pair.Store[Local] = Pair.Store(scratch)
	in store:
		node: Pair = new Pair.Both(span: 7, left: 2, right: 3)
		return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_profile_explicit_abi_wins.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "call ptr @ctx_packed_store_state_new_variant_sparse(") {
		t.Fatalf("expected explicit @packed_abi(variant_sparse) to override packed profile ABI, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_state_new_with_prefix_words(") {
		t.Fatalf("expected explicit variant-sparse ABI override to suppress dense prefix helper, got:\n%s", output)
	}
	enumType, ok := result.NamedTypes["Pair"].(*semantic.EnumType)
	if !ok || enumType == nil {
		t.Fatalf("expected Pair named type to resolve to semantic.EnumType, got %#v", result.NamedTypes["Pair"])
	}
	if !enumType.HasPackedProfile || enumType.PackedProfile != "retained-reads" {
		t.Fatalf("expected semantic enum type to record retained-reads packed profile, got %+v", enumType)
	}
	if !enumType.HasPackedABIOverride || enumType.PackedABIOverride != string(PackedEnumABIVariantSparse) {
		t.Fatalf("expected explicit ABI override to win over packed profile ABI, got %+v", enumType)
	}
	if !enumType.HasPackedPrefixOverride || enumType.PackedPrefixOverride != "all-words" {
		t.Fatalf("expected retained-reads packed profile to keep its all-words prefix default when not explicitly overridden, got %+v", enumType)
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
		"call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(ptr %packed.alloc.store.arena, ptr %packed.alloc.store.state, i32 ",
		"call ptr @ctx_packed_store_decode(ptr %packed.alloc.store.arena, i64",
		"ptr %packed.alloc.store.state)",
		"extractvalue %Expr__Store",
		"extractvalue %PackedStoreAllocResult",
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
	for _, bad := range []string{"define i1 @differs(ptr", "icmp ne ptr", "call i64 @ctx_packed_store_alloc(", "call ptr @arena_alloc(", "ptrtoint ptr %packed.alloc to i64", "inttoptr i64", "call %PackedStoreAllocResult @ctx_packed_store_alloc_result(", "call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_result("} {
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

func TestGenerateLLVMIRLowersNonFinalPackedTailPayloadsInWordHandleABI(t *testing.T) {
	src := `packed enum Expr:
	Block(items: tail int, count: usize)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(items: source_items[0u:3u], count: 3u)
		return match node:
			Expr.Block(items: items, count: count):
				count.int() + items[0u] + items[2u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_non_final_word_handle.llcontext", src)
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
			t.Fatalf("expected non-final packed tail payload lowering to avoid %q, got:\n%s", bad, output)
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

func TestGenerateLLVMIRUsesCanonicalIndexReadHelpersForPackedPayloadMatchByDefault(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_payload_words_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(") {
		t.Fatalf("expected canonical packed lowering to use ctx_packed_store_read_variant_sparse_tag for dispatch, got:\n%s", output)
	}
	readWordCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readWordCalls != 2 {
		t.Fatalf("expected canonical packed lowering to use two direct variant-sparse payload word reads for Pair.Both, got %d helper calls:\n%s", readWordCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical packed lowering to avoid full decode for direct multi-field payload reads, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected canonical packed lowering to avoid row/word-handle payload reads, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected canonical packed lowering to avoid legacy index-soa payload reads, got:\n%s", output)
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
	if decodeCalls != 0 {
		t.Fatalf("expected frozen packed payload match to avoid eager decode when inline-capable variants share the enum, got %d decode calls:\n%s", decodeCalls, output)
	}
	if !strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected frozen packed payload match to use ctx_packed_store_read_tag for dispatch, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected frozen packed payload match to use ctx_packed_store_read_word for non-inline payload extraction, got:\n%s", output)
	}
	for _, check := range []string{"packed.tag.store.arena", "packed.arena"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected frozen packed payload fast path to contain %q, got:\n%s", check, output)
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

func TestGenerateLLVMIRUsesCanonicalIndexReadHelpersForFrozenPackedPayloadMatchByDefault(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payload_decode_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(") {
		t.Fatalf("expected canonical frozen packed payload match to use direct variant-sparse tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 1 {
		t.Fatalf("expected canonical frozen packed payload match to use one direct variant-sparse payload read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen packed payload match to avoid eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected canonical frozen packed payload match to avoid row/word-handle payload reads, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected canonical frozen packed payload match to avoid legacy index-soa payload reads, got:\n%s", output)
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
	if decodeCalls != 0 {
		t.Fatalf("expected mixed frozen packed payload match to avoid eager decode when inline-capable variants share the enum, got %d decode calls:\n%s", decodeCalls, output)
	}
	if !strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected mixed frozen packed payload match to use ctx_packed_store_read_tag for dispatch, got:\n%s", output)
	}
	if !strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected mixed frozen packed payload match to use ctx_packed_store_read_word for non-inline payload extraction, got:\n%s", output)
	}
	for _, check := range []string{"packed.tag.store.arena", "packed.arena"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected mixed frozen packed payload fast path to contain %q, got:\n%s", check, output)
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
	arenaExtracts := countStoreFieldExtracts(output, "Expr__Store", 0)
	if arenaExtracts != 1 {
		t.Fatalf("expected repeated packed common-field reads in one block to reuse a single arena extractvalue, got %d extracts:\n%s", arenaExtracts, output)
	}
	stateExtracts := countStoreFieldExtracts(output, "Expr__Store", 2)
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

func TestGenerateLLVMIRUsesSideWordHelpersForRepeatedSideTableCommonFieldReadsByDefault(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		return node.span + node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_side_table_common_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call ptr @ctx_packed_store_state_new_variant_sparse_with_side_words(") {
		t.Fatalf("expected canonical packed lowering to allocate side columns for side-tabled common fields, got:\n%s", output)
	}
	if !strings.Contains(output, "call void @ctx_packed_store_record_side_words(") {
		t.Fatalf("expected canonical packed constructor lowering to record side-table common-field words, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated side-tabled common-field reads to lower through ctx_packed_store_read_side_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_variant_sparse_word(") {
		t.Fatalf("expected side-tabled common-field reads to bypass inline variant-sparse word helpers, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSideWordHelpersForRepeatedSideTableCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		return node.span + node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_side_table_common_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call ptr @ctx_packed_store_state_new_with_side_words(") {
		t.Fatalf("expected index-soa packed lowering to allocate side columns for side-tabled common fields, got:\n%s", output)
	}
	if !strings.Contains(output, "call void @ctx_packed_store_record_side_words(") {
		t.Fatalf("expected index-soa packed constructor lowering to record side-table common-field words, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 2 {
		t.Fatalf("expected repeated side-tabled common-field reads in index-soa mode to lower through ctx_packed_store_read_side_word twice, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected side-tabled common-field reads in index-soa mode to bypass inline index-word helpers, got:\n%s", output)
	}
}

func TestGenerateLLVMIRRejectsSideTableCommonFieldsInUnsupportedPackedABIs(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

def fold_common() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		return node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_side_table_common_word_handle_reject.llcontext", src)
	_, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err == nil {
		t.Fatal("expected unsupported side-table common-field ABI error, got none")
	}
	if !strings.Contains(err.Error(), "does not support side-tabled common fields") {
		t.Fatalf("expected unsupported side-table common-field ABI diagnostic, got: %v", err)
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
	if readCalls != 1 {
		t.Fatalf("expected repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode_index(")
	if decodeCalls != 0 {
		t.Fatalf("expected repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateLLVMIRUsesCanonicalVariantSparseReadCacheForFrozenRepeatedCommonFieldReads(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_frozen_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 1 {
		t.Fatalf("expected canonical frozen repeated packed common-field reads to reuse one variant-sparse payload read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen repeated packed common-field reads to avoid eager variant-sparse decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") || strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected canonical frozen repeated packed common-field reads to stay on variant-sparse helpers, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesCanonicalVariantSparseReadCacheAcrossMatchedAccessorPatterns(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Wrap(child: Expr)

def fold_child_common_frozen() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	child: Expr = new[store] Expr.Int(span: 5, value: 7)
	node: Expr = new[store] Expr.Wrap(span: 9, child: child)
	frozen: Expr.Store[Frozen] = freeze(move store)
	open node in frozen as Expr.Wrap(child: child_alias):
		return child_alias.span + child_alias.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_field_cache_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(") {
		t.Fatalf("expected canonical frozen packed match to use variant-sparse tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 2 {
		t.Fatalf("expected canonical frozen matched accessor pattern to use two total variant-sparse word reads (child payload + cached repeated child common field), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen matched accessor pattern to avoid eager variant-sparse decode, got:\n%s", output)
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
	if readCalls != 1 {
		t.Fatalf("expected direct repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
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
	if readCalls != 1 {
		t.Fatalf("expected helper-wrapped repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
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
	if readCalls != 1 {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed repeated frozen packed common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSideWordReadForFrozenHelperWrappedRepeatedSideTableCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: mutable Expr

@borrows_return_field(node, node)
extern wrap_node(node: Expr) -> Box

def fold_side_common_frozen_wrapped_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	boxed: Box = wrap_node(node)
	return boxed.node.span + boxed.node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_wrapped_side_field_cache_direct_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 1 {
		t.Fatalf("expected helper-wrapped repeated frozen side-tabled common-field reads in index-soa mode to reuse one ctx_packed_store_read_side_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected helper-wrapped repeated frozen side-tabled common-field reads in index-soa mode to bypass inline index-word helpers, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-wrapped repeated frozen side-tabled common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesSideWordReadForHelperIndexedFrozenRepeatedSideTableCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		@storage(side_table)
		span: int
	Lit(value: int)

repr(c) struct Box:
	node: Expr

repr(c) struct BoxHolder:
	items: array[Box, 1]

@borrows_return_field(items[0].node, node)
extern wrap_indexed_node(node: Expr) -> BoxHolder

def fold_side_common_frozen_helper_indexed_direct() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	wrapped: BoxHolder = wrap_indexed_node(node)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return wrapped.items[0u].node.span + wrapped.items[0u].node.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_helper_indexed_side_field_cache_direct_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_side_word(")
	if readCalls != 1 {
		t.Fatalf("expected helper-indexed repeated frozen side-tabled common-field reads in index-soa mode to reuse one ctx_packed_store_read_side_word call, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected helper-indexed repeated frozen side-tabled common-field reads in index-soa mode to bypass inline index-word helpers, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected helper-indexed repeated frozen side-tabled common-field reads in index-soa mode to avoid eager decode, got:\n%s", output)
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
	if readCalls != 1 {
		t.Fatalf("expected nested rebased helper-indexed repeated frozen packed common-field reads outside an explicit frozen checkpoint in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
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
	if readCalls != 1 {
		t.Fatalf("expected nested wildcard rebased helper-indexed repeated frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
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

func TestGenerateLLVMIRLowersDenseNodeKeysAcrossPackedABIs(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Add(left: Expr, right: Expr)

def read(owner: Arena) -> int:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		_ = new Expr.Add(span: 5, left: left, right: right)

	frozen: Expr.Store[Frozen] = freeze(move store)
	node: Expr = frozen[2u]
	key: NodeKey[Expr] = dense_key(node, frozen)
	again: Expr = frozen[key]
	return again.span
`
	result := parseAndAnalyzeBackendTest(t, "backend_dense_node_key_abi.llcontext", src)

	for _, abi := range []packedEnumABIMode{packedEnumABIRowHandle, packedEnumABIWordHandle, packedEnumABIIndexSOA} {
		t.Run(packedABITestName(abi), func(t *testing.T) {
			output, err := generateLLVMIRWithPackedABIForTest(result, abi)
			if err != nil {
				t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
			}

			switch abi {
			case packedEnumABIIndexSOA:
				indexCalls := strings.Count(output, "call i32 @ctx_packed_store_index_at(")
				if indexCalls != 1 {
					t.Fatalf("expected canonical dense-key lowering to reuse the original numeric frozen lookup and avoid an extra index_at for frozen[key], got %d calls:\n%s", indexCalls, output)
				}
				for _, bad := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
					if strings.Contains(output, bad) {
						t.Fatalf("expected canonical dense-key lowering to avoid %q, got:\n%s", bad, output)
					}
				}
			case packedEnumABIRowHandle:
				for _, check := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index("} {
					if !strings.Contains(output, check) {
						t.Fatalf("expected row-handle dense-key lowering to contain %q, got:\n%s", check, output)
					}
				}
				if strings.Contains(output, "call ptr @ctx_packed_store_decode(") {
					t.Fatalf("expected row-handle dense-key lowering to avoid full word-handle decode helper, got:\n%s", output)
				}
			case packedEnumABIWordHandle:
				for _, check := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
					if !strings.Contains(output, check) {
						t.Fatalf("expected word-handle dense-key lowering to contain %q, got:\n%s", check, output)
					}
				}
			}
		})
	}
}

func TestGenerateLLVMIRLowersDenseNodeKeysFromHiddenFrozenStoreFieldRootsAcrossPackedABIs(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Add(left: Expr, right: Expr)

struct FrozenBox:
	store: Expr.Store[Frozen]

def make_box(owner: Arena) -> FrozenBox:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		_ = new Expr.Add(span: 5, left: left, right: right)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return FrozenBox(frozen)

def read(owner: Arena) -> int:
	box: FrozenBox = make_box(owner)
	node: Expr = box.store[2u]
	key: NodeKey[Expr] = dense_key(node, box.store)
	again: Expr = box.store[key]
	_ = again
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_dense_node_key_hidden_field_root_abi.llcontext", src)

	for _, abi := range []packedEnumABIMode{packedEnumABIRowHandle, packedEnumABIWordHandle, packedEnumABIIndexSOA} {
		t.Run(packedABITestName(abi), func(t *testing.T) {
			output, err := generateLLVMIRWithPackedABIForTest(result, abi)
			if err != nil {
				t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
			}

			switch abi {
			case packedEnumABIIndexSOA:
				indexCalls := strings.Count(output, "call i32 @ctx_packed_store_index_at(")
				if indexCalls != 1 {
					t.Fatalf("expected hidden-root dense-key lowering to reuse the original numeric frozen lookup and avoid an extra index_at for box.store[key], got %d calls:\n%s", indexCalls, output)
				}
				for _, bad := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
					if strings.Contains(output, bad) {
						t.Fatalf("expected hidden-root dense-key lowering to avoid %q, got:\n%s", bad, output)
					}
				}
			case packedEnumABIRowHandle:
				for _, check := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index("} {
					if !strings.Contains(output, check) {
						t.Fatalf("expected hidden-root row-handle dense-key lowering to contain %q, got:\n%s", check, output)
					}
				}
				if strings.Contains(output, "call ptr @ctx_packed_store_decode(") {
					t.Fatalf("expected hidden-root row-handle dense-key lowering to avoid full word-handle decode helper, got:\n%s", output)
				}
			case packedEnumABIWordHandle:
				for _, check := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
					if !strings.Contains(output, check) {
						t.Fatalf("expected hidden-root word-handle dense-key lowering to contain %q, got:\n%s", check, output)
					}
				}
			}
		})
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
	if readCalls != 1 {
		t.Fatalf("expected repeated mixed frozen packed common-field reads in index-soa mode to reuse one ctx_packed_store_read_index_word call, got %d helper calls:\n%s", readCalls, output)
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
	if readCalls != 1 {
		t.Fatalf("expected frozen payloadless packed match with matched-value field reads in index-soa mode to reuse one direct index word read, got %d helper calls:\n%s", readCalls, output)
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
	if strings.Contains(output, "call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(") {
		t.Fatalf("expected payloadless packed match to inline constructor handles without ctx_packed_store_alloc_fixed_tagged_result, got:\n%s", output)
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 0 {
		t.Fatalf("expected no full decode for payloadless packed match after constructor alloc returns a writable row directly, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateLLVMIRUsesInlineWordHandleFastPathForSmallScalarPayloadMatches(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: i32)
	End

def fold() -> i32:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(value: 5i32)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return match node in frozen:
		Expr.Lit(value):
			value
		Expr.End:
			0i32
`
	result := parseAndAnalyzeBackendTest(t, "backend_inline_word_handle_small_scalar_match.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if strings.Contains(output, "call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(") {
		t.Fatalf("expected inline small-scalar packed constructor to avoid ctx_packed_store_alloc_fixed_tagged_result, got:\n%s", output)
	}
	if !strings.Contains(output, "call i32 @ctx_packed_store_read_tag(") {
		t.Fatalf("expected inline small-scalar packed match to keep ctx_packed_store_read_tag for dispatch, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode(") {
		t.Fatalf("expected inline small-scalar packed match to avoid eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected inline small-scalar packed match to avoid ctx_packed_store_read_word for payload extraction, got:\n%s", output)
	}
	if !strings.Contains(output, "lshr i64") || !strings.Contains(output, "trunc i64") {
		t.Fatalf("expected inline small-scalar packed match to extract payload bits directly from the handle, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersInlinePackedViewsWithoutDecode(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: i32)

def score(view_value: packedview[Expr.Lit]) -> i32:
	return view_value.value

def fold_view_value() -> i32:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(value: 7i32)
		view node in store as Expr.Lit(lit_view):
			kept: packedview[Expr.Lit] = lit_view
			return score(kept)
	return 0i32
`
	result := parseAndAnalyzeBackendTest(t, "backend_inline_word_handle_packedview.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if strings.Contains(output, "call ptr @ctx_packed_store_decode(") {
		t.Fatalf("expected inline packedview lowering to avoid ctx_packed_store_decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected inline packedview lowering to avoid ctx_packed_store_read_word, got:\n%s", output)
	}
	if !strings.Contains(output, "%PackedView__Expr__Lit = type { i64, %Expr__Store }") {
		t.Fatalf("expected inline packedview lowering to keep the packedview carrier ABI, got:\n%s", output)
	}
	if !strings.Contains(output, "lshr i64") || !strings.Contains(output, "trunc i64") {
		t.Fatalf("expected inline packedview lowering to extract payload bits directly from the handle, got:\n%s", output)
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

	if !strings.Contains(output, "call %PackedStoreAllocResult @ctx_packed_store_alloc_fixed_tagged_result(") {
		t.Fatalf("expected optimized word-handle IR to keep ctx_packed_store_alloc_fixed_tagged_result as an out-of-line helper call, got:\n%s", output)
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

func TestGenerateLLVMIRLowersPackedViewParamOpenWithoutExplicitStoreInWordHandleABI(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def read(view_node: packedview[Expr.Lit]) -> int:
	open view_node as Expr.Lit(value: value):
		return value + view_node.value + view_node.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packedview_param_open_inferred_store_word_handle.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"%PackedView__Expr__Lit = type { i64, %Expr__Store }",
		"packedview.store.extract",
		"call i64 @ctx_packed_store_read_word(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersFrozenIndexedAliasOpenWithoutExplicitStoreInWordHandleABI(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def read(store: Expr.Store[Frozen], index: usize) -> int:
	node: Expr = store[index]
	alias: Expr = node
	open alias as Expr.Lit(value: value):
		return value + alias.span
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_frozen_index_alias_open_inferred_store_word_handle.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"call i64 @ctx_packed_store_word_handle_at(",
		"call i64 @ctx_packed_store_read_word(",
		"packed.tag.store.state",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersHiddenStoreFieldIndexedAliasOpenWithoutExplicitStoreInWordHandleABI(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)

struct Box:
	store: Expr.Store[Local]

def make_box(owner: Arena) -> Box:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		_ = new Expr.Lit(value: 7)
	return Box(move store)

def read(owner: Arena) -> int:
	box: Box = make_box(owner)
	node: Expr = box.store[0u]
	alias: Expr = node
	open alias as Expr.Lit(value: value):
		return value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_hidden_store_field_index_alias_open_inferred_store_word_handle.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"define %Box @make_box",
		"call i64 @ctx_packed_store_word_handle_at(",
		"call i64 @ctx_packed_store_read_word(",
	} {
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

func TestGenerateLLVMIRUsesSingleDecodeForFrozenPackedIfVariantView(t *testing.T) {
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
	if node in frozen as Expr.Lit(value: value):
		return node.value + node.value + node.span + value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_view_stmt.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected frozen packed if variant view to use a single eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected frozen packed if variant view field reads to reuse the decoded row instead of ctx_packed_store_read_word, got:\n%s", output)
	}
	if strings.Contains(output, "@llvm.trap") {
		t.Fatalf("expected packed if variant view to preserve ordinary if fallthrough instead of trapping on mismatch, got:\n%s", output)
	}
	for _, check := range []string{"packed.decode.store.arena", "packed.decode.store.state"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForFrozenPackedIfVariantViewInIndexSOA(t *testing.T) {
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
	if node in frozen as Expr.Lit(value: value):
		return node.value + node.span + value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_view_stmt_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 2 {
		t.Fatalf("expected frozen packed if variant view in index-soa mode to reuse one repeated payload index word read plus one common-field read, got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed if variant view in index-soa mode to avoid eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "@llvm.trap") {
		t.Fatalf("expected packed if variant view in index-soa mode to preserve ordinary if fallthrough instead of trapping on mismatch, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersFirstClassPackedViewValuesInWordHandleABI(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def echo(view_value: packedview[Expr.Lit]) -> packedview[Expr.Lit]:
	return view_value

def score(view_value: packedview[Expr.Lit]) -> int:
	return view_value.value + view_value.span

def fold_view_value() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		view node in store as Expr.Lit(lit_view):
			kept: packedview[Expr.Lit] = echo(lit_view)
			return score(kept) + echo(lit_view).value
	return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_first_class_packedview_word_handle.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"%PackedView__Expr__Lit = type { i64, %Expr__Store }",
		"define %PackedView__Expr__Lit @echo(%PackedView__Expr__Lit",
		"define i64 @score(%PackedView__Expr__Lit",
		"extractvalue %PackedView__Expr__Lit",
		"call i64 @ctx_packed_store_read_word(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 1 {
		t.Fatalf("expected first-class packedview regression to perform one eager decode for the originating view and then reuse packedview carriers afterwards, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateLLVMIRLowersImplicitPackedViewScrutineeRefinementInWordHandleABI(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

def echo(view_value: packedview[Expr.Lit]) -> packedview[Expr.Lit]:
	return view_value

def score(view_value: packedview[Expr.Lit]) -> int:
	return view_value.value + view_value.span

def fold_view_value() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		node: Expr = new Expr.Lit(span: 7, value: 5)
		if node in store as Expr.Lit(value: value):
			kept: packedview[Expr.Lit] = echo(node)
			return score(kept) + echo(node).value + value
		return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_implicit_packedview_scrutinee_refinement_word_handle.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIWordHandle)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	for _, check := range []string{
		"%PackedView__Expr__Lit = type { i64, %Expr__Store }",
		"define %PackedView__Expr__Lit @echo(%PackedView__Expr__Lit",
		"define i64 @score(%PackedView__Expr__Lit",
		"extractvalue %PackedView__Expr__Lit",
		"call i64 @ctx_packed_store_read_word(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	decodeCalls := strings.Count(output, "call ptr @ctx_packed_store_decode(")
	if decodeCalls != 0 {
		t.Fatalf("expected implicit packedview scrutinee refinement to reuse packedview carriers without eager decode, got %d decode calls:\n%s", decodeCalls, output)
	}
}

func TestGenerateLLVMIRUsesIndexTailViewFastPathForFrozenPackedIfVariantViewInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(count: usize, items: tail int)

def fold_view() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	source_items: array[int, 3] = [1, 2, 3]
	node: Expr = new[store] Expr.Block(count: 3u, items: source_items[0u:3u])
	frozen: Expr.Store[Frozen] = freeze(move store)
	if node in frozen as Expr.Block:
		return node.items[0u] + node.items[2u]
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
			t.Fatalf("expected frozen packed tail if variant view in index-soa mode to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen packed tail if variant view in index-soa mode to avoid eager decode, got:\n%s", output)
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
			total <- total + value + node.value + node.span
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
				return value + node.value
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
			total <- total + value + node.value + node.span
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
		"call %PackedStoreIndexAllocResult @ctx_packed_store_alloc_fixed_tagged_index_result(",
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

func TestGenerateLLVMIRUsesIndexTailViewFastPathForNonFinalTailPayloadInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Block(items: tail int, count: usize)

def fold() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	in store:
		source_items: array[int, 3] = [1, 2, 3]
		node: Expr = new Expr.Block(items: source_items[0u:3u], count: 3u)
		return match node:
			Expr.Block(items: items, count: count):
				count.int() + items[0u] + items[2u]
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_tail_payload_non_final_index_soa.llcontext", src)
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
		t.Fatalf("expected index-soa non-final tail payload match to avoid full decode on the fast path, got:\n%s", output)
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
