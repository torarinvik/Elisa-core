package backend_test

import (
	"elisacore/src/backend"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersNamedEnumPayloadPatterns(t *testing.T) {
	src := `enum PairOrInt:
	Just(value: int)
	Pair(left: int, right: int)

def score(value: PairOrInt) -> int:
	match value:
		PairOrInt.Just(value: inner):
			return inner
		PairOrInt.Pair(right: r, left: l):
			return l + r
	return 0
`
	result := parseAndAnalyze(t, "backend_enum_named_payload_patterns.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%PairOrInt = type { i32, [2 x i64] }",
		"define i64 @score(%PairOrInt",
		"extractvalue %PairOrInt",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "extractvalue { i64, i64 }") < 1 {
		t.Fatalf("expected named payload pattern lowering to unpack pair payloads, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersNamedEnumConstructorArgs(t *testing.T) {
	src := `enum PairOrInt:
	Pair(left: int, right: int)

def make_pair() -> PairOrInt:
	return PairOrInt.Pair(right: 4, left: 3)
`
	result := parseAndAnalyze(t, "backend_enum_named_ctor_args.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%PairOrInt = type { i32, [2 x i64] }",
		"define %PairOrInt @make_pair()",
		"store { i64, i64 } { i64 3, i64 4 }, ptr %enum.payload.ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersEnumEqualityViaTagAndPayloadWords(t *testing.T) {
	src := `enum MaybeInt:
	None
	Some(int)
	Pair(int, int)

def same_none(left: MaybeInt, right: MaybeInt) -> bool:
	return left == right

def differs(left: MaybeInt, right: MaybeInt) -> bool:
	return left != right

def compare_payload() -> bool:
	return MaybeInt.Pair(3, 4) == MaybeInt.Pair(3, 4)
`
	result := parseAndAnalyze(t, "backend_enum_equality.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%MaybeInt = type { i32, [2 x i64] }",
		"define i1 @same_none(%MaybeInt",
		"define i1 @differs(%MaybeInt",
		"define i1 @compare_payload()",
		"extractvalue %MaybeInt",
		"extractvalue [2 x i64]",
		"icmp eq i32",
		"and i1",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"icmp eq %MaybeInt", "icmp ne %MaybeInt"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected enum comparisons to avoid aggregate icmp %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRLowersPayloadlessEnumsAsPlainTags(t *testing.T) {
	src := `enum TokenKind:
	Ident
	Region
	Destroy
	New

def is_region(kind: TokenKind) -> bool:
	return kind == TokenKind.Region

def next_kind() -> TokenKind:
	return TokenKind.Destroy
`
	result := parseAndAnalyze(t, "backend_payloadless_enum_tags.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i1 @is_region(i8",
		"define i8 @next_kind()",
		"icmp eq i8",
		"ret i8 2",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	for _, bad := range []string{"%TokenKind = type", "[0 x i64]", "extractvalue %TokenKind"} {
		if strings.Contains(output, bad) {
			t.Fatalf("expected payloadless enum lowering to avoid %q, got:\n%s", bad, output)
		}
	}
}
func TestGenerateLLVMIRLowersDenseNodeTablesDirectly(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: i32
	Lit(value: i32)
	Add(left: Expr, right: Expr)

def inspect(owner: Arena) -> i32:
	store: Expr.Store[Local] = Expr.Store(owner)
	in store:
		left: Expr = new Expr.Lit(span: 1, value: 3)
		right: Expr = new Expr.Lit(span: 2, value: 4)
		_ = new Expr.Add(span: 5, left: left, right: right)

	frozen: Expr.Store[Frozen] = freeze(move store)
	node: Expr = frozen[2]
	key: NodeKey[Expr] = dense_key(node, frozen)
	table: NodeTable[Expr, i32] = node_table_fill[Expr, i32](owner, frozen, -1)
	table[key] <- 0
	values: view[i32] = table.values
	if values.len == frozen.count:
		return frozen[key].span
	return 0
`
	result := parseAndAnalyze(t, "backend_dense_node_table.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, check := range []string{
		"%\"NodeKey$generic_v1$",
		"%\"NodeTable$generic_v1$",
		"call ptr @arena_alloc(",
		"call void @arena_da_fill(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	nodeKeyType := genericV1Symbol(t, output, `%"NodeKey$generic_v1$`)
	nodeTableType := genericV1Symbol(t, output, `%"NodeTable$generic_v1$`)
	assertLLVMIRContains(t, output,
		nodeKeyType+" = type { i32 }",
		nodeTableType+" = type { %DynArrayView }",
	)
	if strings.Contains(output, "@node_table_fill(") {
		t.Fatalf("expected node_table_fill to lower directly in the backend without a runtime wrapper, got:\n%s", output)
	}

	body := functionIR(output, "inspect")
	if body == "" {
		t.Fatalf("expected to find inspect body, got:\n%s", output)
	}
	if !strings.Contains(body, "getelementptr inbounds nuw "+nodeTableType) {
		t.Fatalf("expected inspect to address through the NodeTable carrier struct, got:\n%s", body)
	}
	if !strings.Contains(body, "extractvalue "+nodeKeyType) {
		t.Fatalf("expected inspect to read the dense node key carrier index field, got:\n%s", body)
	}
}
func TestGenerateLLVMIRLowersDenseNodeTablesFromHiddenFrozenStoreFieldRoots(t *testing.T) {
	src := `packed enum Expr:
	common:
		span: i32
	Lit(value: i32)
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
	return FrozenBox{store: frozen}

def inspect(owner: Arena) -> i32:
	box: FrozenBox = make_box(owner)
	node: Expr = box.store[2]
	key: NodeKey[Expr] = dense_key(node, box.store)
	table: NodeTable[Expr, i32] = node_table_fill[Expr, i32](owner, box.store, -1)
	table[key] <- 7
	return table[key]
`
	result := parseAndAnalyze(t, "backend_dense_node_table_hidden_frozen_field_root.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	for _, check := range []string{
		"%\"NodeKey$generic_v1$",
		"%\"NodeTable$generic_v1$",
		"call ptr @arena_alloc(",
		"call void @arena_da_fill(",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	nodeKeyType := genericV1Symbol(t, output, `%"NodeKey$generic_v1$`)
	nodeTableType := genericV1Symbol(t, output, `%"NodeTable$generic_v1$`)
	assertLLVMIRContains(t, output,
		nodeKeyType+" = type { i32 }",
		nodeTableType+" = type { %DynArrayView }",
	)
	if strings.Contains(output, "@node_table_fill(") {
		t.Fatalf("expected hidden-field node_table_fill to lower directly in the backend without a runtime wrapper, got:\n%s", output)
	}
}
func TestGenerateLLVMIRLowersDictSurfaceTypesViaDynDictCarrier(t *testing.T) {
	src := `extern take_runtime(values: DynDict[cstr, i32]) -> void
extern make_runtime() -> DynDict[cstr, i32]

def id[V](values: dict[cstr, V]) -> dict[cstr, V]:
	return values

def keep(values: dict[cstr, i32]) -> dict[cstr, i32]:
	return id(values)

def pass_runtime(values: dict[cstr, i32]) -> void:
	take_runtime(values)

def from_runtime() -> dict[cstr, i32]:
	return make_runtime()
`
	result := parseAndAnalyze(t, "backend_dict_runtime_bridge.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%\"DynDict$generic_v1$",
		"declare void @take_runtime(%\"DynDict$generic_v1$",
		"@make_runtime()",
		"define void @pass_runtime(%\"DynDict$generic_v1$",
		"call void @take_runtime(%\"DynDict$generic_v1$",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	dictType := genericV1Symbol(t, output, `%"DynDict$generic_v1$`)
	idFunction := genericV1Symbol(t, output, `@"id$generic_v1$`)
	assertLLVMIRContains(t, output,
		dictType+" = type { ptr, i64, i64, i64, ptr }",
		"declare void @take_runtime("+dictType+")",
		"declare "+dictType+" @make_runtime()",
		"define "+dictType+" "+idFunction+"("+dictType,
		"define "+dictType+" @keep("+dictType,
		"call "+dictType+" "+idFunction+"("+dictType,
		"call void @take_runtime("+dictType,
		"define "+dictType+" @from_runtime()",
		"call "+dictType+" @make_runtime()",
	)
}
func TestGenerateLLVMIRRejectsGenericDictRuntimeBridge(t *testing.T) {
	// Integral keys (u32, ...) are now runtime-backed, so their surface `dict` IS the `DynDict`
	// carrier and bridges cleanly. A float key is NOT runtime-backed, so the surface-vs-carrier
	// mismatch still stands — that is the case this test pins.
	src := `extern take_runtime(values: DynDict[f64, i32]) -> void
extern make_runtime() -> DynDict[f64, i32]

def use(values: dict[f64, i32]) -> dict[f64, i32]:
	take_runtime(values)
	return make_runtime()
`
	l := lexer.New("backend_dict_runtime_bridge_reject.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile("backend_dict_runtime_bridge_reject.elisa")
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.Analyze(file)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "expects dict[f64, i32] (runtime carrier), got dict[f64, i32]") {
		t.Fatalf("expected non-backed-key runtime bridge mismatch diagnostic, got:\n%s", all)
	}
}
func TestGenerateLLVMIRSpecializesDictHelperStyleFunctions(t *testing.T) {
	src := `error RuntimeError:
	OutOfMemory

def arena_dict_get[V](m: dict[cstr, V]&, key: cstr) -> V&?:
	return null

def arena_dict_put[V](a: Arena&, m: dict[cstr, V]&, key: cstr, value: V) -> V&? error[RuntimeError]:
	raise RuntimeError.OutOfMemory

def touch(a: Arena&, m: dict[cstr, i32]&, key: cstr) -> bool:
	slot: i32&? = try arena_dict_put(a, m, key, 7) else null
	if slot == null:
		return false
	maybe_slot: i32&? = arena_dict_get(m, key)
	return maybe_slot != null
`
	result := parseAndAnalyze(t, "backend_dict_helper_calls.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__RuntimeError__i32 = type { i32, ptr }",
		"define ptr @\"arena_dict_get$generic_v1$",
		"define i32 @\"arena_dict_put$generic_v1$",
		"define i1 @touch(ptr %0, ptr %1, ptr %2)",
		"call i32 @\"arena_dict_put$generic_v1$",
		"call ptr @\"arena_dict_get$generic_v1$",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	getFunction := genericV1Symbol(t, output, `@"arena_dict_get$generic_v1$`)
	putFunction := genericV1Symbol(t, output, `@"arena_dict_put$generic_v1$`)
	assertLLVMIRContains(t, output,
		"define ptr "+getFunction+"(ptr",
		"call ptr "+getFunction+"(ptr",
		"define i32 "+putFunction+"(ptr",
		"call i32 "+putFunction+"(ptr",
	)
}
func TestGenerateLLVMIRLowersFrontendStressFixture(t *testing.T) {
	src := loadFixtureSource(t, "Code", "test_programs", "frontend_stress.elisa")
	result := parseAndAnalyze(t, "backend_frontend_stress.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%SourceSpan = type { i64, i64 }",
		"%Token = type { i8, %SourceSpan, ptr }",
		"%\"DynDict$generic_v1$",
		"%Scope = type { ptr, %\"DynDict$generic_v1$",
		"%ParserState = type { %DynArrayView, i64, ptr }",
		"define %DynArrayView @make_tokens(ptr",
		"define i32 @frontend_scope_stress(ptr",
		"define i64 @frontend_region_token(i64",
		"define i32 @frontend_smoke(ptr",
		"define i32 @arena_dict_put__Symbol(ptr",
		"define i1 @arena_dict_contains__Symbol(ptr",
		"call ptr @new_region_backend(i64 2048, i64 0)",
		"call ptr @arena_alloc(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	dictType := genericV1Symbol(t, output, `%"DynDict$generic_v1$`)
	assertLLVMIRContains(t, output,
		dictType+" = type { ptr, i64, i64, i64, ptr }",
		"%Scope = type { ptr, "+dictType+", i64 }",
		"define "+dictType+" @arena_dict_new__Symbol(ptr",
	)
}
func TestGenerateLLVMIRLowersAllocatorOwnershipFixture(t *testing.T) {
	src := loadFixtureSource(t, "Code", "test_programs", "allocator_ownership.elisa")
	result := parseAndAnalyze(t, "backend_allocator_ownership.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%FuzzPair = type { i64, i64 }",
		"%HeapPairNode = type { %FuzzPair, ptr }",
		"declare ptr @alloc_heap_pair_node()",
		"declare ptr @sfree_heap_pair_node(ptr)",
		"declare ptr @alloc_bytes(i64)",
		"declare ptr @sfree_bytes(ptr)",
		"declare i64 @snprintf(ptr, i64, ptr, ...)",
		"define i32 @recursive_pair_node_sum(ptr ",
		"define void @recursive_free_pair_chain(ptr ",
		"define i32 @build_pair_chain_sum(ptr ",
		"call i32 @recursive_pair_node_sum(ptr ",
		"call void @recursive_free_pair_chain(ptr ",
		"define i32 @alloc_and_format_heap_buffer(ptr ",
		"call i64 (ptr, i64, ptr, ...) @snprintf(",
		"@recursive_format_or_fallback(",
		"@allocator_ownership_combo(",
		"alloca [32 x i8]",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	if strings.Count(output, "call ptr @sfree_heap_pair_node(ptr") < 2 {
		t.Fatalf("expected multiple heap-pair frees in ownership fixture lowering, got:\n%s", output)
	}
	if strings.Count(output, "call i32 @require_heap_pair_node(ptr") < 3 {
		t.Fatalf("expected repeated heap-pair acquisition helper lowering, got:\n%s", output)
	}
}
