package semantic_test

import (
	"strings"
	"testing"
)

func TestAnalyzeRejectsPackedMoveAsWithWrongStore(t *testing.T) {
	src := `packed enum Expr:
	Int(value: int)

packed enum Token:
	Ident

def bad(node: Expr, store: Token.Store[Local]) -> int:
	move node in store as Expr.Int(value)
	return value
`
	_, errs := parseAndAnalyze(t, "move_as_packed_wrong_store_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum move-as over \"Expr\" requires store type \"Expr.Store\", got Token.Store[Local]") {
		t.Fatalf("expected packed move-as wrong-store diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsUsingMoveAsEnumBoundReferenceEscapingRegion(t *testing.T) {
	src := `enum Holder:
	Keep(value: i32&, count: i32)

def bad() -> i32&:
	region scratch:
		value: Holder = Holder.Keep(new[scratch] 1, 7)
		move value as Holder.Keep(alias, count)
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_move_enum_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for enum move-as alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsMoveAsEnumScalarLeavingRegion(t *testing.T) {
	src := `enum Holder:
	Keep(value: i32&, count: i32)

def ok() -> i32:
	total: i32 = 0
	region scratch:
		value: Holder = Holder.Keep(new[scratch] 1, 7)
		move value as Holder.Keep(alias, count)
		total = count
	return total
`
	_, errs := parseAndAnalyze(t, "regions_move_enum_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingHelperReturnedReferenceEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def borrow_value(holder: Holder) -> i32&:
	return holder.value

def bad() -> i32&:
	region scratch:
		holder: Holder = Holder{value: new[scratch] 1, count: 7}
		alias: i32& = borrow_value(holder)
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_helper_returned_ref.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for helper-returned reference, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsHelperReturnedScalarLeavingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def borrow_count(holder: Holder) -> i32:
	return holder.count

def ok() -> i32:
	total: i32 = 0
	region scratch:
		holder: Holder = Holder{value: new[scratch] 1, count: 7}
		total = borrow_count(holder)
	return total
`
	_, errs := parseAndAnalyze(t, "regions_helper_returned_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingHelperReturnedNestedViewAliasEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]

def keep_window(window: Window) -> Window:
	return window

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		window: Window = keep_window(Window{items: items[0:2]})
		which: usize = 1
		alias: i32& = window.items[which].value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_helper_nested_view_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for helper-returned nested view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsFreshHelperReturnLeavingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&?
	count: i32

def copy_count(holder: Holder) -> Holder:
	return Holder{value: null, count: holder.count}

def ok() -> i32:
	total: i32 = 0
	region scratch:
		holder: Holder = Holder{value: new[scratch] 1, count: 7}
		copy: Holder = copy_count(holder)
		total = copy.count
	return total
`
	_, errs := parseAndAnalyze(t, "regions_fresh_helper_return_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingExternBorrowReturnedReferenceEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

@borrows_return(holder)
extern borrow_value(holder: Holder) -> i32&

def bad() -> i32&:
	region scratch:
		holder: Holder = Holder{value: new[scratch] 1, count: 7}
		alias: i32& = borrow_value(holder)
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_borrowed_ref.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for extern borrowed reference, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternBorrowReturnedNestedViewAliasEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]

@borrows_return(window)
extern keep_window(window: Window) -> Window

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		window: Window = keep_window(Window{items: items[0:2]})
		which: usize = 1
		alias: i32& = window.items[which].value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_borrowed_nested_view.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for extern borrowed nested view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternPathBorrowReturnedViewAliasEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window) -> view[Holder]

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		window: Window = Window{items: items[0:2], id: 9}
		selected: view[Holder] = get_items(window)
		which: usize = 1
		alias: i32& = selected[which].value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_path_borrowed_view.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for extern path-borrowed view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternPathBorrowReturnedViewScalarLeavingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window) -> view[Holder]

def ok() -> i32:
	total: i32 = 0
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		window: Window = Window{items: items[0:2], id: 9}
		selected: view[Holder] = get_items(window)
		which: usize = 1
		total = selected[which].count
	return total
`
	_, errs := parseAndAnalyze(t, "regions_extern_path_borrowed_view_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingExternRefParamBorrowReturnedViewAliasEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window&) -> view[Holder]

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		window: Window = Window{items: items[0:2], id: 9}
		selected: view[Holder] = get_items((&window).cast[Window&])
		which: usize = 1
		alias: i32& = selected[which].value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_ref_param_view.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for extern ref-param borrowed view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternRefParamBorrowReturnedElementAliasEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items[*])
extern get_item(window: Window&, which: usize) -> Holder

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		window: Window = Window{items: items[0:2], id: 9}
		which: usize = 1
		item: Holder = get_item((&window).cast[Window&], which)
		alias: i32& = item.value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_ref_param_elem.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for extern ref-param borrowed element alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternFieldBorrowReturnedStructFieldEscapingRegion(t *testing.T) {
	src := `struct Box:
	value: i32&

struct Pair:
	left: i32&
	right: i32&

@borrows_return(field, left, left_src.value, right, right_src.value)
extern pair_refs(left_src: Box, right_src: Box) -> Pair

def bad() -> i32&:
	region left_r:
		region right_r:
			left_box: Box = Box{value: new[left_r] 1}
			right_box: Box = Box{value: new[right_r] 2}
			pair: Pair = pair_refs(left_box, right_box)
			return pair.left
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_field_borrow_left.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"left_r\"") {
		t.Fatalf("expected region-escape diagnostic for extern field-borrowed left field, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternRebasedBorrowReturnedSubviewAliasEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

@borrows_return(rebased, items)
extern sub_items(items: view[Holder], start: usize, end: usize) -> view[Holder]

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		view: view[Holder] = items[0:2]
		sub: view[Holder] = sub_items(view, 1, 2)
		alias: i32& = sub[0].value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_rebased_subview.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected region-escape diagnostic for rebased subview alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternRebasedBorrowReturnedSubviewScalarLeavingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

@borrows_return(rebased, items)
extern sub_items(items: view[Holder], start: usize, end: usize) -> view[Holder]

def ok() -> i32:
	total: i32 = 0
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		view: view[Holder] = items[0:2]
		sub: view[Holder] = sub_items(view, 1, 2)
		total = sub[0].count
	return total
`
	_, errs := parseAndAnalyze(t, "regions_extern_rebased_subview_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingExternFieldRebasedBorrowReturnedStructFieldEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct SlicePair:
	items: view[Holder]
	total: i32

@borrows_return(field, rebased, items, src)
extern slice_pair(src: view[Holder], start: usize, end: usize, total: i32) -> SlicePair

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		view: view[Holder] = items[0:2]
		pair: SlicePair = slice_pair(view, 1, 2, 9)
		alias: i32& = pair.items[0].value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_field_rebased.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected field-rebased region-escape diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternFieldRebasedSiblingScalarLeavingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct SlicePair:
	items: view[Holder]
	total: i32

@borrows_return(field, rebased, items, src)
extern slice_pair(src: view[Holder], start: usize, end: usize, total: i32) -> SlicePair

def ok() -> i32:
	out: i32 = 0
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		view: view[Holder] = items[0:2]
		pair: SlicePair = slice_pair(view, 1, 2, 9)
		out = pair.total
	return out
`
	_, errs := parseAndAnalyze(t, "regions_extern_field_rebased_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsExternBorrowsReturnFieldRebasedOnNonStructReturn(t *testing.T) {
	src := `@borrows_return(field, rebased, items, source)
extern bad(source: i32&) -> i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_field_rebased_non_struct_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return(field, …) on extern function \"bad\" requires a concrete struct return type, got i32&") {
		t.Fatalf("expected field-rebased non-struct diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternNestedFieldBorrowReturnedAliasEscapingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Meta:
	items: view[Holder]
	total: i32

struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return(field, meta.items, src)
extern wrap_meta(src: view[Holder], total: i32, tag: i32) -> Wrapper

def bad() -> i32&:
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		view: view[Holder] = items[0:2]
		wrapped: Wrapper = wrap_meta(view, 9, 5)
		alias: i32& = wrapped.meta.items[0].value
		return alias
`
	_, errs := parseAndAnalyze(t, "regions_escape_extern_nested_field.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "cannot return reference: region dependency facts include local region \"scratch\"") {
		t.Fatalf("expected nested field-path region-escape diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternNestedFieldBorrowReturnedSiblingScalarLeavingRegion(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Meta:
	items: view[Holder]
	total: i32

struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return(field, meta.items, src)
extern wrap_meta(src: view[Holder], total: i32, tag: i32) -> Wrapper

def ok() -> i32:
	out: i32 = 0
	region scratch:
		items: array[Holder, 2] = [Holder{value: new[scratch] 1, count: 7}, Holder{value: new[scratch] 2, count: 8}]
		view: view[Holder] = items[0:2]
		wrapped: Wrapper = wrap_meta(view, 9, 5)
		out = wrapped.meta.total + wrapped.tag
	return out
`
	_, errs := parseAndAnalyze(t, "regions_extern_nested_field_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
