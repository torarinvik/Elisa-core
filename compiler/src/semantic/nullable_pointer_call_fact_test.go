//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestNullablePointerValueFactSurvivesPointeeCall(t *testing.T) {
	src := `
extern touch(p: mutable void&?) -> u8 can[Unsafe.RawExtern]
def probe(p: mutable void&?) -> u8 can[Unsafe.RawExtern]:
    ensure p != null or result == 3
    return 3 if p == null
    status: mutable u8 = 0
    trusted Unsafe.RawExtern:
        status <- touch(p)
    return 1
`
	r := analyzeContractStrict(t, "nullable_pointer_pointee_call.elisa", src)
	if errs := r.Errors(); len(errs) != 0 {
		t.Fatalf("a pointee call cannot rebind the pointer value: %v", errs)
	}
}

func TestNullablePointerPointeeFactDoesNotSurviveCall(t *testing.T) {
	src := `
extern touch(p: mutable i64&) -> u8 can[Unsafe.RawExtern]
def probe(p: mutable i64&) -> i64 can[Unsafe.RawExtern]:
    requires p == 7
    ensure result == 7
    status: mutable u8 = 0
    trusted Unsafe.RawExtern:
        status <- touch(p)
    return p
`
	r := analyzeContractStrict(t, "nullable_pointer_pointee_negative.elisa", src)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("mutation of the referent must invalidate its value fact: %v", r.Errors())
	}
}

func TestNullablePointerSlotMutationInvalidatesNullness(t *testing.T) {
	src := `
def clear[T](slot: mutable T&) -> void:
    pass
def probe(input: mutable void&?) -> bool:
    ensure result
    p: mutable void&? = input
    return true if p == null
    clear(&p)
    return p != null
`
	r := analyzeContractStrict(t, "nullable_pointer_slot_negative.elisa", src)
	if !strings.Contains(strings.Join(r.Errors(), "\n"), "could not be proven statically") {
		t.Fatalf("an unknown pointer value cannot establish this false contract: %v", r.Errors())
	}
}
