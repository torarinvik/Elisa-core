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
	var explicitABI PackedEnumABI
	switch abi {
	case packedEnumABIIndexSOA:
		explicitABI = PackedEnumABIIndexSOA
	case packedEnumABIVariantSparse:
		explicitABI = PackedEnumABIVariantSparse
	default:
		return "", fmt.Errorf("unsupported packed enum ABI mode %d", abi)
	}
	g.packedProfile = mustExplicitPackedLoweringProfile(explicitABI)
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
	if result.PackedLowering.PublicationReadonlyGateStoreState != "Frozen" {
		t.Fatalf("expected frozen publication gate metadata, got %q", result.PackedLowering.PublicationReadonlyGateStoreState)
	}
	if !result.PackedLowering.OnePackedEnumOneHandleInvariant {
		t.Fatalf("expected one-packed-enum/one-handle invariant metadata to be recorded")
	}
}

func TestGenerateLLVMIRExplicitPackedModeKeepsCanonicalPackedLoweringMetadata(t *testing.T) {
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
	profile, err := ExplicitPackedLoweringProfile(PackedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("ExplicitPackedLoweringProfile returned error: %v", err)
	}
	if _, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel0, profile); err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
	}
	if result.PackedLowering.Contract != string(PackedLoweringContractCanonicalCompilerGraph) {
		t.Fatalf("expected explicit packed mode to keep canonical metadata contract, got %q", result.PackedLowering.Contract)
	}
	if result.PackedLowering.CanonicalPackedLowering != string(PackedEnumABIVariantSparse) {
		t.Fatalf("expected explicit packed mode to preserve canonical metadata baseline %q, got %q", PackedEnumABIVariantSparse, result.PackedLowering.CanonicalPackedLowering)
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
		t.Fatalf("expected canonical packed lowering to avoid legacy payload-read helpers, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected canonical packed lowering to avoid legacy index-soa payload reads, got:\n%s", output)
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
		t.Fatalf("expected canonical frozen packed payload match to avoid legacy payload-read helpers, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected canonical frozen packed payload match to avoid legacy index-soa payload reads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesIndexReadHelpersForNestedFrozenPackedPayloadMatchInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	Wrap(inner: Expr)

def fold_frozen(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Wrap(inner: Expr.Lit(value: value)):
			value
		Expr.Wrap(inner: inner):
			fold_frozen(inner, frozen)
		Expr.Lit(value: value):
			value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_payload_decode_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	tagCalls := strings.Count(output, "call i32 @ctx_packed_store_read_index_tag(")
	if tagCalls < 2 {
		t.Fatalf("expected nested frozen packed payload match in index-soa mode to use direct tag reads for both levels, got %d helper calls:\n%s", tagCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls < 2 {
		t.Fatalf("expected nested frozen packed payload match in index-soa mode to use direct payload reads for both levels, got %d helper calls:\n%s", readCalls, output)
	}
	if !strings.Contains(output, "trunc i64") {
		t.Fatalf("expected nested frozen packed payload match in index-soa mode to truncate direct word reads back to packed enum handles, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected nested frozen packed payload match in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesCanonicalIndexReadHelpersForNestedFrozenPackedPayloadMatchByDefault(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	Wrap(inner: Expr)

def fold_frozen(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Wrap(inner: Expr.Lit(value: value)):
			value
		Expr.Wrap(inner: inner):
			fold_frozen(inner, frozen)
		Expr.Lit(value: value):
			value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_payload_decode_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	tagCalls := strings.Count(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(")
	if tagCalls < 2 {
		t.Fatalf("expected canonical nested frozen packed payload match to use direct variant-sparse tag reads for both levels, got %d helper calls:\n%s", tagCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls < 2 {
		t.Fatalf("expected canonical nested frozen packed payload match to use direct variant-sparse payload reads for both levels, got %d helper calls:\n%s", readCalls, output)
	}
	if !strings.Contains(output, "trunc i64") {
		t.Fatalf("expected canonical nested frozen packed payload match to truncate direct word reads back to packed enum handles, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical nested frozen packed payload match to avoid eager decode, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected canonical nested frozen packed payload match to avoid legacy index-soa payload reads, got:\n%s", output)
	}
}

func TestGenerateLLVMIRShiftsAdjacentFrozenPackedHandlePayloadsInIndexSOA(t *testing.T) {
	src := `@packed_profile(retained_reads)
packed enum TypeExpr:
	Var(symbol: i64)

@packed_profile(retained_reads)
packed enum Clause:
	Terminal(body: Expr)

@packed_profile(retained_reads)
packed enum Expr:
	Literal(value: i64)
	Match(scrutinee: Expr, clauses: Clause, ty: TypeExpr)

def score_type(node: TypeExpr, types: TypeExpr.Store[Frozen]) -> i64:
	return match node in types:
		TypeExpr.Var(symbol: symbol):
			symbol

def score_clause(node: Clause, clauses: Clause.Store[Frozen], exprs: Expr.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	return match node in clauses:
		Clause.Terminal(body: body):
			score_expr(body, exprs, clauses, types) + 1

def score_expr(node: Expr, exprs: Expr.Store[Frozen], clauses: Clause.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	return match node in exprs:
		Expr.Literal(value: value):
			value
		Expr.Match(scrutinee: scrutinee, clauses: match_clauses, ty: ty):
			score_expr(scrutinee, exprs, clauses, types) + score_clause(match_clauses, clauses, exprs, types) + score_type(ty, types)

def fold_frozen() -> i64:
	region scratch(512u)
	types: TypeExpr.Store[Local] = TypeExpr.Store(scratch)
	exprs: Expr.Store[Local] = Expr.Store(scratch)
	clauses: Clause.Store[Local] = Clause.Store(scratch)
	ty: TypeExpr = new[types] TypeExpr.Var(symbol: 7)
	lit: Expr = new[exprs] Expr.Literal(value: 9)
	clause: Clause = new[clauses] Clause.Terminal(body: lit)
	node: Expr = new[exprs] Expr.Match(scrutinee: lit, clauses: clause, ty: ty)
	frozen_types: TypeExpr.Store[Frozen] = freeze(move types)
	frozen_exprs: Expr.Store[Frozen] = freeze(move exprs)
	frozen_clauses: Clause.Store[Frozen] = freeze(move clauses)
	return score_expr(node, frozen_exprs, frozen_clauses, frozen_types)
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_adjacent_frozen_handle_payload_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected adjacent frozen packed handle payload match in index-soa mode to use direct tag reads, got:\n%s", output)
	}
	if strings.Count(output, "call i64 @ctx_packed_store_read_index_word(") < 2 {
		t.Fatalf("expected adjacent frozen packed handle payload match in index-soa mode to use direct payload reads, got:\n%s", output)
	}
	if !strings.Contains(output, "lshr i64") {
		t.Fatalf("expected adjacent frozen packed handle payload match in index-soa mode to shift packed word reads before truncation, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected adjacent frozen packed handle payload match in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRShiftsAdjacentFrozenPackedHandlePayloadsByDefault(t *testing.T) {
	src := `@packed_profile(retained_reads)
packed enum TypeExpr:
	Var(symbol: i64)

@packed_profile(retained_reads)
packed enum Clause:
	Terminal(body: Expr)

@packed_profile(retained_reads)
packed enum Expr:
	Literal(value: i64)
	Match(scrutinee: Expr, clauses: Clause, ty: TypeExpr)

def score_type(node: TypeExpr, types: TypeExpr.Store[Frozen]) -> i64:
	return match node in types:
		TypeExpr.Var(symbol: symbol):
			symbol

def score_clause(node: Clause, clauses: Clause.Store[Frozen], exprs: Expr.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	return match node in clauses:
		Clause.Terminal(body: body):
			score_expr(body, exprs, clauses, types) + 1

def score_expr(node: Expr, exprs: Expr.Store[Frozen], clauses: Clause.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	return match node in exprs:
		Expr.Literal(value: value):
			value
		Expr.Match(scrutinee: scrutinee, clauses: match_clauses, ty: ty):
			score_expr(scrutinee, exprs, clauses, types) + score_clause(match_clauses, clauses, exprs, types) + score_type(ty, types)

def fold_frozen() -> i64:
	region scratch(512u)
	types: TypeExpr.Store[Local] = TypeExpr.Store(scratch)
	exprs: Expr.Store[Local] = Expr.Store(scratch)
	clauses: Clause.Store[Local] = Clause.Store(scratch)
	ty: TypeExpr = new[types] TypeExpr.Var(symbol: 7)
	lit: Expr = new[exprs] Expr.Literal(value: 9)
	clause: Clause = new[clauses] Clause.Terminal(body: lit)
	node: Expr = new[exprs] Expr.Match(scrutinee: lit, clauses: clause, ty: ty)
	frozen_types: TypeExpr.Store[Frozen] = freeze(move types)
	frozen_exprs: Expr.Store[Frozen] = freeze(move exprs)
	frozen_clauses: Clause.Store[Frozen] = freeze(move clauses)
	return score_expr(node, frozen_exprs, frozen_clauses, frozen_types)
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_adjacent_frozen_handle_payload_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	hasIndexTags := strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(")
	hasVariantSparseTags := strings.Contains(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(")
	if !hasIndexTags && !hasVariantSparseTags {
		t.Fatalf("expected adjacent frozen packed handle payload match to use the default direct tag reads, got:\n%s", output)
	}
	indexWordReads := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	variantSparseWordReads := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if indexWordReads < 2 && variantSparseWordReads < 2 {
		t.Fatalf("expected adjacent frozen packed handle payload match to use the default direct payload reads, got:\n%s", output)
	}
	if !strings.Contains(output, "lshr i64") {
		t.Fatalf("expected adjacent frozen packed handle payload match to shift packed word reads before truncation, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") || strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected adjacent frozen packed handle payload match to avoid eager decode, got:\n%s", output)
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

func TestGenerateLLVMIRUsesCanonicalDirectReadsForFrozenMatchedValueFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Lit(value):
			node.span + node.span + value
		Expr.End:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_value_field_cache_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(") {
		t.Fatalf("expected canonical frozen matched-value field access to use variant-sparse tag reads, got:\n%s", output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 2 {
		t.Fatalf("expected canonical frozen matched-value field access to use two total variant-sparse payload reads (cached repeated common field + payload value), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen matched-value field access to avoid eager variant-sparse decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRPreloadsCanonicalMatchedValueCommonFieldsAcrossArms(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
		weight: int
	Lit(value: int)
	End
	Stop

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Lit(value):
			node.span + node.weight + value
		Expr.End:
			node.span + node.weight
		Expr.Stop:
			node.span + node.weight
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_value_common_preload_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls != 3 {
		t.Fatalf("expected canonical frozen matched-value common-field preloading to reduce total variant-sparse word reads across arms to three (two common preloads + one payload), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen matched-value common-field preloading to avoid eager variant-sparse decode, got:\n%s", output)
	}
	if !strings.Contains(output, "switch i32") {
		t.Fatalf("expected canonical frozen matched-value common-field preloading case to lower through a switch, got:\n%s", output)
	}
}

func TestGenerateLLVMIRPreloadsIndexSOAMatchedValueCommonFieldsAcrossArms(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
		weight: int
	Lit(value: int)
	End
	Stop

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Lit(value):
			node.span + node.weight + value
		Expr.End:
			node.span + node.weight
		Expr.Stop:
			node.span + node.weight
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_matched_value_common_preload_index_soa.llcontext", src)
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}

	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_index_word(")
	if readCalls != 3 {
		t.Fatalf("expected frozen index-soa matched-value common-field preloading to reduce total index word reads across arms to three (two common preloads + one payload), got %d helper calls:\n%s", readCalls, output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected frozen index-soa matched-value common-field preloading to avoid eager decode, got:\n%s", output)
	}
	if !strings.Contains(output, "switch i32") {
		t.Fatalf("expected frozen index-soa matched-value common-field preloading case to lower through a switch, got:\n%s", output)
	}
}

func TestGenerateLLVMIRUsesCanonicalDirectReadsForNestedMatchedValueFieldAccess(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Wrap(child: Expr)

def fold_child(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Lit(value):
			node.span + node.span + value
		Expr.Wrap(child: inner):
			fold_child(inner, frozen)

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Wrap(child: child):
			fold_child(child, frozen)
		Expr.Lit(value):
			node.span + value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_nested_matched_value_field_cache_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical nested matched-value field access to avoid eager variant-sparse decode, got:\n%s", output)
	}
	tagCalls := strings.Count(output, "call i32 @ctx_packed_store_read_variant_sparse_tag(")
	if tagCalls < 2 {
		t.Fatalf("expected canonical nested matched-value field access to use direct tag reads for both levels, got %d helper calls:\n%s", tagCalls, output)
	}
	readCalls := strings.Count(output, "call i64 @ctx_packed_store_read_variant_sparse_word(")
	if readCalls < 3 {
		t.Fatalf("expected canonical nested matched-value field access to stay on variant-sparse payload reads, got %d helper calls:\n%s", readCalls, output)
	}
}

func TestGenerateLLVMIRUsesSwitchForCanonicalFrozenUniqueVariantMatch(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	Wrap(child: Expr)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Lit(value):
			node.span + value
		Expr.Wrap(child):
			child.span
		Expr.End:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_match_switch_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if !strings.Contains(output, "switch i32") {
		t.Fatalf("expected canonical frozen unique-variant match to lower through an LLVM switch, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_variant_sparse(") {
		t.Fatalf("expected canonical frozen unique-variant match to avoid eager variant-sparse decode, got:\n%s", output)
	}
}

func TestGenerateLLVMIRDoesNotMaterializeUndefFailPathForExhaustiveCanonicalMatchExpr(t *testing.T) {
	src := `packed enum Expr:
	Lit(value: int)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Lit(value):
			value
		Expr.End:
			0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_match_expr_exhaustive_no_undef_default.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}

	if strings.Contains(output, "phi i64 [ undef") {
		t.Fatalf("expected exhaustive canonical packed match expr to avoid materializing an undef fail path, got:\n%s", output)
	}
	if !strings.Contains(output, "unreachable") {
		t.Fatalf("expected exhaustive canonical packed match expr to terminate the impossible fail path with unreachable, got:\n%s", output)
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

func TestGenerateLLVMIRUsesIndexWordReadForFrozenHelperWrappedRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
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

func TestGenerateLLVMIRUsesIndexWordReadForHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: Expr

struct BoxHolder:
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

struct Box:
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

struct Box:
	node: Expr

struct BoxHolder:
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

func TestGenerateLLVMIRAvoidsDecodeForFrozenHelperWrappedCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
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

func TestGenerateLLVMIRAvoidsDecodeForFrozenHelperIndexedCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

struct BoxHolder:
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

func TestGenerateLLVMIRUsesIndexWordReadForNestedRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
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

func TestGenerateLLVMIRUsesIndexWordReadForNestedWildcardRebasedHelperIndexedFrozenRepeatedCommonFieldReadsOutsideCheckpoint(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
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

func TestGenerateLLVMIRAvoidsDecodeForNestedWildcardRebasedHelperIndexedFrozenCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

struct Meta:
	items: view[Box]

struct Wrapper:
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

func TestGenerateLLVMIRAvoidsDecodeForNestedRebasedHelperIndexedFrozenCommonFieldReadsAfterFieldAssignmentInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)

struct Box:
	node: mutable Expr

struct Meta:
	items: view[Box]

struct Wrapper:
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

func TestGenerateLLVMIRLowersDenseNodeKeysInIndexSOA(t *testing.T) {
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
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}
	indexCalls := strings.Count(output, "call i32 @ctx_packed_store_index_at(")
	if indexCalls != 1 {
		t.Fatalf("expected dense-key lowering to reuse the original numeric frozen lookup and avoid an extra index_at for frozen[key], got %d calls:\n%s", indexCalls, output)
	}
	for _, bad := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected dense-key lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}

func TestGenerateLLVMIRLowersDenseNodeKeysFromHiddenFrozenStoreFieldRootsInIndexSOA(t *testing.T) {
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
	output, err := generateLLVMIRWithPackedABIForTest(result, packedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("generateLLVMIRWithPackedABIForTest returned error: %v", err)
	}
	indexCalls := strings.Count(output, "call i32 @ctx_packed_store_index_at(")
	if indexCalls != 1 {
		t.Fatalf("expected hidden-root dense-key lowering to reuse the original numeric frozen lookup and avoid an extra index_at for box.store[key], got %d calls:\n%s", indexCalls, output)
	}
	for _, bad := range []string{"call i32 @ctx_packed_store_encode_index(", "call ptr @ctx_packed_store_decode_index(", "call i64 @ctx_packed_store_encode(", "call ptr @ctx_packed_store_decode("} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected hidden-root dense-key lowering to avoid %q, got:\n%s", bad, output)
		}
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

func TestGenerateLLVMIRUsesIndexReadHelpersForHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

struct Box:
	node: Expr

struct BoxHolder:
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

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
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

func TestGenerateLLVMIRUsesIndexReadHelpersForNestedWildcardRebasedHelperIndexedFrozenMatchedPayloadRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Int(value: int)
	Hold(value: any i32&)
	Wrap(child: Expr)

struct Box:
	node: Expr

struct Meta:
	items: view[Box]

struct Wrapper:
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

func TestGenerateOptimizedLLVMIRUsesDirectPrefixColumnLoadsForFrozenRepeatedCommonFieldReadsInIndexSOA(t *testing.T) {
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
	result := parseAndAnalyzeBackendTest(t, "backend_packed_field_cache_frozen_index_soa_opt.llcontext", src)
	profile, err := ExplicitPackedLoweringProfile(PackedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("ExplicitPackedLoweringProfile returned error: %v", err)
	}
	output, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel3, profile)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
	}

	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected optimized frozen repeated packed common-field reads in index-soa mode to avoid ctx_packed_store_read_index_word, got:\n%s", output)
	}
	if strings.Contains(output, "call fastcc i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected optimized frozen repeated packed common-field reads in index-soa mode to avoid ctx_packed_store_read_word fallback calls, got:\n%s", output)
	}
}

func TestGenerateOptimizedLLVMIRUsesDirectDenseMetadataLoadsForFrozenPackedMatchInIndexSOA(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: int
	Lit(value: int)
	End

def fold_match_frozen() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Lit(span: 7, value: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	in frozen:
		return match node:
			Expr.Lit(value):
				value + node.span
			Expr.End:
				0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_match_frozen_index_soa_opt.llcontext", src)
	profile, err := ExplicitPackedLoweringProfile(PackedEnumABIIndexSOA)
	if err != nil {
		t.Fatalf("ExplicitPackedLoweringProfile returned error: %v", err)
	}
	output, err := GenerateLLVMIRWithOptAndPackedLoweringProfile(result, OptimizationLevel3, profile)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOptAndPackedLoweringProfile returned error: %v", err)
	}

	if strings.Contains(output, "call i32 @ctx_packed_store_read_index_tag(") {
		t.Fatalf("expected optimized frozen packed match in index-soa mode to avoid ctx_packed_store_read_index_tag, got:\n%s", output)
	}
	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected optimized frozen packed match in index-soa mode to avoid ctx_packed_store_read_index_word, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected optimized frozen packed match in index-soa mode to avoid eager decode, got:\n%s", output)
	}
}

func TestGenerateOptimizedLLVMIRUsesDirectAllWordsPrefixLoadsForRetainedReadsFrozenWidePayloadMatch(t *testing.T) {
	src := `@packed_profile(retained_reads)
packed enum Expr:
	common:
		span: int
		cost: int
	Leaf(value: int)
	Wide(first: int, second: int, third: int)
	End

def fold(node: Expr, frozen: Expr.Store[Frozen]) -> int:
	return match node in frozen:
		Expr.Wide(first: first, second: second, third: third):
			node.span + node.cost + first + second + third
		Expr.Leaf(value: value):
			node.span + node.cost + value
		Expr.End:
			0

def fold_export() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	node: Expr = new[store] Expr.Wide(span: 7, cost: 11, first: 2, second: 3, third: 5)
	frozen: Expr.Store[Frozen] = freeze(move store)
	return fold(node, frozen)
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_retained_reads_wide_payload_opt.llcontext", src)
	output, err := GenerateLLVMIRWithOpt(result, OptimizationLevel3)
	if err != nil {
		t.Fatalf("GenerateLLVMIRWithOpt returned error: %v", err)
	}

	if strings.Contains(output, "call i64 @ctx_packed_store_read_index_word(") {
		t.Fatalf("expected optimized retained-reads frozen wide payload match to avoid ctx_packed_store_read_index_word, got:\n%s", output)
	}
	if strings.Contains(output, "call fastcc i64 @ctx_packed_store_read_word(") {
		t.Fatalf("expected optimized retained-reads frozen wide payload match to avoid ctx_packed_store_read_word fallback calls, got:\n%s", output)
	}
	if strings.Contains(output, "call ptr @ctx_packed_store_decode_index(") {
		t.Fatalf("expected optimized retained-reads frozen wide payload match to avoid eager decode, got:\n%s", output)
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
