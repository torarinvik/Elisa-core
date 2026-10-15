package semantic

import "testing"

// A callee analyzed after its caller has no exprTypes yet: a local spelled
// sview @source still carries only program-region bytes (ns10); a plain sview
// of the byte argument is still a store (ns11).
func TestStoreScanProgramViewLocalUnanalyzedCallee(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"accept", `global region source

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
    joined_text: mutable NameStore::Store
    joined_text_heads: mutable darray[u32] = []
    joined_text_rows: mutable darray[sview @source] = []
    joined_text_next: mutable darray[u32] = []
    rows: mutable darray[sview @source] = []
    extra: mutable darray[sview] = []

def caller(table: mutable Table&, rows: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for row in rows |table| -> table:
            row_bytes: mutable darray[u8] = []
            for index in 0..<row.len |row_bytes, row|:
                row_bytes.push(row[index])
            interned: sview @source = intern(row_bytes, table)
            table.rows <- table.rows.push(interned)

def intern(bytes: darray[u8]&, table: mutable Table&) -> sview @source:
    can Memory.Allocate, Abort.Panic:
        hash: mutable u64 = 14695981039346656037.u64()
        for byte in bytes |hash|:
            hash <- (hash ^ byte.u64()) * 1099511628211.u64()
        row: mutable u32 = 4294967295.u32()
        if table.joined_text_heads.count > 0:
            row <- table.joined_text_heads[0]
        found: mutable u32 = 4294967295.u32()
        while row != 4294967295.u32() and found == 4294967295.u32() |row, found, table, bytes|:
            candidate: sview @source = table.joined_text_rows[row.usize()]
            if candidate.len == bytes.count:
                same: mutable bool = true
                for index in 0..<bytes.count |same, candidate, bytes|:
                    same <- false if candidate[index] != bytes[index]
                found <- row if same
            row <- table.joined_text_next[row.usize()]
        return table.joined_text_rows[found.usize()] if found != 4294967295.u32()
        text: sview @source = NameStore::append(&table.joined_text, bytes, 4096)
        next: mutable u32 = 4294967295.u32()
        if table.joined_text_heads.count > 0:
            next <- table.joined_text_heads[0]
        table.joined_text_heads <- table.joined_text_heads.push(table.joined_text_rows.count.u32())
        table.joined_text_rows <- table.joined_text_rows.push(text)
        table.joined_text_next <- table.joined_text_next.push(next)
        return text

`, nil},
		{"leak", `global region source

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
    joined_text: mutable NameStore::Store
    joined_text_heads: mutable darray[u32] = []
    joined_text_rows: mutable darray[sview @source] = []
    joined_text_next: mutable darray[u32] = []
    rows: mutable darray[sview @source] = []
    extra: mutable darray[sview] = []

def caller(table: mutable Table&, rows: darray[sview]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for row in rows |table| -> table:
            row_bytes: mutable darray[u8] = []
            for index in 0..<row.len |row_bytes, row|:
                row_bytes.push(row[index])
            interned: sview @source = intern(row_bytes, table)
            table.rows <- table.rows.push(interned)

def intern(bytes: darray[u8]&, table: mutable Table&) -> sview @source:
    can Memory.Allocate, Abort.Panic:
        hash: mutable u64 = 14695981039346656037.u64()
        for byte in bytes |hash|:
            hash <- (hash ^ byte.u64()) * 1099511628211.u64()
        row: mutable u32 = 4294967295.u32()
        if table.joined_text_heads.count > 0:
            row <- table.joined_text_heads[0]
        found: mutable u32 = 4294967295.u32()
        while row != 4294967295.u32() and found == 4294967295.u32() |row, found, table, bytes|:
            candidate: sview @source = table.joined_text_rows[row.usize()]
            if candidate.len == bytes.count:
                same: mutable bool = true
                for index in 0..<bytes.count |same, candidate, bytes|:
                    same <- false if candidate[index] != bytes[index]
                found <- row if same
            row <- table.joined_text_next[row.usize()]
        return table.joined_text_rows[found.usize()] if found != 4294967295.u32()
        text: sview @source = NameStore::append(&table.joined_text, bytes, 4096)
        next: mutable u32 = 4294967295.u32()
        if table.joined_text_heads.count > 0:
            next <- table.joined_text_heads[0]
        table.joined_text_heads <- table.joined_text_heads.push(table.joined_text_rows.count.u32())
        table.joined_text_rows <- table.joined_text_rows.push(text)
        table.joined_text_next <- table.joined_text_next.push(next)
        leak: sview = bytes.as_sview()
        table.extra <- table.extra.push(leak)
        return text

`, []string{":40:46"}},
	}
	for _, c := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, c.name+".elisa", c.src)
		expectErrorLines(t, result.Errors(), c.want)
	}
}
