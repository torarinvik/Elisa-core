package semantic_test

import (
	"strings"
	"testing"

	"elisacore/src/semantic"
)

func TestHierarchyRootStoreHandleSeparatesPlainStoreFromPacked(t *testing.T) {
	result, errs := parseAndAnalyze(t, "hierarchy_root_store_handle.elisa", `enum Node layout(handle: u32):
    common:
        value: i64
    pass
enum Leaf is Node:
    Item

def read(node: Node, store: Node.Store[Local]) -> i64:
    match node in store:
        Leaf.Item:
            return node.value
`)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	root, ok := result.NamedTypes["Node"].(*semantic.EnumType)
	if !ok || root == nil {
		t.Fatalf("expected Node enum type, got %#v", result.NamedTypes["Node"])
	}
	if root.SourcePacked {
		t.Fatal("layout(handle:) hierarchy store must retain its non-Packed source declaration")
	}
	if !root.Packed || !root.StoreBackedPlain || root.RecursivePlain {
		t.Fatalf("expected a plain, non-recursive hierarchy store using the backend carrier, got packed=%v store-backed=%v recursive=%v", root.Packed, root.StoreBackedPlain, root.RecursivePlain)
	}
	if root.StoreType == nil {
		t.Fatal("layout(handle:) hierarchy root must expose its synthesized Store type")
	}
	leaf, ok := result.NamedTypes["Leaf"].(*semantic.EnumType)
	if !ok || leaf == nil || !leaf.StoreBackedPlain || leaf.SourcePacked {
		t.Fatalf("hierarchy refinement must share the root's plain store representation, got %#v", result.NamedTypes["Leaf"])
	}
}

func TestHierarchyHandleOnlyDoesNotSynthesizeStore(t *testing.T) {
	_, errs := parseAndAnalyze(t, "hierarchy_handle_without_store.elisa", `enum Node layout(handle: u32): pass
enum Leaf is Node:
    Item

def read(node: Node, store: Node.Store[Local]) -> i64:
    return 0
`)
	if len(errs) == 0 {
		t.Fatal("a handle width alone must not silently synthesize a store for an inline hierarchy")
	}
}

func TestHierarchyStorePromotionDoesNotEnablePackedProfileAnnotation(t *testing.T) {
	_, errs := parseAndAnalyze(t, "hierarchy_store_not_source_packed.elisa", `@packed_profile(canonical)
enum Node layout(handle: u32):
    common:
        value: i64
    pass
enum Leaf is Node:
    Item
`)
	if len(errs) == 0 {
		t.Fatal("plain hierarchy store promotion must not enable source-Packed-only annotations")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@packed_profile on enum \"Node\" requires a packed enum") {
		t.Fatalf("expected source-level packed diagnostic, got %v", errs)
	}
}
