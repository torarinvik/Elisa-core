package semantic

import "testing"

// A region parameter carried only by program-lived arguments (an append-only name store inside
// a program-lived table) binds to the program region. Binding never sanctions a store of a
// shorter-lived value: a callee storing its `sview @r` into program storage is still rejected.
func TestProgramLivedRegionArgBinding(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"program_lived_store_arg", `global region source

module NameStore:
    @append_only
    struct Store:
        private:
            current: mutable darray[u8] @source
            retired: mutable darray[darray[u8] @source] @source

    def new_store() -> Store:
        return Store{current: [], retired: []}

    def make_room[@r](store: mutable Store& @r, needed: usize, chunk: usize) -> void:
        return if store.current.count + needed <= store.current.capacity
        store.retired.push(store.current)
        store.current <- []
        store.current.reserve(needed if needed > chunk else chunk)

    def append[@r](store: mutable Store& @r, bytes: darray[u8]&, chunk: usize) -> sview @source:
        make_room(store, bytes.count, chunk)
        start: usize = store.current.count
        for index in 0..<bytes.count |store, bytes|:
            store.current.push(bytes[index])
        return ""

struct Table:
    joined: mutable NameStore::Store
    rows: mutable darray[sview @source] @source = []

def intern(bytes: darray[u8]&, table: lmut Table) -> sview @source:
    can Memory.Allocate, Abort.Panic:
        text: sview @source = NameStore::append(&table.joined, bytes, 4096)
        table.rows <- table.rows.push(text)
        return text
`, nil},
		{"region_param_view_into_program_rows", `global region source

struct Rows:
    names: mutable darray[sview @source] @source = []

def keep[@r](rows: mutable Rows& @r, n: sview @r) -> void:
    can Memory.Allocate, Abort.Panic:
        rows.names.push(n)

def caller(rows: lmut Rows) -> void:
    can Memory.Allocate, Abort.Panic:
        d: mutable dstr = []
        d.push(65)
        keep(rows, d.as_sview())
`, []string{":8:25", ":8:25"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
