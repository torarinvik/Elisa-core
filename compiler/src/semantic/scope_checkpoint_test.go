package semantic

import (
	"strings"
	"testing"
)

func TestAnalyzeScopeCheckpointAndReverseIterableLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "scope_checkpoint.llcontext", `extern pool_new(workers: usize) -> ThreadPool can[Pool.Create]

def build(owner: Arena, items: darray[int]) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
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
    result := analyzeFunctionAnalysisTestSource(t, "grouped_scope_checkpoint.llcontext", `def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
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
    result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "grouped_scope_checkpoint_invalid.llcontext", `def build(value: i64) -> void:
    checkpoint value, value:
        pass
`)
    all := strings.Join(result.Errors(), "\n")
    if !strings.Contains(all, "checkpoint requires a region or mutable darray value") {
        t.Fatalf("expected grouped checkpoint target diagnostic, got:\n%s", all)
    }
}

func TestAnalyzeWarnsOnLegacyReverseIterableLoopSyntax(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "legacy_reverse_iter_warning.llcontext", `def build(items: darray[int]) -> void:
    for rev value in items:
        pass
`)
	if all := strings.Join(result.Warnings(), "\n"); !strings.Contains(all, "legacy reverse iterable loop syntax `for rev ... in ...:` is deprecated") {
		t.Fatalf("expected legacy reverse iterable warning, got:\n%s", all)
	}
}

func TestAnalyzeRejectsReverseRangeForNow(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "reverse_range_rejected.llcontext", `def build() -> void:
    for rev i in 0..<10:
        pass
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "reverse range for loops are not supported yet") {
		t.Fatalf("expected reverse range diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStoreAndDictSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "store_dict_sugar.llcontext", `
store PendingGotoStore:
    name_key: u32
    depth: u32

def arena_dict_get[T](m: any dict[dstr[key_shape], T]&, key: dstr[key_shape]) -> mutable any T&?:
    return null

def arena_dict_contains[T](m: any dict[dstr[key_shape], T]&, key: dstr[key_shape]) -> bool:
    return false

def arena_dict_remove[T](m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape]) -> bool:
    return false

def arena_dict_clear[T](m: mutable any dict[dstr[key_shape], T]&):
    pass

def arena_dict_reserve[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, min_capacity: usize) -> void:
    pass

def arena_dict_put[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape], value: T) -> mutable any T&?:
    return null

def arena_dict_get_or_insert[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape], value: T) -> mutable any T&?:
    return null

def build(owner: Arena, key: dstr[key_shape]) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.reserve(8)
        pending.push(1u32, 2u32)
        pending.truncate(1)
        pending.clear()
        values: mutable dict[dstr[key_shape], i64] = zeroed
        _ = values.put(key, 7)
        slot = values.get_or_insert(key):
            base = 9
            base
        _ = slot
        if values.entry(key).found == false:
            _ = values.entry(key).insert(11)
        _ = values.entry(key).value
        _ = values.entry(key).get_or_insert(13)
        entry_slot = values.entry(key).get_or_insert():
            17
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

func TestAnalyzeRejectsGenericKeyRuntimeBackedDictSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "generic_key_dict_sugar.llcontext", `
struct Key:
    id: u32

def arena_dict_get[K, T](m: any dict[K, T]&, key: K) -> mutable any T&?:
    return null

def arena_dict_contains[K, T](m: any dict[K, T]&, key: K) -> bool:
    return false

def arena_dict_remove[K, T](m: mutable any dict[K, T]&, key: K) -> bool:
    return false

def arena_dict_clear[K, T](m: mutable any dict[K, T]&):
    pass

def arena_dict_reserve[K, T](a: mutable any Arena&, m: mutable any dict[K, T]&, min_capacity: usize) -> void:
    pass

def arena_dict_put[K, T](a: mutable any Arena&, m: mutable any dict[K, T]&, key: K, value: T) -> mutable any T&?:
    return null

def arena_dict_get_or_insert[K, T](a: mutable any Arena&, m: mutable any dict[K, T]&, key: K, value: T) -> mutable any T&?:
    return null

def build(owner: Arena, key: Key, id: u32) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        counts: mutable dict[u32, i64] = zeroed
        keyed: mutable dict[Key, i64] = zeroed
        _ = counts.put(id, 7)
        count_slot = counts.get_or_insert(id):
            9
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
	if !strings.Contains(all, "runtime-backed dict operations currently support only dict[dstr, V]") {
		t.Fatalf("expected runtime-backed dict restriction diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeDoExprBlock(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "do_expr_block.llcontext", `def build() -> i64:
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
	result := analyzeFunctionAnalysisTestSource(t, "do_expr_block_call.llcontext", `extern consume(x: i64) -> i64

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
	result := analyzeFunctionAnalysisTestSource(t, "do_expr_block_grouped_forms.llcontext", `extern consume(x: i64, y: i64) -> i64

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
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args.llcontext", `extern consume(x: i64, y: i64) -> i64

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
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "named_function_call_args_unknown.llcontext", `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    return consume(z: 7, x: 1)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `function "consume" has no parameter "z"`) {
		t.Fatalf("expected unknown named arg diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeNamedFunctionCallArgsRejectPositionalAfterNamed(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "named_function_call_args_positional_after_named.llcontext", `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    return consume(x: 1, 7)
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `function "consume" cannot use positional arguments after named arguments`) {
		t.Fatalf("expected positional-after-named diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeNamedGenericFunctionCallArgs(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "named_generic_function_call_args.llcontext", `def pick_second[T](first: T, second: T) -> T:
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
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args_local_alias.llcontext", `def add(x: i64, y: i64) -> i64:
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
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args_global_alias.llcontext", `def add(x: i64, y: i64) -> i64:
    return x + y

global runner: func(i64, i64) -> i64 = add

def build() -> i64:
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
	result := analyzeFunctionAnalysisTestSource(t, "named_function_call_args_global_field_alias.llcontext", `struct CallbackBox:
    run: func(i64, i64) -> i64

def add(x: i64, y: i64) -> i64:
    return x + y

global BOX: CallbackBox = CallbackBox(add)

def build() -> i64:
    return BOX.run(y: 7, x: do:
        seed = 3
        seed
    )
`)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected semantic errors: %v", errs)
	}
}
