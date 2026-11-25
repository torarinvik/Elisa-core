package main

import (
	"os/exec"
	"strings"
	"testing"
)

// A loop-carried value rebuilt by a region-polymorphic call must outlive the iteration.
// When the call result reaches the assignment through a value-forwarding expression
// (`get f() else …`, a value `if`), the loop body was still given a per-iteration region:
// the table grown in one iteration lived in an arena the next iteration reset, so the
// next growth was allocated over the table it was copying from (a silent wrong sum).
const regionPolyLoopCarriedSource = `
struct Table:
    slots: mutable darray[u64]
    count: mutable u32

def empty_slots(capacity: u32) -> darray[u64] can[Memory.Allocate]:
    slots: mutable darray[u64] = []
    while slots.count < capacity |slots, capacity|:
        slots.push(0)
    slots

def grown(table: Table&) -> Table can[Abort.Panic, Memory.Allocate]:
    bigger: mutable Table = Table{slots: empty_slots(table.slots.count.u32() * 2), count: table.count}
    for index in 0..<table.slots.count |bigger, table|:
        bigger.slots[index] <- table.slots[index]
    bigger

def table_set(table: mutable Table, value: u64) -> Table can[Abort.Panic, Memory.Allocate]:
    current: mutable Table = table
    if current.count + 1 > current.slots.count.u32():
        current <- grown(&current)
    current.slots[current.count] <- value
    current.count <- current.count + 1
    current

def enter(table: mutable Table, step: u64) -> Table can[Abort.Panic, Memory.Allocate]:
    values: mutable Table = table
    for index in 0..<2 |values, step|:
        values <- table_set(values, step * 10 + index.u64())
    values

def maybe_enter(table: mutable Table, step: u64) -> Table? can[Abort.Panic, Memory.Allocate]:
    return null if step == 1000
    enter(table, step)

# Unused slots stay zero, so the sum over every slot is the sum of the entries.
def total(values: Table&) -> u64 can[Abort.Panic]:
    sum: mutable u64 = 0
    for slot in values.slots |sum|:
        sum <- sum + slot
    sum

def SHAPE(start: Table, steps: u64) -> u64 can[Abort.Panic, Memory.Allocate]:
    values: mutable Table = start
    step: mutable u64 = 0
    while step < steps |values, step|:
        values <- REBUILD
        step <- step + 1
    total(&values)

@test
def SHAPE_test() -> void:
    can Memory.Allocate, Abort.Panic:
        if SHAPE(Table{slots: empty_slots(2), count: 0}, 20) != 3820:
            panic("loop-carried table lost entries")
`

func TestRegionPolyLoopCarriedValueOutlivesIteration(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not available")
	}
	shapes := map[string]string{
		"get_else_return": "get maybe_enter(values, step) else return 0",
		"get_else_value":  "get maybe_enter(values, step) else values",
		"value_if":        "enter(values, step) if step < 1000 else values",
	}
	for shape, rebuild := range shapes {
		t.Run(shape, func(t *testing.T) {
			src := strings.ReplaceAll(strings.ReplaceAll(regionPolyLoopCarriedSource, "REBUILD", rebuild), "SHAPE", shape)
			runSingleTestProgramExpectOK(t, shape+"_test", src, "a loop-carried value lived in the per-iteration region")
		})
	}
}
