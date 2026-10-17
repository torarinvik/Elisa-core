package semantic

import "testing"

const recordElementTypes = `struct RecordOwner:
    value: u32
struct RecordBinding:
    owner: RecordOwner
    serial: u64
struct RecordTable:
    rows: mutable darray[RecordBinding]
`

func TestSMTRecordElementCongruence(t *testing.T) {
	for _, source := range []string{
		`def same(rows: darray[RecordBinding]&, i: usize, j: usize) -> u32:
    requires i < rows.count and j < rows.count and i == j
    ensure result == rows[j].owner.value
    return rows[i].owner.value
`,
		`def copied(rows: darray[RecordBinding]&, i: usize) -> u32:
    requires i < rows.count
    ensure result == rows[i].owner.value
    row: RecordBinding = rows[i]
    return row.owner.value
`,
		`def nested(table: RecordTable&, i: usize, j: usize) -> u64:
    requires i < table.rows.count and j < table.rows.count and i == j
    ensure result == table.rows[j].serial
    return table.rows[i].serial
`,
		`def quantified(rows: darray[RecordBinding]&, i: usize) -> u32:
    requires i < rows.count
    ensure forall j in 0..<rows.count: j == i implies result == rows[j].owner.value
    return rows[i].owner.value
`,
	} {
		result := analyzeContractStrict(t, "record_elements.elisa", recordElementTypes+source)
		if len(result.Errors()) != 0 {
			t.Fatalf("record element congruence declined:\n%s", allDiagnostics(result))
		}
	}
}

func TestSMTRecordElementsDoNotOverclaim(t *testing.T) {
	result := analyzeContractStrict(t, "record_elements_wrong.elisa", recordElementTypes+`def other(rows: darray[RecordBinding]&, other_rows: darray[RecordBinding]&) -> u32:
    requires rows.count > 0 and other_rows.count > 0
    ensure result == other_rows[0].owner.value
    return rows[0].owner.value
def index(rows: darray[RecordBinding]&) -> u32:
    requires rows.count > 1
    ensure result == rows[1].owner.value
    return rows[0].owner.value
def field(rows: darray[RecordBinding]&) -> u64:
    requires rows.count > 0
    ensure result == rows[0].serial
    return rows[0].owner.value.u64()
def mutated(rows: mutable darray[RecordBinding]&) -> u32:
    requires rows.count > 0
    ensure result == rows[0].owner.value
    before: u32 = rows[0].owner.value
    rows[0] <- RecordBinding{owner: RecordOwner{value: 0}, serial: 0}
    return before
`)
	for _, function := range []string{"other", "index", "field", "mutated"} {
		if ensureFailuresFor(result.Errors(), function) == 0 {
			t.Fatalf("%s: false record claim was accepted:\n%s", function, allDiagnostics(result))
		}
	}
}
