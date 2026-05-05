package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeAcceptsPackedStoreSliceTraversal(t *testing.T) {
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
	chunk: dview[Expr] = frozen[1:frozen.count]
	total: mutable int = 0
	index: mutable usize = 0
	while index < chunk.len:
		node: Expr = chunk[index]
		if node in frozen as Expr.Int(value: value):
			total <- total + value + node.span
		index <- index + 1
	return total
`
	result, errs := parseAndAnalyze(t, "packed_store_slice_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "walk", "int")
}
func TestAnalyzeAcceptsForLoopRangeForms(t *testing.T) {
	src := `def walk(limit: int) -> int:
	total: mutable int = 0
	for i in 0..<limit:
		total <- total + i
	for j in limit..>0:
		total <- total + j
	for k in 0..3:
		total <- total + k
	for m in 0..10..2:
		total <- total + m
	return total
`
	result, errs := parseAndAnalyze(t, "for_loop_ranges_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "walk", "int")
}
func TestAnalyzeAcceptsIterableForLoopValueDestructuring(t *testing.T) {
	src := `struct Pair:
	left: int
	right: int

def walk(items: array[Pair, 2]) -> int:
	total: mutable int = 0
	for Pair(left, right) in items:
		total <- total + left + right
	return total
`
	result, errs := parseAndAnalyze(t, "iterable_for_value_destructure_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "walk", "int")
}
func TestAnalyzeAcceptsIterableForLoopMutableRef(t *testing.T) {
	src := `struct Counter:
	value: mutable int

def bump() -> int:
	items: mutable array[Counter, 2] = [Counter(1), Counter(2)]
	for mutable ref item in items:
		item.value <- item.value + 1
	return items[0].value + items[1].value
`
	result, errs := parseAndAnalyze(t, "iterable_for_mutable_ref_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "bump", "int")
}
func TestAnalyzeAcceptsIterableForLoopOverDynamicString(t *testing.T) {
	src := `def checksum(text: cstr[row]) -> int:
	total: mutable int = 0
	for ch in text:
		total <- total + ch
	return total
`
	result, errs := parseAndAnalyze(t, "iterable_for_cstr_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "checksum", "int")
}
func TestAnalyzeAcceptsIterableForLoopOverChunksExactView(t *testing.T) {
	src := `def add_bias(value: i64, bias: i64) -> i64:
	return value + bias

def run(values: darray[i64, 4], bias: i64) -> i64:
	base: dview[i64] = values[0:4]
	chunks: ChunksExactView[i64] = chunks_exact(readonly(base), 2)
	total: mutable i64 = 0
	for chunk in chunks:
		total <- total + reduce_sum(chunk, add_bias, bias)
	return total
`
	result, errs := parseAndAnalyze(t, "iterable_for_chunks_exact_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeRejectsIterableForLoopRefOverChunksExactView(t *testing.T) {
	src := `def bad(values: darray[i32, 4]) -> void:
	base: dview[i32] = values[0:4]
	chunks: ChunksExactView[i32] = chunks_exact(readonly(base), 2)
	for ref chunk in chunks:
		_ = chunk
`
	_, errs := parseAndAnalyze(t, "iterable_for_chunks_exact_ref_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "for ref requires an addressable array-like iterable, got ChunksExactView[i32]") {
		t.Fatalf("expected chunks_exact ref-binder diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsForLoopZeroStep(t *testing.T) {
	src := `def bad() -> void:
	for i in 0..10..0:
		pass
`
	_, errs := parseAndAnalyze(t, "for_loop_zero_step_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "for loop range step cannot be zero") {
		t.Fatalf("expected zero-step diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsForLoopNonIntegralBounds(t *testing.T) {
	src := `def bad() -> void:
	for value in 0.0..1.0:
		pass
`
	_, errs := parseAndAnalyze(t, "for_loop_non_integral_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "for loop range requires integral bounds") {
		t.Fatalf("expected non-integral-bounds diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsParallelForOverFrozenPackedStore(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	common:
		span: int
	Int(value: int)
	Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int:
	can Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
		store: Expr.Store[Local] = Expr.Store(owner)
		in store:
			_ = new Expr.Int(span: 1, value: 3)
			_ = new Expr.Int(span: 2, value: 4)
		frozen: Expr.Store[Frozen] = freeze(move store)
		pool workers(2):
			parallel for node in frozen:
				if node in frozen as Expr.Int(value: value):
					_ = value + node.span
		return 0
`
	result, errs := parseAndAnalyze(t, "parallel_for_frozen_store_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "visit", "int")
}
func TestAnalyzeAcceptsPackedStoreTagsViewAndParallelForIndexBinder(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	Int(value: int)
	Add(left: Expr, right: Expr)

def visit(owner: Arena) -> int:
	can Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
		store: Expr.Store[Local] = Expr.Store(owner)
		in store:
			left: Expr = new Expr.Int(value: 1)
			right: Expr = new Expr.Int(value: 2)
			_ = new Expr.Add(left: left, right: right)
		frozen: Expr.Store[Frozen] = freeze(move store)
		tags: dview[Expr.Tag] = frozen.tags
		pool workers(2):
			parallel for tag at i in tags:
				_ = i
				if tag == Expr.Tag.Add:
					_ = i
		return 0
`
	result, errs := parseAndAnalyze(t, "parallel_for_packed_tags_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "visit", "int")
}
func TestAnalyzeRejectsParallelForOutsidePool(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	Int(value: int)

def bad(store: Expr.Store[Frozen]) -> void:
	parallel for node in store:
		pass
`
	_, errs := parseAndAnalyze(t, "parallel_for_outside_pool_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "parallel for requires an enclosing pool scope") {
		t.Fatalf("expected missing-pool diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsParallelForMutatingOuterBinding(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
packed enum Expr:
	Int(value: int)

def bad(store: Expr.Store[Frozen]) -> void can[Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange]:
	total: mutable int = 0
	pool workers(2):
		parallel for node in store:
			total <- total + 1
`
	_, errs := parseAndAnalyze(t, "parallel_for_outer_mutation_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "parallel for body cannot mutate outer binding \"total\"") {
		t.Fatalf("expected outer-mutation diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsParallelForOverReadonlyChunksExactView(t *testing.T) {
	src := parallelForConcurrencyPrelude + `
def visit(values: darray[i32, 4]) -> int:
	can Pool.Create, Pool.Shutdown, Pool.Submit, Pool.WaitAll, Memory.Allocate, Memory.Release, Abort.Panic, Atomics.Load, Atomics.CompareExchange:
		full: dview[i32] = values[0:4]
		chunks: ChunksExactView[i32] = chunks_exact(readonly(full), 2)
		pool workers(2):
			parallel for chunk in chunks:
				_ = chunk[0] + chunk[1]
		return 0
`
	result, errs := parseAndAnalyze(t, "parallel_for_chunks_exact_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "visit", "int")
}
func TestAnalyzeRejectsAssigningReadonlyViewIndexResult(t *testing.T) {
	src := `def bad() -> void:
	buf: mutable array[i32, 4] = [1, 2, 3, 4]
	ro: view[i32, 0, 2] = readonly(buf[0:2])
	ro[0] <- 7
`
	_, errs := parseAndAnalyze(t, "readonly_index_assign_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot assign to readonly view index result") {
		t.Fatalf("expected readonly-view assignment diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsZipMapWithoutReadonlySources(t *testing.T) {
	src := `def add_pair(left: i32, right: i32) -> i32:
	return left + right

def bad() -> void:
	buf: mutable array[i32, 6] = [0, 0, 1, 2, 3, 4]
	dst: view[i32, 0, 2] = buf[0:2]
	src1: view[i32, 2, 4] = buf[2:4]
	src2: view[i32, 4, 6] = buf[4:6]
	zip_map(dst, src1, src2, add_pair)
`
	_, errs := parseAndAnalyze(t, "zip_map_requires_readonly_sources_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "zip_map requires source 1 to be a readonly contiguous exact-extent view") {
		t.Fatalf("expected zip_map readonly-source diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsReduceSumWithReadonlySourceAndExtraArgs(t *testing.T) {
	src := `def add_bias(value: i64, bias: i64) -> i64:
	return value + bias

def run(values: darray[i64, 4], bias: i64) -> i64:
	base: dview[i64] = values[0:4]
	return reduce_sum(readonly(base), add_bias, bias)
`
	result, errs := parseAndAnalyze(t, "reduce_sum_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
	requireFunctionReturnTypeString(t, result, "run", "i64")
}
func TestAnalyzeRejectsReduceSumNonNumericCallbackResult(t *testing.T) {
	src := `def is_positive(value: i64) -> bool:
	return value > 0

def bad(values: darray[i64, 4]) -> i64:
	base: dview[i64] = values[0:4]
	return reduce_sum(readonly(base), is_positive)
`
	_, errs := parseAndAnalyze(t, "reduce_sum_bool_callback_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "reduce_sum callback must return a numeric accumulator") {
		t.Fatalf("expected reduce_sum numeric-callback diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsAssigningPackedStoreSliceIndexResult(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(store: Expr.Store[Frozen], node: Expr) -> void:
	chunk: dview[Expr] = store[0:store.count]
	chunk[0] <- node
`
	_, errs := parseAndAnalyze(t, "packed_store_slice_index_assign_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot assign to packed store view index result") {
		t.Fatalf("expected packed store slice assignment diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsAssigningPackedStoreTagViewIndexResult(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def bad(store: Expr.Store[Frozen]) -> void:
	tags: dview[Expr.Tag] = store.tags
	tags[0] <- Expr.Tag.Int
`
	_, errs := parseAndAnalyze(t, "packed_store_tag_view_index_assign_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot assign to packed store tag view index result") {
		t.Fatalf("expected packed store tag view assignment diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsPayloadlessPackedCommonFieldInitialization(t *testing.T) {
	src := `packed enum Token:
	common:
		span: int
	Region

def build(store_owner: Arena) -> Token:
	store: Token.Store[Local] = Token.Store(store_owner)
	return new[store] Token.Region(span: 4)
`
	_, errs := parseAndAnalyze(t, "packed_enum_payloadless_common_field_init_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsPayloadlessPackedConstructorInAlloc(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def build(store_owner: Arena) -> Token:
	store: Token.Store[Local] = Token.Store(store_owner)
	return new[store] Token.Region
`
	_, errs := parseAndAnalyze(t, "packed_enum_payloadless_alloc_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsBarePayloadlessPackedConstructorWithoutActiveStore(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def build() -> Token:
	return Token.Region
`
	_, errs := parseAndAnalyze(t, "packed_enum_payloadless_ctor_without_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum constructor \"Token.Region\" requires an active in Token.Store: scope or explicit new[Token.Store]") {
		t.Fatalf("expected payloadless packed constructor diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeAcceptsBarePayloadlessPackedConstructorWithActiveStoreLocal(t *testing.T) {
	src := `packed enum Token:
	Ident
	Region

def build(store_owner: Arena) -> Token:
	store: Token.Store[Local] = Token.Store(store_owner)
	return Token.Region
`
	_, errs := parseAndAnalyze(t, "packed_enum_payloadless_ctor_active_store_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsAffinePayloadsInPackedEnums(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64
extern take_handle(handle: Handle) -> void

affine struct Handle:
	raw: mutable uintptr

packed enum Job:
	common:
		handle: Handle
	Run(thread: Thread[i64, Joinable])

def consume(owner: Arena, handle: Handle, thread: Thread[i64, Joinable]) -> i64:
	store: Job.Store[Local] = Job.Store(owner)
	job: Job = new[store] Job.Run(handle: move handle, thread: move thread)
	match job in store:
		Job.Run(thread: taken):
			take_handle(move job.handle)
			return join(move taken)
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_affine_payload_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeConsumesAffinePackedEnumAfterMatch(t *testing.T) {
	src := `extern join(thread: Thread[i64, Joinable]) -> i64

packed enum Job:
	Run(thread: Thread[i64, Joinable])

def consume_twice(owner: Arena, thread: Thread[i64, Joinable]) -> i64:
	store: Job.Store[Local] = Job.Store(owner)
	job: Job = new[store] Job.Run(thread: move thread)
	match job in store:
		Job.Run(thread: taken):
			_ = join(move taken)
	match job in store:
		Job.Run(thread: taken):
			return join(move taken)
	return 0
`
	_, errs := parseAndAnalyze(t, "packed_enum_affine_payload_match_consumes_root.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "cannot be used: usage facts were consumed by match over affine enum") {
		t.Fatalf("expected affine packed match consumption diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeWarnsOnAvoidableStructPadding(t *testing.T) {
	src := `struct Padded:
	flag: bool
	value: i64
	small: i16
`
	result, errs := parseAndAnalyze(t, "struct_padding_warn.elisa", src)
	requireNoErrors(t, errs)
	warns := result.Warnings()
	if len(warns) == 0 {
		t.Fatal("expected padding warning, got none")
	}
	all := strings.Join(warns, "\n")
	if !strings.Contains(all, `struct "Padded" has 8 bytes of avoidable padding`) {
		t.Fatalf("expected padding-size warning, got:\n%s", all)
	}
	if !strings.Contains(all, `consider ordering fields as "value", "small", "flag"`) {
		t.Fatalf("expected field reorder suggestion, got:\n%s", all)
	}
}
func TestAnalyzeDoesNotWarnOnDenseStructFieldOrder(t *testing.T) {
	src := `struct Dense:
	value: i64
	small: i16
	flag: bool
`
	result, errs := parseAndAnalyze(t, "struct_padding_dense_ok.elisa", src)
	requireNoErrors(t, errs)
	requireNoWarnings(t, result)
}
func TestAnalyzeWarnsOnReprCStructPadding(t *testing.T) {
	src := `struct CLayout:
	flag: bool
	value: i64
	small: i16
`
	result, errs := parseAndAnalyze(t, "struct_padding_reprc_ok.elisa", src)
	requireNoErrors(t, errs)
	warns := result.Warnings()
	if len(warns) == 0 {
		t.Fatal("expected padding warning for struct, got none")
	}
	all := strings.Join(warns, "\n")
	if !strings.Contains(all, `struct "CLayout" has 8 bytes of avoidable padding`) {
		t.Fatalf("expected padding warning, got:\n%s", all)
	}
}
func TestAnalyzeRejectsSpawnOfValueDependingOnUnpublishedPackedStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

def spawn1[A, R](fn: func(A) -> R, arg: A) -> Thread[R, Joinable]:
	return zeroed

def worker(node: Expr) -> i64:
	return 0

def bad(owner: Arena) -> Thread[i64, Joinable]:
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Int(value: 1)
	return spawn1(worker, node)
`
	_, errs := parseAndAnalyze(t, "spawn1_unpublished_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "argument to \"spawn1\" cannot cross thread boundary: store dependency facts require rebase to frozen/public store, got \"Expr.Store[Local]\"") {
		t.Fatalf("expected unpublished-store spawn diagnostic, got:\n%s", all)
	}
}
