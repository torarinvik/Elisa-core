package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func TestAnalyzeScopeCheckpointAndReverseIterableLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "scope_checkpoint.elisa", `extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]

def build(owner: Arena, items: darray[int]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    total: mutable usize = 0
    for value in rev(items):
        total <- total + 1
    scope pool_new(2):
        pass
    in alloc:
        xs: mutable darray[int] = [1, 2, 3]
        checkpoint mark = xs:
            xs.push(4)
        return xs.count + total
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("unexpected semantic errors: %v", result.Errors())
	}
}

func TestAnalyzeGroupedCheckpointStmt(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "grouped_scope_checkpoint.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[int] = [1, 2]
        ys: mutable darray[int] = [3, 4]
        checkpoint xs, ys:
            xs.push(5)
            ys.push(6)
        return xs.count + ys.count
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeRejectsInvalidGroupedCheckpointTarget(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "grouped_scope_checkpoint_invalid.elisa", `def build(value: i64) -> void:
    checkpoint value, value:
        pass
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "checkpoint requires a region or mutable darray value") {
		t.Fatalf("expected grouped checkpoint target diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeInvalidatedRegionRefDiagnosticUsesFactVocabulary(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "region_fact_invalidated_use.elisa", `def build(seed: i32) -> i32:
    region scratch(1024)
    mark scratch as cp
    value: i32& @scratch = new[scratch] seed
    restore scratch from cp
    return value[0]
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `reference "value" cannot be used: region dependency facts were invalidated by restore of region "scratch" from checkpoint "cp"`) {
		t.Fatalf("expected invalidated region fact diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeInvalidatedDArrayViewDiagnosticUsesFactVocabulary(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_view_fact_invalidated_use.elisa", `def build(owner: Arena) -> i32:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        items: mutable darray[i32] = [1, 2, 3]
        view: view[i32] = items[0:items.count]
        items.push(4)
        return view[0]
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `view "view" cannot be used: storage dependency facts were invalidated by darray push of items`) {
		t.Fatalf("expected invalidated view fact diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeCopiedDArrayViewInvalidatesWithSourceMutation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "copied_darray_view_fact_invalidated_use.elisa", `def build(owner: Arena) -> i32:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        items: mutable darray[i32] = [1, 2, 3]
        view: view[i32] = items[0:items.count]
        copy: view[i32] = view
        items.clear()
        return copy[0]
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `view "copy" cannot be used: storage dependency facts were invalidated by darray clear of items`) {
		t.Fatalf("expected copied invalidated view diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeBranchDArrayMutationInvalidatesViewAfterMerge(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "branch_darray_view_fact_invalidated_use.elisa", `def build(owner: Arena, cond: bool) -> i32:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        items: mutable darray[i32] = [1, 2, 3]
        view: view[i32] = items[0:items.count]
        if cond:
            items.push(4)
        return view[0]
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `view "view" cannot be used: storage dependency facts were invalidated by darray push of items`) {
		t.Fatalf("expected branch invalidated view diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsLegacyReverseIterableLoopSyntax(t *testing.T) {
	l := lexer.New("legacy_reverse_iter_error.elisa", []byte(`def build(items: darray[int]) -> void:
    for rev value in items:
        pass
`))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	_ = p.ParseFile("legacy_reverse_iter_error.elisa")
	if all := strings.Join(p.Errors(), "\n"); !strings.Contains(all, "legacy reverse iterable loop syntax `for rev ... in ...:` is no longer supported") {
		t.Fatalf("expected legacy reverse iterable parser diagnostic, got:\n%s", all)
	}
}

func TestParseRejectsLegacyReverseRangeLoopSyntax(t *testing.T) {
	l := lexer.New("reverse_range_rejected.elisa", []byte(`def build() -> void:
    for rev i in 0..<10:
        pass
    `))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	p := parser.New(tokens)
	_ = p.ParseFile("reverse_range_rejected.elisa")
	if all := strings.Join(p.Errors(), "\n"); !strings.Contains(all, "legacy reverse iterable loop syntax `for rev ... in ...:` is no longer supported") {
		t.Fatalf("expected legacy reverse range parser diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDuplicateLocalBinding(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "duplicate_local_binding.elisa", `def build() -> i64:
    value: i64 = 1
    value: i64 = 2
    return value
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, DuplicateLocalMessage("value", SymbolLocal)) {
		t.Fatalf("expected duplicate local diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeUndefinedAssignmentTargetSuggestsLocalBinding(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "undefined_assignment_target_hint.elisa", `def build() -> void:
    value <- 1
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "undefined assignment target \"value\" (use = to introduce a new local; <- requires an existing mutable target)") {
		t.Fatalf("expected assignment target hint diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsDuplicateGlobalDeclaration(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "duplicate_global_declaration.elisa", `
global answer: i64 = 1
global answer: i64 = 2
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, DuplicateDeclarationMessage("answer", SymbolGlobal)) {
		t.Fatalf("expected duplicate global diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStoreAndDictSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "store_dict_sugar.elisa", `
layout soa struct PendingGotoStore:
    name_key: u32
    depth: u32

def arena_dict_get[T](m: dict[cstr[key_shape], T]&, key: cstr[key_shape]) -> T&?:
    return null

def arena_dict_get[T](m: mutable dict[cstr[key_shape], T]&, key: cstr[key_shape]) -> mutable T&?:
    return null

def arena_dict_contains[T](m: dict[cstr[key_shape], T]&, key: cstr[key_shape]) -> bool:
    return false

def arena_dict_remove[T](m: mutable dict[cstr[key_shape], T]&, key: cstr[key_shape]) -> bool:
    return false

def arena_dict_clear[T](m: mutable dict[cstr[key_shape], T]&):
    pass

def arena_dict_reserve[T](a: mutable Arena&, m: mutable dict[cstr[key_shape], T]&, min_capacity: usize) -> void:
    pass

def arena_dict_put[T](a: mutable Arena&, m: mutable dict[cstr[key_shape], T]&, key: cstr[key_shape], value: T) -> mutable T&?:
    return null

def arena_dict_get_or_insert[T](a: mutable Arena&, m: mutable dict[cstr[key_shape], T]&, key: cstr[key_shape], value: T) -> mutable T&?:
    return null

def build(owner: Arena, key: cstr[key_shape]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.reserve(8)
        pending.push(1, 2)
        pending.truncate(1)
        pending.clear()
        values: mutable dict[cstr[key_shape], i64] = zeroed
        _ = values.put(key, 7)
        base = 9
        slot = values.get_or_insert(key, base)
        _ = slot
        if values.entry(key).found == false:
            _ = values.entry(key).insert(11)
        _ = values.entry(key).value
        _ = values.entry(key).get_or_insert(13)
        entry_slot = values.entry(key).get_or_insert(17)
        _ = entry_slot
        _ = values.get(key)
        _ = values.contains(key)
        _ = values.remove(key)
        values.clear()
        return pending.name_key.count + values.count
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeStoreRowsIteration(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "store_rows_iteration.elisa", `
layout soa struct PendingGotoStore:
    name_key: usize
    depth: usize

def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.push(1, 2)
        pending.push(3, 4)
        total: mutable usize = 0
        for row in pending.rows():
            total <- total + row.name_key + row.depth
        for index, row in pending.rows().enumerate():
            total <- total + index + row.name_key
        for row in rev(pending.rows()):
            total <- total + row.depth
        return total
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeAllowsStoreRowMutation(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "store_rows_mutation.elisa", `
layout soa struct PendingGotoStore:
    name_key: usize
    depth: usize

def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.push(1, 2)
        total: mutable usize = 0
        for row in pending.rows():
            row.name_key <- 9
            row.depth <- 7
            total <- total + row.name_key + row.depth
        return total
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeRejectsStoreRowsRefBinding(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "store_rows_ref_binding.elisa", `
layout soa struct PendingGotoStore:
    name_key: usize
    depth: usize

def build(owner: Arena) -> void:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.push(1, 2)
        for ref row in pending.rows():
            pass
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "for ref requires an addressable array-like iterable") {
		t.Fatalf("expected store rows ref-binding diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsGenericKeyRuntimeBackedDictSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "generic_key_dict_sugar.elisa", `
struct Key:
    id: u32

def arena_dict_get[K, T](m: dict[K, T]&, key: K) -> T&?:
    return null

def arena_dict_get[K, T](m: mutable dict[K, T]&, key: K) -> mutable T&?:
    return null

def arena_dict_contains[K, T](m: dict[K, T]&, key: K) -> bool:
    return false

def arena_dict_remove[K, T](m: mutable dict[K, T]&, key: K) -> bool:
    return false

def arena_dict_clear[K, T](m: mutable dict[K, T]&):
    pass

def arena_dict_reserve[K, T](a: mutable Arena&, m: mutable dict[K, T]&, min_capacity: usize) -> void:
    pass

def arena_dict_put[K, T](a: mutable Arena&, m: mutable dict[K, T]&, key: K, value: T) -> mutable T&?:
    return null

def arena_dict_get_or_insert[K, T](a: mutable Arena&, m: mutable dict[K, T]&, key: K, value: T) -> mutable T&?:
    return null

def build(owner: Arena, key: Key, id: u32) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        counts: mutable dict[u32, i64] = zeroed
        keyed: mutable dict[Key, i64] = zeroed
        _ = counts.put(id, 7)
        count_slot = counts.get_or_insert(id, 9)
        _ = count_slot
        _ = keyed.get(key)
        _ = keyed.contains(key)
        if keyed.entry(key).found == false:
            _ = keyed.entry(key).insert(11)
        _ = keyed.entry(key).value
        _ = keyed.entry(key).get_or_insert(13)
        _ = keyed.remove(key)
        keyed.clear()
        return counts.count + keyed.count
`)
	all := strings.Join(result.Errors(), "\n")
	// `dict[u32, i64]` (counts) is now a supported key type; the `dict[Key, i64]` (keyed,
	// a struct key) is still rejected — aggregates have no value equality for the linear-scan find.
	if !strings.Contains(all, "runtime-backed dict keys must be cstr, an integer type, bool, or a const enum") {
		t.Fatalf("expected runtime-backed dict key restriction diagnostic for the struct key, got:\n%s", all)
	}
}

func TestAnalyzeDoExprBlock(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "do_expr_block.elisa", `def build() -> i64:
    value = do:
        base = 9
        base + 4
    return value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeCallWithDoExprBlockArg(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "do_expr_block_call.elisa", `extern consume(x: i64) -> i64

def build() -> i64:
    value = consume(do:
        base = 9
        base + 4
    )
    return value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeGroupedDoExprBlockForms(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "do_expr_block_grouped_forms.elisa", `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    values: i64[2] = [do:
        base = 9
        base + 4
    , 7]
    value = consume(do:
        seed = 3
        seed + 1
    , values[1])
    return value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeNamedFunctionCallArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args.elisa", `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    value = consume(y: 7, x: do:
        seed = 3
        seed + 1
    )
    return value
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeNamedFunctionCallArgsRejectUnknownName(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "named_function_call_args_unknown.elisa", `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    return consume(z: 7, x: 1)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `function "consume" has no parameter "z"`) {
		t.Fatalf("expected unknown named arg diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeNamedFunctionCallArgsRejectPositionalAfterNamed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "named_function_call_args_positional_after_named.elisa", `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    return consume(x: 1, 7)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `function "consume" cannot use positional arguments after named arguments`) {
		t.Fatalf("expected positional-after-named diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeNamedGenericFunctionCallArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "named_generic_function_call_args.elisa", `def pick_second[T](first: T, second: T) -> T:
    return second

def build() -> i64:
    return pick_second[i64](second: 7, first: do:
        seed = 3
        seed + 1
    )
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeNamedFunctionCallArgsThroughLocalAlias(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args_local_alias.elisa", `def add(x: i64, y: i64) -> i64:
    return x + y

def build() -> i64:
    f = add
    return f(y: 7, x: do:
        seed = 3
        seed
    )
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeNamedFunctionCallArgsThroughGlobalAlias(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args_global_alias.elisa", `def add(x: i64, y: i64) -> i64:
    return x + y

global runner: func(i64, i64) -> i64 = add

def build() -> i64:
    can Global.Read:
        return runner(y: 7, x: do:
            seed = 3
            seed
        )
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}

func TestAnalyzeNamedFunctionCallArgsThroughGlobalFieldAlias(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args_global_field_alias.elisa", `struct CallbackBox:
    run: func(i64, i64) -> i64

def add(x: i64, y: i64) -> i64:
    return x + y

global BOX: CallbackBox = CallbackBox{run: add}

def build() -> i64:
    can Global.Read:
        return BOX.run(y: 7, x: do:
            seed = 3
            seed
        )
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}
