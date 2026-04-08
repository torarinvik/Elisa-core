package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersStoreSugar(t *testing.T) {
	src := `store PendingGotoStore:
    name_key: u32
    depth: u32

def arena_dict_get[T](m: any dict[dstr[key_shape], T]&, key: dstr[key_shape]) -> mutable any T&?:
    return null

def arena_dict_put[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape], value: T) -> mutable any T&?:
    return null

def arena_dict_get_or_insert[T](a: mutable any Arena&, m: mutable any dict[dstr[key_shape], T]&, key: dstr[key_shape], value: T) -> mutable any T&?:
    return null

def build(owner: Arena) -> usize:
    alloc: mutable any Arena& = (&owner).cast[mutable any Arena&]
    in alloc:
        pending: mutable PendingGotoStore = zeroed
        pending.reserve(8u)
        pending.push(1u32, 2u32)
        pending.push(3u32, 4u32)
        pending.truncate(1u)
        pending.clear()
        values: mutable dict[dstr[key_shape], i64] = zeroed
        slot = values.get_or_insert("seed"):
            base = 5
            base
        _ = slot
        _ = values.entry("name").found
        _ = values.entry("name").value
        _ = values.entry("name").insert(7)
        _ = values.entry("name").get_or_insert(9)
        return pending.name_key.count + pending.depth.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_store.llcontext", src)
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

func TestGenerateLLVMIRForDoExprBlock(t *testing.T) {
	src := `def build() -> i64:
    value = do:
        base = 5
        base + 7
    return value
`
	result := parseAndAnalyzeBackendTest(t, "backend_do_expr_block.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_do_expr_block_call.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_do_expr_block_grouped_forms.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_named_function_call_args.llcontext", src)
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
	result := parseAndAnalyzeBackendTest(t, "backend_named_generic_function_call_args.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "store i64 3") {
		t.Fatalf("expected named generic do expression arg setup statements to lower into IR, got:\n%s", output)
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
	result := parseAndAnalyzeBackendTest(t, "backend_named_extension_method_call_args.llcontext", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	if !strings.Contains(output, "__ext__") {
		t.Fatalf("expected named extension method call to lower through mangled extension symbol, got:\n%s", output)
	}
}
