package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeArenaArrayViewHelpersCarryElementTypes(t *testing.T) {
	src := `repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> view[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&](), values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_view_len[T](view: view[T]) -> usize:
	return view.len

def arena_da_view_slice[T](view: view[T], start: usize, end: usize) -> view[T]:
	_ = start
	_ = end
	return view

def arena_da_view_get[T](view: view[T], index: usize) -> T:
	_ = view
	_ = index
	return zeroed

def use(values: any darray[i32, row]&) -> i32:
	view: view[i32] = arena_da_view(values, 0u, values.count)
	sub: view[i32] = arena_da_view_slice(view, 0u, 1u)
	if arena_da_view_len(sub) > 0u:
		return arena_da_view_get(sub, 0u)
	return 0
`
	_, errs := parseAndAnalyze(t, "arena_array_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeArenaArrayFromViewReturnsFreshShape(t *testing.T) {
	src := `repr(c) struct Arena:
	begin: mutable any void&?
	end: mutable any void&?

repr(c) struct DynArray[T]:
	items: mutable any T&?
	count: mutable usize
	capacity: mutable usize

repr(c) struct DynArrayView:
	data: mutable any void&?
	len: mutable usize
	elem_size: mutable usize

def arena_da_view[T](values: any darray[T, shape_in]&, start: usize, end: usize) -> view[T]:
	_ = start
	_ = end
	if values.items != null:
		return DynArrayView(values.items.cast[any void&](), values.count, sizeof(T))
	return DynArrayView(null, 0, sizeof(T))

def arena_da_from_view[T](a: any Arena&, view: view[T]) -> darray[T, shape_out]:
	_ = a
	_ = view
	return zeroed

def bad(a: any Arena&, values: any darray[i32, row]&) -> darray[i32, row]:
	view: view[i32] = arena_da_view(values, 0u, values.count)
	return arena_da_from_view(a, view)
`
	_, errs := parseAndAnalyze(t, "arena_array_from_view_fresh_shape.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects darray[i32, row], got darray[i32, shape_out#") || !strings.Contains(all, "note: arena_da_from_view returns a fresh logical shape for shape_out") {
		t.Fatalf("expected fresh arena from-view diagnostic, got:\n%s", all)
	}
}
