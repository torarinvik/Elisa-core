package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeArenaArrayViewHelpersCarryElementTypes(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_view_len[T](view: dview[T]) -> usize:
	return view.len

def arena_da_view_slice[T](view: dview[T], start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	return view

def arena_da_view_get[T](view: dview[T], index: usize) -> T:
	_ = view
	_ = index
	return zeroed

def use(values: darray[i32, row]&) -> i32:
	view: dview[i32] = arena_da_view(values, 0, values.count)
	sub: dview[i32] = arena_da_view_slice(view, 0, 1)
	if arena_da_view_len(sub) > 0:
		return arena_da_view_get(sub, 0)
	return 0
`
	_, errs := parseAndAnalyze(t, "arena_array_view_helpers.elisa", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeArenaArrayFromViewReturnsFreshShape(t *testing.T) {
	src := `struct Arena:
	begin: mutable void&?
	end: mutable void&?

struct DynArray[T]:
	items: mutable T&?
	count: mutable usize
	capacity: mutable usize

struct DynArrayView:
	data: mutable void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: darray[T, shape_in]&, start: usize, end: usize) -> dview[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[void&], values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_from_view[T](a: Arena&, view: dview[T]) -> darray[T, shape_out]:
	_ = a
	_ = view
	return zeroed

def bad(a: Arena&, values: darray[i32, row]&) -> darray[i32, row]:
	view: dview[i32] = arena_da_view(values, 0, values.count)
	return arena_da_from_view(a, view)
`
	_, errs := parseAndAnalyze(t, "arena_array_from_view_fresh_shape.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects darray[i32, row], got darray[i32, shape_out#") || !strings.Contains(all, "note: arena_da_from_view returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh arena from-view diagnostic, got:\n%s", all)
	}
}
