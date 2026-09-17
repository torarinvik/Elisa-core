package main

import (
	"strings"
	"testing"
)

// region-return inference, INLINE ENUM PAYLOAD: a function that builds a container locally
// and hands it back inside a value enum's payload (`return Box.Full(tag, items)`) must be
// classified region-polymorphic, so its `__auto_*` region is adopted into the caller's.
//
// Before the fix this shape was NOT classified. The equivalent struct spelling
// (`return Box{tag: tag, items: items}`) was, and worked — the enum one silently read back
// freed memory. The failure mode is the nastiest kind: not a crash. The darray HEADER is
// copied out of the callee by value, so `.count` reads correctly and every ELEMENT reads as
// zero. A caller that trusts `.count` and sums the payload gets a plausible wrong answer.
//
// Under ASan the read-back faults instead, which is what this gate detects.
const enumPayloadLocalReturnBody = `
enum Box:
    Empty
    Full(tag: i64, items: darray[i64])

def rebuild(n: usize) -> Box:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        items: mutable darray[i64] = []
        i: mutable usize = 0
        while i < n:
            items.push(i.i64() * 2)
            i <- i + 1
        return Box.Full(7, items)

@test
def enum_payload_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        box: Box = rebuild(50000)
        match box:
            Box.Full(tag, items):
                if tag != 7:
                    panic("enum payload lost its scalar field")
                if items.count != 50000:
                    panic("enum payload darray lost its count")
                sum: mutable i64 = 0
                for v in items:
                    sum <- sum + v
                if sum != 2499950000:
                    panic("enum payload darray elements corrupted (UAF?)")
                if items[0] != 0 or items[49999] != 99998:
                    panic("enum payload darray boundary corrupted")
            _:
                panic("rebuild returned the wrong variant")
`

func TestEnumPayloadLocalReturnAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "enum_payload_local_return_uaf", enumPayloadLocalReturnBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "enum_payload_local_return_lives")
}

// The same escape one level deeper: the payload container is built by a nested struct that the
// enum wraps. Pins that the classifier walks the constructor ARGUMENTS, not just their spelling —
// a struct literal holding a region-fed local, handed to an inline enum constructor.
const enumPayloadStructLocalReturnBody = `
struct Rows:
    values: darray[i64]

enum Table:
    Missing
    Present(name: sview, rows: Rows)

def build(n: usize) -> Table:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        values: mutable darray[i64] = []
        i: mutable usize = 0
        while i < n:
            values.push(i.i64() + 1)
            i <- i + 1
        return Table.Present("t", Rows{values: values})

@test
def enum_payload_struct_local_return_lives() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        table: Table = build(20000)
        match table:
            Table.Present(name, rows):
                if rows.values.count != 20000:
                    panic("nested struct payload lost its count")
                sum: mutable i64 = 0
                for v in rows.values:
                    sum <- sum + v
                if sum != 200010000:
                    panic("nested struct payload elements corrupted (UAF?)")
            _:
                panic("build returned the wrong variant")
`

func TestEnumPayloadStructLocalReturnAdoptedNoUAF(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "enum_payload_struct_local_return_uaf", enumPayloadStructLocalReturnBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "enum_payload_struct_local_return_lives")
}
