//go:build cgo

package semantic

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"elisacore/src/ast"
)

// Exact pre-cut admission predicate. This oracle intentionally retains the
// quadratic walk, independently of the candidate's interval lookup.
func quadraticSpecializationSyntaxValidation(s *specializationSyntax) error {
	if s.err != nil {
		return s.err
	}
	var ranges []*specializationSlice
	for _, r := range s.slices {
		if r.typ.Elem().Size() != 0 {
			ranges = append(ranges, r)
		}
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
	var end uintptr
	for _, r := range ranges {
		if r.start < end {
			return fmt.Errorf("specialization syntax has unsupported partially overlapping slice backing")
		}
		end = r.start + uintptr(r.capacity)*r.typ.Elem().Size()
		for pointer := range s.scanned {
			if pointer.typ.Kind() == reflect.Pointer && pointer.typ.Elem() == r.typ.Elem() && pointer.ptr >= r.start && pointer.ptr < end && pointer.ptr >= r.start+uintptr(r.length)*r.typ.Elem().Size() {
				return fmt.Errorf("specialization syntax has an interior pointer beyond visible slice views")
			}
		}
	}
	return nil
}

func TestSpecializationSyntaxIntervalAdmissionParity(t *testing.T) {
	// Address-only census fixtures never dereference these synthetic addresses.
	// Cover equal starts, adjacent/disjoint/partial overlap, zero-sized backing,
	// different element types, visible/tail/boundary/misaligned pointers and maps.
	sliceTypes := []reflect.Type{reflect.TypeFor[[]ast.Ident](), reflect.TypeFor[[]ast.IntLit](), reflect.TypeFor[[]struct{}]()}
	pointerTypes := []reflect.Type{reflect.TypeFor[*ast.Ident](), reflect.TypeFor[*ast.IntLit](), reflect.TypeFor[*struct{}](), reflect.TypeFor[map[string]int]()}
	random := rand.New(rand.NewSource(9217))
	for trial := 0; trial < 2048; trial++ {
		syntax := newSpecializationSyntax()
		for i := 0; i < 1+trial%8; i++ {
			typ := sliceTypes[random.Intn(len(sliceTypes))]
			start := uintptr(1024 + i*128)
			capacity := 1 + random.Intn(8)
			syntax.slices[specializationPointer{typ, start}] = &specializationSlice{typ: typ, start: start, capacity: capacity, length: random.Intn(capacity + 1)}
		}
		for i := 0; i < 1+trial%17; i++ {
			typ := pointerTypes[random.Intn(len(pointerTypes))]
			address := uintptr(1023 + random.Intn(2300))
			syntax.scanned[specializationPointer{typ, address}] = true
		}
		expected, actual := quadraticSpecializationSyntaxValidation(syntax), syntax.validate()
		if fmt.Sprint(expected) != fmt.Sprint(actual) {
			t.Fatalf("trial %d changed diagnostic: old=%v new=%v", trial, expected, actual)
		}
	}
}

func TestSpecializationSyntaxIntervalMixedInvalidDiagnosticPrecedence(t *testing.T) {
	typ := reflect.TypeFor[[]ast.Ident]()
	pointer := reflect.TypeFor[*ast.Ident]()
	size := typ.Elem().Size()
	for _, test := range []struct {
		name        string
		hiddenRange int
		want        string
	}{
		{"hidden_before_later_overlap", 0, "beyond visible"},
		{"hidden_in_overlapping_range", 2, "partially overlapping"},
		{"hidden_after_overlap", 3, "partially overlapping"},
	} {
		t.Run(test.name, func(t *testing.T) {
			syntax := newSpecializationSyntax()
			starts := []uintptr{1024, 1024 + 10*size, 1024 + 11*size, 1024 + 20*size}
			for _, start := range starts {
				syntax.slices[specializationPointer{typ, start}] = &specializationSlice{typ: typ, start: start, capacity: 3, length: 1}
			}
			syntax.scanned[specializationPointer{pointer, starts[test.hiddenRange] + 2*size}] = true
			old, new := quadraticSpecializationSyntaxValidation(syntax), syntax.validate()
			if fmt.Sprint(old) != fmt.Sprint(new) || new == nil || !strings.Contains(new.Error(), test.want) {
				t.Fatalf("diagnostic precedence changed: old=%v new=%v expected=%s", old, new, test.want)
			}
		})
	}
}

func TestSpecializationSyntaxIntervalBoundaryClasses(t *testing.T) {
	typ := reflect.TypeFor[[]ast.Ident]()
	size := typ.Elem().Size()
	start := uintptr(4096)
	for _, test := range []struct {
		name    string
		typ     reflect.Type
		address uintptr
		reject  bool
	}{
		{"before", reflect.TypeFor[*ast.Ident](), start - 1, false},
		{"visible_start", reflect.TypeFor[*ast.Ident](), start, false},
		{"visible_interior", reflect.TypeFor[*ast.Ident](), start + size, false},
		{"first_hidden", reflect.TypeFor[*ast.Ident](), start + 2*size, true},
		{"misaligned_hidden", reflect.TypeFor[*ast.Ident](), start + 2*size + 1, true},
		{"exclusive_end", reflect.TypeFor[*ast.Ident](), start + 4*size, false},
		{"other_type_hidden", reflect.TypeFor[*ast.IntLit](), start + 2*size, false},
		{"map_address_hidden", reflect.TypeFor[map[string]int](), start + 2*size, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			syntax := newSpecializationSyntax()
			syntax.slices[specializationPointer{typ, start}] = &specializationSlice{typ: typ, start: start, capacity: 4, length: 2}
			// Zero-sized slice metadata neither overlaps nor captures a pointer.
			zero := reflect.TypeFor[[]struct{}]()
			syntax.slices[specializationPointer{zero, start + size}] = &specializationSlice{typ: zero, start: start + size, capacity: 100, length: 0}
			syntax.scanned[specializationPointer{test.typ, test.address}] = true
			old, new := quadraticSpecializationSyntaxValidation(syntax), syntax.validate()
			if (old != nil) != test.reject || (new != nil) != test.reject {
				t.Fatalf("boundary admission changed: old=%v new=%v reject=%v", old, new, test.reject)
			}
		})
	}
}

