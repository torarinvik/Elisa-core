//go:build cgo

package backend

import (
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

const parallelForConcurrencyPrelude = `extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]
extern pool_shutdown(pool: ThreadPool&) -> void can[Pool.Shutdown]

def pool_submit1[A, R](pool: ThreadPool&, fn: func(A) -> R, arg: A) -> Task[R, Pending] can[Pool.Submit, Memory.Allocate, Abort.Panic]:
	task: Task[R, Pending] = zeroed
	return move task

def task_group_new() -> TaskGroup:
	group: TaskGroup = zeroed
	return move group

def task_group_add[R](group: TaskGroup&, task: Task[R, Pending]) -> void can[Memory.Allocate, Abort.Panic]:
	_ = move task

def task_group_wait_all(group: TaskGroup&) -> void can[Pool.WaitAll]:
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
		match node:
			Expr.Lit(value):
				out: int = value
				destroy scratch
				return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_metadata_default.elisa", src)
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
		match node:
			Expr.Lit(value):
				out: int = value
				destroy scratch
				return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_metadata_legacy.elisa", src)
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
		@storage(inline)
		span: int
	Both(left: int, right: int)
	End

def sum_pair() -> int:
	region scratch(256u)
	store: Pair.Store[Local] = Pair.Store(scratch)
	in store:
		node: Pair = new Pair.Both(span: 7, left: 2, right: 3)
		out: int = node.span
		destroy scratch
		return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_profile_build_heavy.elisa", src)
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
		match node:
			Pair.Both(left: left, right: right):
				out: int = left + right
				destroy scratch
				return out
			Pair.End:
				destroy scratch
				return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_payload_words_index_soa.elisa", src)
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
		match node:
			Pair.Both(left: left, right: right):
				out: int = left + right
				destroy scratch
				return out
			Pair.End:
				destroy scratch
				return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_payload_words_default.elisa", src)
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
	match node in frozen:
		Expr.Lit(value):
			out: int = value
			destroy scratch
			return out
		Expr.End:
			destroy scratch
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payload_decode_index_soa.elisa", src)
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
	match node in frozen:
		Expr.Lit(value):
			out: int = value
			destroy scratch
			return out
		Expr.End:
			destroy scratch
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_payload_decode_default.elisa", src)
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
	match node in frozen:
		Expr.Wrap(inner: Expr.Lit(value: value)):
			return value
		Expr.Wrap(inner: inner):
			return fold_frozen(inner, frozen)
		Expr.Lit(value: value):
			return value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_payload_decode_index_soa.elisa", src)
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
	match node in frozen:
		Expr.Wrap(inner: Expr.Lit(value: value)):
			return value
		Expr.Wrap(inner: inner):
			return fold_frozen(inner, frozen)
		Expr.Lit(value: value):
			return value
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_nested_payload_decode_default.elisa", src)
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
	match node in types:
		TypeExpr.Var(symbol: symbol):
			return symbol

def score_clause(node: Clause, clauses: Clause.Store[Frozen], exprs: Expr.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	match node in clauses:
		Clause.Terminal(body: body):
			return score_expr(body, exprs, clauses, types) + 1

def score_expr(node: Expr, exprs: Expr.Store[Frozen], clauses: Clause.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	match node in exprs:
		Expr.Literal(value: value):
			return value
		Expr.Match(scrutinee: scrutinee, clauses: match_clauses, ty: ty):
			return score_expr(scrutinee, exprs, clauses, types) + score_clause(match_clauses, clauses, exprs, types) + score_type(ty, types)

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
	out: i64 = score_expr(node, frozen_exprs, frozen_clauses, frozen_types)
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_adjacent_frozen_handle_payload_index_soa.elisa", src)
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
	match node in types:
		TypeExpr.Var(symbol: symbol):
			return symbol

def score_clause(node: Clause, clauses: Clause.Store[Frozen], exprs: Expr.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	match node in clauses:
		Clause.Terminal(body: body):
			return score_expr(body, exprs, clauses, types) + 1

def score_expr(node: Expr, exprs: Expr.Store[Frozen], clauses: Clause.Store[Frozen], types: TypeExpr.Store[Frozen]) -> i64:
	match node in exprs:
		Expr.Literal(value: value):
			return value
		Expr.Match(scrutinee: scrutinee, clauses: match_clauses, ty: ty):
			return score_expr(scrutinee, exprs, clauses, types) + score_clause(match_clauses, clauses, exprs, types) + score_type(ty, types)

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
	out: i64 = score_expr(node, frozen_exprs, frozen_clauses, frozen_types)
	destroy scratch
	return out
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_adjacent_frozen_handle_payload_default.elisa", src)
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
	Hold(value: i32&)
	End

def fold_frozen_mixed() -> int:
	region scratch(256u)
	store: Expr.Store[Local] = Expr.Store(scratch)
	local_ref: i32& @scratch = new[scratch] 7i32
	node: Expr = new[store] Expr.Hold(value: local_ref)
	frozen: Expr.Store[Frozen] = freeze(move store)
	match node in frozen:
		Expr.Hold(value):
			destroy scratch
			return 1
		Expr.End:
			destroy scratch
			return 0
`
	result := parseAndAnalyzeBackendTest(t, "backend_packed_frozen_mixed_payload_decode_index_soa.elisa", src)
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
