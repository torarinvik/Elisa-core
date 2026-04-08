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
