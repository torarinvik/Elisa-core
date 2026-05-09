package backend

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
	"elisacore/src/semantic"
)

func TestGenerateLLVMIRLowersStoreSugar(t *testing.T) {
	src := `store PendingGotoStore:
    name_key: u32
    depth: u32

def arena_dict_get[T](m: dict[cstr[key_shape], T]&, key: cstr[key_shape]) -> mutable T&?:
    return null

def arena_dict_put[T](a: mutable Arena&, m: mutable dict[cstr[key_shape], T]&, key: cstr[key_shape], value: T) -> mutable T&?:
    return null

def arena_dict_get_or_insert[T](a: mutable Arena&, m: mutable dict[cstr[key_shape], T]&, key: cstr[key_shape], value: T) -> mutable T&?:
    return null

def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
		pending.reserve(8)
		pending.push(1, 2)
		pending.push(3, 4)
		pending.truncate(1)
        pending.clear()
        values: mutable dict[cstr[key_shape], i64] = zeroed
        slot = values.get_or_insert("seed"):
            base = 5
            base
        _ = slot
        _ = values.entry("name").found
        _ = values.entry("name").value
        _ = values.entry("name").insert(7)
        _ = values.entry("name").get_or_insert(9)
		entry_slot = values.entry("other").get_or_insert():
			11
		_ = entry_slot
        return pending.name_key.count + pending.depth.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_store.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "store.name_key.push.slot") {
		t.Fatalf("expected store push lowering for first column, got:\n%s", output)
	}
	if !strings.Contains(output, "store.depth.push.slot") {
		t.Fatalf("expected store push lowering for second column, got:\n%s", output)
	}
	if !strings.Contains(output, "store.name_key.truncate.count") {
		t.Fatalf("expected store truncate lowering for first column, got:\n%s", output)
	}
	if !strings.Contains(output, "dict.entry.get") {
		t.Fatalf("expected dict entry lookup lowering, got:\n%s", output)
	}
	if !strings.Contains(output, "dict.entry.insert.result") {
		t.Fatalf("expected dict entry insert lowering, got:\n%s", output)
	}
	if !strings.Contains(output, "dict.entry.get_or_insert.result") {
		t.Fatalf("expected dict entry get_or_insert lowering, got:\n%s", output)
	}
}

func TestGenerateLLVMIRLowersSOASugar(t *testing.T) {
	src := `soa SymbolRows:
    name_id: usize
    flags: u32

def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        symbols: mutable SymbolRows = zeroed
		symbols.reserve(4)
		row: RowId[SymbolRows] = symbols.push(12, 3)
        if not symbols.valid(row):
            return 0
        view = symbols[row]
        total: mutable usize = view.name_id + symbols.count
        for iter_row in symbols.rows:
            total <- total + iter_row.name_id
        return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_soa.elisa", src)
	if st, ok := result.NamedTypes["SymbolRows"].(*semantic.StructType); !ok || st == nil || !st.Store || st.StoreDecl == nil || !st.StoreDecl.Soa {
		t.Fatalf("expected SOA declaration to analyze as a column store, got %#v", result.NamedTypes["SymbolRows"])
	}
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "store.name_id.push.slot") {
		t.Fatalf("expected SOA push lowering for first column, got:\n%s", output)
	}
	if !strings.Contains(output, "store.flags.push.slot") {
		t.Fatalf("expected SOA push lowering for second column, got:\n%s", output)
	}
	if !strings.Contains(output, "trunc") {
		t.Fatalf("expected SOA push row id to narrow from usize to u32 storage, got:\n%s", output)
	}
	for _, want := range []string{"soa.row.index", "soa.rows.store", "name_id.row.index"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected SOA row-view lowering to include %q, got:\n%s", want, output)
		}
	}
	for _, want := range []string{"soa.count", "soa.valid"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected SOA table helper lowering to include %q, got:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRLowersStoreRowsIteration(t *testing.T) {
	src := `store PendingGotoStore:
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
        for index, row in enumerate(pending.rows()):
            total <- total + index + row.name_key
        for row in rev(pending.rows()):
            total <- total + row.depth
        return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_store_rows.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, check := range []string{"store.rows.store", "name_key.row.index", "depth.row.index", "enumerate.item.value.insert"} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected store rows lowering to include %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRIgnoresEffectsDeclarations(t *testing.T) {
	src := `effects FrontendEffects = error[Error] can[Abort.Panic, Memory.Allocate]

error Error:
    Bad

def build() -> i64 effects FrontendEffects:
    return 42
`
	result := parseAndAnalyzeBackendTest(t, "backend_effects_decl.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@build") {
		t.Fatalf("expected LLVM output for build, got:\n%s", output)
	}
}

func TestGenerateLLVMIRIgnoresExplicitBundleDeclarations(t *testing.T) {
	src := `bundle SharedArgs explicit:
    value: i64
    extra: i64 = 2

def entry(value: i64) -> i64:
	return value + 2
`
	result := parseAndAnalyzeBackendTest(t, "backend_params_decl.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@entry") {
		t.Fatalf("expected LLVM output for entry, got:\n%s", output)
	}
}

func TestGenerateLLVMIRRejectsGenericKeyRuntimeBackedDictSugar(t *testing.T) {
	src := `def arena_dict_get[K, T](m: dict[K, T]&, key: K) -> mutable T&?:
    return null

def arena_dict_put[K, T](a: mutable Arena&, m: mutable dict[K, T]&, key: K, value: T) -> mutable T&?:
    return null

def arena_dict_get_or_insert[K, T](a: mutable Arena&, m: mutable dict[K, T]&, key: K, value: T) -> mutable T&?:
    return null

def build(owner: Arena, key: u32) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        values: mutable dict[u32, i64] = zeroed
        _ = values.get(key)
        slot = values.get_or_insert(key):
            5
        _ = slot
        _ = values.entry(key).found
        _ = values.entry(key).get_or_insert(7)
        return values.count
`
	l := lexer.New("backend_generic_dict.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) > 0 {
		t.Fatalf("lexer errors:\n%s", strings.Join(errs, "\n"))
	}
	p := parser.New(tokens)
	file := p.ParseFile("backend_generic_dict.elisa")
	if errs := p.Errors(); len(errs) > 0 {
		t.Fatalf("parse errors:\n%s", strings.Join(errs, "\n"))
	}
	result := semantic.Analyze(file)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "runtime-backed dict operations currently support only dict[cstr, V]") {
		t.Fatalf("expected runtime-backed dict restriction diagnostic, got:\n%s", all)
	}
}

func TestGenerateLLVMIRForDoExprBlock(t *testing.T) {
	src := `def build() -> i64:
    value = do:
        base = 5
        base + 7
    return value
`
	result := parseAndAnalyzeBackendTest(t, "backend_do_expr_block.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "store i64 5") {
		t.Fatalf("expected do expression setup statements to lower into IR, got:\n%s", output)
	}
}

func TestGenerateLLVMIRForCallWithDoExprBlockArg(t *testing.T) {
	src := `extern consume(x: i64) -> i64

def build() -> i64:
    value = consume(do:
        base = 5
        base + 7
    )
    return value
`
	result := parseAndAnalyzeBackendTest(t, "backend_do_expr_block_call.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@consume") {
		t.Fatalf("expected do expression call arg lowering to call consume, got:\n%s", output)
	}
}

func TestGenerateLLVMIRForGroupedDoExprBlockForms(t *testing.T) {
	src := `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    values: i64[2] = [do:
        base = 5
        base + 7
    , 9]
    value = consume(do:
        seed = 3
        seed + 1
    , values[1])
    return value
`
	result := parseAndAnalyzeBackendTest(t, "backend_do_expr_block_grouped_forms.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@consume") {
		t.Fatalf("expected grouped do expression forms to lower into consume call, got:\n%s", output)
	}
	if !strings.Contains(output, "store i64 5") {
		t.Fatalf("expected list do expression setup statements to lower into IR, got:\n%s", output)
	}
}

func TestGenerateLLVMIRForNamedFunctionCallArgs(t *testing.T) {
	src := `extern consume(x: i64, y: i64) -> i64

def build() -> i64:
    value = consume(y: 7, x: do:
        seed = 3
        seed + 1
    )
    return value
`
	result := parseAndAnalyzeBackendTest(t, "backend_named_function_call_args.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@consume") {
		t.Fatalf("expected named function call args to lower into consume call, got:\n%s", output)
	}
	if !strings.Contains(output, "store i64 3") {
		t.Fatalf("expected named do expression arg setup statements to lower into IR, got:\n%s", output)
	}
}

func TestGenerateLLVMIRForNamedGenericFunctionCallArgs(t *testing.T) {
	src := `def pick_second[T](first: T, second: T) -> T:
    return second

def build() -> i64:
    return pick_second[i64](second: 7, first: do:
        seed = 3
        seed + 1
    )
`
	result := parseAndAnalyzeBackendTest(t, "backend_named_generic_function_call_args.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "store i64 3") {
		t.Fatalf("expected named generic do expression arg setup statements to lower into IR, got:\n%s", output)
	}
}

func TestGenerateLLVMIRForNamedLocalFunctionAliasCallArgs(t *testing.T) {
	src := `def add(x: i64, y: i64) -> i64:
    return x + y

def build() -> i64:
    runner: func(i64, i64) -> i64 = add
    return runner(y: 7, x: do:
        seed = 3
        seed
    )
`
	result := parseAndAnalyzeBackendTest(t, "backend_named_local_function_alias_call_args.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@add") {
		t.Fatalf("expected named local function alias call to lower into add call, got:\n%s", output)
	}
	if !strings.Contains(output, "store i64 3") {
		t.Fatalf("expected named local alias do expression arg setup statements to lower into IR, got:\n%s", output)
	}
}

func TestGenerateLLVMIRForNamedLocalFieldFunctionAliasCallArgs(t *testing.T) {
	src := `struct CallbackBox:
    run: func(i64, i64) -> i64

def add(x: i64, y: i64) -> i64:
    return x + y

def build() -> i64:
    box: CallbackBox = CallbackBox(add)
    return box.run(y: 7, x: do:
        seed = 3
        seed
    )
`
	result := parseAndAnalyzeBackendTest(t, "backend_named_local_field_function_alias_call_args.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "@add") {
		t.Fatalf("expected named local field function alias call to lower into add call, got:\n%s", output)
	}
	if !strings.Contains(output, "store i64 3") {
		t.Fatalf("expected named local field alias do expression arg setup statements to lower into IR, got:\n%s", output)
	}
}

func TestGenerateLLVMIRForNamedExtensionMethodCallArgs(t *testing.T) {
	src := `struct Box:
    value: i64

impl Box:
    def adjust(self: Box, delta: i64, scale: i64) -> i64:
        return self.value + delta * scale

def build(box: Box) -> i64:
    return box.adjust(scale: 3, delta: do:
        seed = 4
        seed
    )
`
	result := parseAndAnalyzeBackendTest(t, "backend_named_extension_method_call_args.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "__ext__") {
		t.Fatalf("expected named extension method call to lower through mangled extension symbol, got:\n%s", output)
	}
}
