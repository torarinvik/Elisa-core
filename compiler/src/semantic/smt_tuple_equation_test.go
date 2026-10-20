//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

// A recursive ghost fold returning a named tuple is read through its body, and two calls of it
// with provably equal arguments agree, so a loop invariant that tracks the fold preserves.
func TestSMTNamedTupleEquationPreservesFoldInvariant(t *testing.T) {
	const fold = `
ghost def fold_from(text: sview, position: usize, total: i64) -> (valid: bool, value: i64):
    requires position <= text.len
    decreases text.len - position
    return (true, total) if position == text.len
    return (false, 0) if text[position] > 9 or total > 1000
    return fold_from(text, position + 1, total + text[position].i64())
`
	for _, tc := range []struct {
		name, step string
		proven     bool
	}{
		{"same_fold", "total + text[position].i64()", true},
		{"different_step", "total + text[position].i64() + 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := fold + `
def sum(text: sview) -> (valid: bool, value: i64):
    position: mutable usize = 0
    total: mutable i64 = 0
    while position < text.len |position, text, total|:
        decreases text.len - position
        invariant position <= text.len
        invariant total >= 0 and total <= 1009
        invariant fold_from(text, position, total).valid == fold_from(text, 0, 0).valid
        if text[position] > 9 or total > 1000:
            return (false, 0)
        total <- ` + tc.step + `
        position <- position + 1
    return (true, total)
`
			r := analyzeContractStrict(t, "tuple_equation_"+tc.name+".elisa", src)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the fold invariant to prove: %s", es)
			}
			if !tc.proven && es == "" {
				t.Fatal("an invariant over a different fold step was accepted")
			}
		})
	}
}

// A body-level assert whose quantifier the parser lowered to a runtime loop changes no state,
// so the sequential model keeps the body and the loop measure proves.
func TestLoopMeasureAdmitsQuantifiedBodyAssert(t *testing.T) {
	src := `
def ok(x: u8) -> bool:
    return x < 10

def scan(text: sview) -> bool:
    position: mutable usize = 0
    while position < text.len |position, text|:
        decreases text.len - position
        if not ok(text[position]):
            return false
        position <- position + 1
        assert position <= text.len or (forall k in 0..<position: ok(text[k]))
    return true
`
	r := analyzeContractStrict(t, "quantified_body_assert.elisa", src)
	if es := strings.Join(r.Errors(), "\n"); es != "" {
		t.Fatalf("expected the loop measure to prove: %s", es)
	}
}

// An element store keeps the array's length, so a loop that writes elements of a mutable
// array parameter preserves an invariant about its count.
func TestLoopElementStoreKeepsArrayLength(t *testing.T) {
	src := `
def clear(xs: mutable darray[i64]&) -> void can[Abort.Panic]:
    n: usize = xs.count
    i: mutable usize = 0
    while i < n |xs, n, i|:
        invariant xs.count == n
        xs[i] <- 0
        i <- i + 1
`
	r := analyzeContractStrict(t, "element_store_length.elisa", src)
	if es := strings.Join(r.Errors(), "\n"); es != "" {
		t.Fatalf("expected the count invariant to prove: %s", es)
	}
}

func TestSMTStoreBaseArray(t *testing.T) {
	for in, want := range map[string]string{
		"v_xs":               "v_xs",
		"(store v_xs v_i 0)": "v_xs",
		"(store (store |a b| (+ i 1) 2) i (f x))": "|a b|",
		"(select v_xs 0)":                         "(select v_xs 0)",
	} {
		if got := smtStoreBaseArray(in); got != want {
			t.Errorf("smtStoreBaseArray(%q) = %q, want %q", in, got, want)
		}
	}
}

// A ghost prefix fold over containers held in an immutable record parameter unfolds at the call
// site: the callee's container fields are the caller's, a const-enum field is its storage value,
// and calls with provably equal arguments agree. A wrong claim about the fold still fails.
func TestSMTRecordContainerPrefixFold(t *testing.T) {
	const decls = `
const enum Kind of u8:
    Struct
    Other

struct Sym:
    kind: Kind
    name: sview

struct Table:
    symbols: darray[Sym]
    names: darray[sview]

ghost def count_prefix(table: Table&, name: sview, end: usize) -> u8:
    requires end <= table.symbols.count
    decreases end
    return 0 if end == 0
    prior_end: usize = end - 1
    matches_name: bool = table.symbols[prior_end].kind == Kind.Struct and table.symbols[prior_end].name == name
    return 2 if:
        count_prefix(table, name, prior_end) == 2
        count_prefix(table, name, prior_end) == 1 and matches_name
    return 1 if matches_name
    return count_prefix(table, name, prior_end)

ghost def member_prefix(table: Table&, name: sview, end: usize) -> bool:
    requires end <= table.names.count
    decreases end
    return false if end == 0
    prior_end: usize = end - 1
    return true if table.names[prior_end] == name
    return member_prefix(table, name, prior_end)
`
	for _, tc := range []struct {
		name, claim string
		proven      bool
	}{
		{"fold", "count_prefix(table, name, table.symbols.count) == 1 and member_prefix(table, name, table.names.count)", true},
		{"wrong_count", "count_prefix(table, name, table.symbols.count) == 2 and member_prefix(table, name, table.names.count)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + `
def unique(table: Table&, name: sview) -> bool:
    ensure result == (` + tc.claim + `)
    can Abort.Panic:
        declarations: mutable u8 = 0
        symbol_count: usize = table.symbols.count
        for symbol_index in 0..<symbol_count |table, name, symbol_count, declarations|:
            invariant symbol_count == table.symbols.count
            invariant symbol_index <= symbol_count
            invariant declarations == count_prefix(table, name, symbol_index)
            symbol_matches: bool = table.symbols[symbol_index].kind == Kind.Struct and table.symbols[symbol_index].name == name
            declarations <- 2 if declarations == 2 or (declarations == 1 and symbol_matches) else (1 if symbol_matches else declarations)
        registered: mutable bool = false
        n: usize = table.names.count
        for i in 0..<n |table, name, n, registered|:
            invariant n == table.names.count
            invariant i <= n
            invariant registered == member_prefix(table, name, i)
            registered <- registered or table.names[i] == name
        return declarations == 1 and registered
`
			r := analyzeContractStrict(t, "record_prefix_"+tc.name+".elisa", src)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the record prefix fold to prove: %s", es)
			}
			if !tc.proven && !strings.Contains(es, "ensure postcondition") {
				t.Fatalf("a wrong claim about the fold was accepted: %s", es)
			}
		})
	}
}

// A `continue` in a range loop returns to the head after the index increment, without the
// rest of the body: a counter bounded by the index preserves, one claimed equal to it does not.
func TestRangeLoopContinueIncrementsIndex(t *testing.T) {
	for _, tc := range []struct {
		name, inv string
		proven    bool
	}{
		{"bounded", "count <= i", true},
		{"equal", "count == i", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := `
def count_nonnegative(xs: darray[i64]&) -> usize:
    count: mutable usize = 0
    for i in 0..<xs.count |xs, count|:
        invariant ` + tc.inv + `
        continue if xs[i] < 0
        count <- count + 1
    return count
`
			r := analyzeContractStrict(t, "range_continue_"+tc.name+".elisa", src)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the invariant to prove: %s", es)
			}
			if !tc.proven && es == "" {
				t.Fatal("an invariant broken by the continue path was accepted")
			}
		})
	}
}

// A whole-record store into an array element is read field by field: the stored index reads the
// stored value's fields, every other index the pre-body element's. A store that breaks the
// quantified invariant (a wrong value, or the wrong index) is still rejected.
func TestLoopRecordElementStorePreservesFieldInvariant(t *testing.T) {
	const decls = `
struct F:
    id: u32
    known: bool

def mk(id: u32) -> F:
    ensure not result.known
    return F{id: id, known: false}
`
	for _, tc := range []struct {
		name, store string
		proven      bool
	}{
		{"call", "facts[index] <- mk(fact.id)", true},
		{"literal", "facts[index] <- F{id: fact.id, known: false}", true},
		{"wrong_value", "facts[index] <- F{id: fact.id, known: true}", false},
		{"wrong_index", "facts[0] <- mk(fact.id)", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + `
def clear(facts: mutable darray[F]&) -> void can[Abort.Panic]:
    n: usize = facts.count
    for index in 0..<n |facts, n|:
        invariant facts.count == n
        invariant forall k in 0..<index: not facts[k].known
        fact: F = facts[index]
        ` + tc.store + `
`
			r := analyzeContractStrict(t, "record_store_"+tc.name+".elisa", src)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the record-store invariant to prove: %s", es)
			}
			if !tc.proven && es == "" {
				t.Fatal("a store breaking the invariant was accepted")
			}
		})
	}
}

// A path that continues past an element read `table.pos[index]` had the index in bounds, so an
// invariant bounding a later-recorded index by that container's count preserves. Reading a
// different element gives no such bound.
func TestLoopElementReadBoundsLaterPath(t *testing.T) {
	const src = `struct Owner:
    value: u32

struct Id:
    owner: Owner
    serial: u32

struct Pos:
    offset: u32
    end_offset: u32

struct Table:
    ids: darray[Id]
    pos: darray[Pos]

def ids_equal(a: Id, b: Id) -> bool:
    return a.owner.value == b.owner.value and a.serial == b.serial

def find(table: Table&, owner: Owner, position: Pos) -> usize:
    lookup_valid: mutable bool = true
    found: mutable bool = false
    result_index: mutable usize = 0
    for index in 0..<table.ids.count |index, table, owner, position, found, result_index|:
        invariant not found or (
            result_index < table.pos.count and
            result_index < table.ids.count and
            table.ids[result_index].owner.value == owner.value and
            table.pos[result_index].offset == position.offset)
        id: Id = table.ids[index]
        continue if id.owner.value != owner.value
        recorded: Pos = table.pos[index]
        continue if recorded.offset != position.offset
        if found and not ids_equal(table.ids[result_index], id):
            lookup_valid <- false
        if not found:
            result_index <- index
            found <- true
            assert table.ids[result_index].owner.value == owner.value
            assert table.pos[result_index].offset == position.offset
    return result_index if lookup_valid else 0
`
	for _, tc := range []struct {
		name, from, to string
		proven         bool
	}{
		{"read_bounds", "", "", true},
		{"other_element", "recorded: Pos = table.pos[index]", "recorded: Pos = table.pos[0]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := src
			if tc.from != "" {
				body = strings.Replace(body, tc.from, tc.to, 1)
			}
			r := analyzeContractStrict(t, "read_bounds_"+tc.name+".elisa", body)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the invariant to prove: %s", es)
			}
			if !tc.proven && es == "" {
				t.Fatal("an invariant without a read bound was accepted")
			}
		})
	}
}

// A void-exit postcondition holds when an assert spelled the same way (a range quantifier in
// either its quantifier or its lowered runtime-loop form) was proven and nothing has written its
// roots since. A write or a push after the assert retires it. A quantified assert in the loop
// body changes no state, so the record-store body is still captured.
func TestVoidEnsureFromLiveAssertFact(t *testing.T) {
	const decls = `
struct F:
    id: u32
    known: bool

def mk(id: u32) -> F:
    ensure not result.known
    return F{id: id, known: false}
`
	for _, tc := range []struct {
		name, after string
		proven      bool
	}{
		{"assert_live", "", true},
		{"stored_after", "    if facts.count > 0:\n        facts[0] <- F{id: 0, known: true}\n", false},
		{"pushed_after", "    facts.push(F{id: 0, known: true})\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := decls + `
def clear(facts: mutable darray[F]&) -> void can[Abort.Panic]:
    ensure facts.count == 0 or (forall k in 0..<facts.count: not facts[k].known)
    n: usize = facts.count
    for index in 0..<n |facts, n|:
        invariant facts.count == n
        invariant forall k in 0..<index: not facts[k].known
        fact: F = facts[index]
        facts[index] <- mk(fact.id)
        assert forall k in 0..<(index + 1.usize()): not facts[k].known
    assert facts.count == 0 or (forall k in 0..<facts.count: not facts[k].known)
` + tc.after
			r := analyzeContractStrict(t, "assert_fact_ensure_"+tc.name+".elisa", src)
			es := strings.Join(r.Errors(), "\n")
			if tc.proven && es != "" {
				t.Fatalf("expected the postcondition to prove: %s", es)
			}
			if !tc.proven && !strings.Contains(es, "ensure postcondition") {
				t.Fatalf("a postcondition retired by a later write was accepted: %s", es)
			}
		})
	}
}

// Elements of an integer array lie in their type's range: a u8 element is below 256, and
// nothing more (not below 255, and an i8 element may be negative).
func TestSMTArrayElementTypeRange(t *testing.T) {
	r := analyzeContractStrict(t, "array_element_range.elisa", `def ok(xs: darray[u8]&, i: usize) -> bool:
    requires i < xs.count
    ensure result
    return xs[i].u32() < 256

def bad(xs: darray[u8]&, i: usize) -> bool:
    requires i < xs.count
    ensure result
    return xs[i].u32() < 255

def sbad(xs: darray[i8]&, i: usize) -> bool:
    requires i < xs.count
    ensure result
    return xs[i].i32() >= 0
`)
	es := strings.Join(r.Errors(), "\n")
	if strings.Contains(es, `"ok"`) {
		t.Fatalf("expected the element range to prove: %s", es)
	}
	if !strings.Contains(es, `"bad"`) || !strings.Contains(es, `"sbad"`) {
		t.Fatalf("an element claim beyond the type range was accepted: %s", es)
	}
}