func TestSpecializationSyntaxIntervalRevalidatesVisibleClosureGrowth(t *testing.T) {
	backing := make([]ast.Ident, 3, 20)
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(backing[:1]))
	if err := syntax.validate(); err != nil {
		t.Fatal(err)
	}
	syntax.prepare(reflect.ValueOf(&backing[2]))
	if err := syntax.validate(); err == nil || !strings.Contains(err.Error(), "beyond visible") {
		t.Fatalf("new hidden pointer bypassed revalidation: %v", err)
	}
	syntax.prepare(reflect.ValueOf(backing[:3]))
	if err := syntax.validate(); err != nil {
		t.Fatal("visible same-start union was not reconsidered", err)
	}
	pointer := syntax.copy(reflect.ValueOf(&backing[2])).Interface().(*ast.Ident)
	private := syntax.copy(reflect.ValueOf(backing)).Interface().([]ast.Ident)
	if pointer != &private[2] || pointer == &backing[2] {
		t.Fatal("source alias/interior identity changed")
	}
	// A new shifted view remains unsupported even after prior successful validation.
	syntax.prepare(reflect.ValueOf(backing[1:]))
	if err := syntax.validate(); err == nil || !strings.Contains(err.Error(), "partially overlapping") {
		t.Fatalf("new overlapping view bypassed validation: %v", err)
	}
}

func BenchmarkSpecializationSyntaxValidationIntervals(b *testing.B) {
	syntax := newSpecializationSyntax()
	typ := reflect.TypeFor[[]ast.Ident]()
	pointer := reflect.TypeFor[*ast.Ident]()
	size := typ.Elem().Size()
	for i := 0; i < 2048; i++ {
		address := uintptr(1024) + uintptr(i)*size*4
		syntax.slices[specializationPointer{typ, address}] = &specializationSlice{typ: typ, start: address, capacity: 3, length: 3}
		syntax.scanned[specializationPointer{pointer, address + size}] = true
	}
	for _, test := range []struct {
		name     string
		validate func(*specializationSyntax) error
	}{{"quadratic", quadraticSpecializationSyntaxValidation}, {"interval", func(s *specializationSyntax) error { return s.validate() }}} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := test.validate(syntax); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
