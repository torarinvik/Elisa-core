package backend

import (
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersStoreRowDestructuring(t *testing.T) {
	src := `layout soa struct PendingGotoStore:
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
            let {name_key, depth} = row
            total <- total + name_key + depth
        for {name_key, depth} in pending.rows():
            total <- total + name_key + depth
        return total
`
	result := parseAndAnalyzeBackendTest(t, "backend_store_row_destructure.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, want := range []string{"let.destructure.", "iter.destructure.row.", "name_key.row.index", "depth.row.index"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected store-row destructuring lowering to include %q, got:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRLowersWithArenaScopedAllocatorShorthand(t *testing.T) {
	src := `def build() -> usize:
    can Memory.Allocate:
        with arena scratch(4096) as owner:
            xs: darray[int] = [1, 2, 3]
            return xs.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_with_arena_scoped_allocator.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, want := range []string{"@new_region", "@arena_alloc", "darray.literal.alloc"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected scoped arena shorthand lowering to include %q, got:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRLowersStructLiteralSpread(t *testing.T) {
	src := `struct Accessors:
    read_name_id: i64?
    write_name_id: i64?
    default_enabled: bool

def update(base: Accessors, name: i64) -> bool:
    next: Accessors = Accessors{...base, read_name_id: name, default_enabled: true}
    return next.default_enabled
`
	result := parseAndAnalyzeBackendTest(t, "backend_struct_literal_spread.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, want := range []string{"ins", "default_enabled"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected struct literal spread lowering to include %q, got:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRLowersStructFieldDefaults(t *testing.T) {
	src := `struct Accessors:
    read_name_id: i64? = null
    write_name_id: i64? = null
    default_enabled: bool = false
    count: i64 = 7

def build() -> i64:
    next: Accessors = Accessors{count: 9}
    return next.count
`
	result := parseAndAnalyzeBackendTest(t, "backend_struct_field_defaults.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, want := range []string{"%Accessors = type", "zeroinitializer", "i64 9", "count"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected struct field default lowering to include %q, got:\n%s", want, output)
		}
	}
}

func TestGenerateLLVMIRLowersTypeNamedConstructorDefaults(t *testing.T) {
	src := `struct Pair:
    left: i64
    right: i64 = 7

def Pair(left: i64, right: i64 = 9) -> Pair:
    return Pair{left: left, right: right}

def build() -> i64:
    from_ctor: Pair = Pair(3)
    from_literal: Pair = Pair{left: 4}
    return from_ctor.right + from_literal.right
`
	result := parseAndAnalyzeBackendTest(t, "backend_type_named_constructor_defaults.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRWithDefaultPackedLoweringForTest returned error: %v", err)
	}
	for _, want := range []string{"@Pair", "i64 9", "i64 7"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected type-named constructor/default lowering to include %q, got:\n%s", want, output)
		}
	}
}
