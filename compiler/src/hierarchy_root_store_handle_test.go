package main

import "testing"

func TestHierarchyRootStoreHandleCommonField(t *testing.T) {
	runEnumHierarchyProgram(t, "hierarchy_root_store_common.elisa", `
enum Node layout(handle: u32):
    common:
        value: i64
    pass
enum Leaf is Node:
    Item

@test
def bt() -> void:
    region r(4096):
        store: Node.Store[Local] = Node.Store(r)
        in store:
            node: Node = new[store] Leaf.Item(value: 11)
            if node.value != 11:
                panic("hierarchy common field was not stored on the root row")
`)
}
