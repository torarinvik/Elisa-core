package semantic

import "testing"

const recordElementTypes = `struct RecordOwner:
    value: u32
struct RecordBinding:
    owner: RecordOwner
    serial: u64
struct RecordTable:
    rows: mutable darray[RecordBinding]
struct RecordFlag:
    known: bool
def equal_record(left: RecordBinding, right: RecordBinding) -> bool:
    ensure result == (left.owner.value == right.owner.value and left.serial == right.serial)
    return left.owner.value == right.owner.value and left.serial == right.serial
def overwrite_record(table: RecordTable) -> bool:
    requires table.rows.count > 0
    table.rows[0] <- RecordBinding{owner: RecordOwner{value: 0}, serial: 0}
    return true
def recursive_overwrite(table: RecordTable, depth: usize) -> bool:
    requires table.rows.count > 0
    return recursive_overwrite(table, depth - 1) if depth > 0
    table.rows[0] <- RecordBinding{owner: RecordOwner{value: 0}, serial: 0}
    return true
`

func TestSMTRecordElementCongruence(t *testing.T) {
	for _, source := range []string{
		`def same(rows: darray[RecordBinding]&, i: usize, j: usize) -> u32:
    requires i < rows.count and j < rows.count and i == j
    ensure result == rows[j].owner.value
    return rows[i].owner.value
`,
		`def same_through_call(rows: darray[RecordBinding]&, i: usize, j: usize) -> bool:
    requires i < rows.count and j < rows.count and i == j
    ensure result == true
    return equal_record(rows[i], rows[j])
`,
		`def same_flag(rows: darray[RecordFlag]&, i: usize, j: usize) -> bool:
    requires i < rows.count and j < rows.count and i == j
    ensure result == rows[j].known
    return rows[i].known
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
		`def local_record_frame(table: RecordTable&, i: usize) -> u32:
    requires i < table.rows.count
    ensure result == table.rows[i].owner.value
    selected: mutable RecordBinding = RecordBinding{owner: RecordOwner{value: 0}, serial: 0}
    assert i < table.rows.count
    for step in 0..<1 |selected|:
        selected <- table.rows[i]
    return table.rows[i].owner.value
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
def shadowed(rows: darray[RecordBinding], other_rows: darray[RecordBinding]) -> u32:
    requires rows.count > 0 and other_rows.count > 0
    ensure result == rows[0].owner.value
    if true:
        rows: darray[RecordBinding] = other_rows
        return rows[0].owner.value
    return 0
def shadowed_table(table: RecordTable, other_table: RecordTable) -> u32:
    requires table.rows.count > 0 and other_table.rows.count > 0
    ensure result == table.rows[0].owner.value
    if true:
        table: RecordTable = other_table
        return table.rows[0].owner.value
    return 0
def unequal_call(rows: darray[RecordBinding]&) -> bool:
    requires rows.count > 1
    ensure result == true
    return equal_record(rows[0], rows[1])
def unequal_flag(rows: darray[RecordFlag]&) -> bool:
    requires rows.count > 1
    ensure result == rows[1].known
    return rows[0].known
def called_mutator(table: RecordTable) -> u32:
    requires table.rows.count > 0
    ensure result == table.rows[0].owner.value
    before: u32 = table.rows[0].owner.value
    changed: bool = overwrite_record(table)
    return before
def called_recursive_mutator(table: RecordTable) -> u32:
    requires table.rows.count > 0
    ensure result == table.rows[0].owner.value
    before: u32 = table.rows[0].owner.value
    changed: bool = recursive_overwrite(table, 1)
    return before
`)
	for _, function := range []string{"other", "index", "field", "mutated", "shadowed", "shadowed_table", "unequal_call", "unequal_flag", "called_mutator", "called_recursive_mutator"} {
		if ensureFailuresFor(result.Errors(), function) == 0 {
			t.Fatalf("%s: false record claim was accepted:\n%s", function, allDiagnostics(result))
		}
	}
}

func TestSMTRecordElementTupleResult(t *testing.T) {
	result := analyzeContractStrict(t, "record_tuple.elisa", recordElementTypes+`def candidate(table: RecordTable&, owner: RecordOwner) -> bool:
    return table.rows.count > 0 and table.rows[0].owner.value == owner.value
def selected(table: RecordTable&, owner: RecordOwner) -> (known: bool, binding_id: RecordBinding):
    requires table.rows.count > 0 and table.rows[0].owner.value == owner.value
    ensure result.known and result.binding_id.owner.value == owner.value
    chosen: RecordBinding = table.rows[0]
    assert chosen.owner.value == owner.value
    assert candidate(table, owner)
    return (true, chosen)
`)
	if len(result.Errors()) != 0 {
		t.Fatalf("tuple record projection declined:\n%s", allDiagnostics(result))
	}
}
