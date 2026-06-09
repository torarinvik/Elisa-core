package semantic

import "testing"

func TestAnalyzeUnsuffixedIntegerLiteralsInUnsignedContexts(t *testing.T) {
	analyzeFunctionAnalysisTestSource(t, "int_literal_unsigned_contexts.elisa", `
store PendingGotoStore:
	name_key: usize
	depth: usize

def build(owner: Arena, items: darray[usize]) -> usize:
	alloc: mutable Arena& = (&owner).cast[mutable Arena&]
	in alloc:
		pending: mutable PendingGotoStore = zeroed
		pending.push(1, 2)
		total: mutable usize = 0
		index: mutable usize = 0
		if items.count > 0:
			total <- total + items[0]
		for row in pending.rows():
			total <- total + row.name_key + row.depth
		index <- index + 1
		chunk: view[usize] = items[0:1]
		total <- total + chunk[0]
		return total
`)
}
