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
	_, errs := parseAndAnalyze(t, "move_as_packed_wrong_store_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "packed enum move-as over \"Expr\" requires store type \"Expr.Store\", got Token.Store[Local]") {
		t.Fatalf("expected packed move-as wrong-store diagnostic, got:\n%s", all)
	}
}
func TestAnalyzeRejectsUsingMoveAsEnumBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `enum Holder:
	Keep(value: i32&, count: i32)

def bad() -> i32:
	region scratch
	mark scratch as cp
	value: Holder = Holder.Keep(new[scratch] 1, 7)
	move value as Holder.Keep(alias, count)
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_enum_alias.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for enum move-as alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsMoveAsEnumScalarAfterRestore(t *testing.T) {
	src := `enum Holder:
	Keep(value: i32&, count: i32)

def ok() -> i32:
	region scratch
	mark scratch as cp
	value: Holder = Holder.Keep(new[scratch] 1, 7)
	move value as Holder.Keep(alias, count)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_enum_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingHelperReturnedReferenceInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def borrow_value(holder: Holder) -> i32&:
	return holder.value

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1, 7)
	alias: i32& = borrow_value(holder)
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_helper_returned_ref_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for helper-returned reference, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsHelperReturnedScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def borrow_count(holder: Holder) -> i32:
	return holder.count

def ok() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1, 7)
	count: i32 = borrow_count(holder)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_helper_returned_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingHelperReturnedNestedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]

def keep_window(window: Window) -> Window:
	return window

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = keep_window(Window(items[0:2]))
	which: usize = 1
	alias: i32& = window.items[which].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_helper_nested_view_alias_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for helper-returned nested view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsFreshHelperReturnAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&?
	count: i32

def copy_count(holder: Holder) -> Holder:
	return Holder(null, holder.count)

def ok() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1, 7)
	copy: Holder = copy_count(holder)
	restore scratch from cp
	return copy.count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_fresh_helper_return_ok.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingExternBorrowReturnedReferenceInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

@borrows_return(holder)
extern borrow_value(holder: Holder) -> i32&

def bad() -> i32:
	region scratch
	mark scratch as cp
	holder: Holder = Holder(new[scratch] 1, 7)
	alias: i32& = borrow_value(holder)
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_borrowed_ref_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern borrowed reference, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternBorrowReturnedNestedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]

@borrows_return(window)
extern keep_window(window: Window) -> Window

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = keep_window(Window(items[0:2]))
	which: usize = 1
	alias: i32& = window.items[which].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_borrowed_nested_view_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern borrowed nested view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternPathBorrowReturnedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window) -> view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = Window(items[0:2], 9)
	selected: view[Holder] = get_items(window)
	which: usize = 1
	alias: i32& = selected[which].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_path_borrowed_view_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern path-borrowed view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternPathBorrowReturnedViewScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window) -> view[Holder]

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = Window(items[0:2], 9)
	selected: view[Holder] = get_items(window)
	which: usize = 1
	count: i32 = selected[which].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_path_borrowed_view_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingExternRefParamBorrowReturnedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items)
extern get_items(window: Window&) -> view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = Window(items[0:2], 9)
	selected: view[Holder] = get_items((&window).cast[Window&])
	which: usize = 1
	alias: i32& = selected[which].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_ref_param_view_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern ref-param borrowed view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternRefParamBorrowReturnedElementAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]
	id: i32

@borrows_return(window.items[*])
extern get_item(window: Window&, which: usize) -> Holder

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = Window(items[0:2], 9)
	which: usize = 1
	item: Holder = get_item((&window).cast[Window&], which)
	alias: i32& = item.value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_ref_param_elem_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern ref-param borrowed element alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternFieldBorrowReturnedStructFieldInvalidatedByRestore(t *testing.T) {
	src := `struct Box:
	value: i32&

struct Pair:
	left: i32&
	right: i32&

@borrows_return_field(left, left_src.value, right, right_src.value)
extern pair_refs(left_src: Box, right_src: Box) -> Pair

def bad() -> i32:
	region left_r
	region right_r
	mark left_r as left_cp
	mark right_r as right_cp
	left_box: Box = Box(new[left_r] 1)
	right_box: Box = Box(new[right_r] 2)
	pair: Pair = pair_refs(left_box, right_box)
	restore left_r from left_cp
	return pair.left[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_borrow_left_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"pair.left\" cannot be used: region dependency facts were invalidated by restore of region \"left_r\" from checkpoint \"left_cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for extern field-borrowed left field, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsUsingExternFieldBorrowReturnedSiblingFieldAfterUnrelatedRestore(t *testing.T) {
	src := `struct Box:
	value: i32&

struct Pair:
	left: i32&
	right: i32&

@borrows_return_field(left, left_src.value, right, right_src.value)
extern pair_refs(left_src: Box, right_src: Box) -> Pair

def ok() -> i32:
	region left_r
	region right_r
	mark left_r as left_cp
	mark right_r as right_cp
	left_box: Box = Box(new[left_r] 1)
	right_box: Box = Box(new[right_r] 2)
	pair: Pair = pair_refs(left_box, right_box)
	restore left_r from left_cp
	return pair.right[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_borrow_right_ok.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingExternRebasedBorrowReturnedSubviewAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

@borrows_return_rebased(items)
extern sub_items(items: view[Holder], start: usize, end: usize) -> view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	sub: view[Holder] = sub_items(view, 1, 2)
	alias: i32& = sub[0].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_rebased_subview_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected rebased subview invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternRebasedBorrowReturnedSubviewScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

@borrows_return_rebased(items)
extern sub_items(items: view[Holder], start: usize, end: usize) -> view[Holder]

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	sub: view[Holder] = sub_items(view, 1, 2)
	count: i32 = sub[0].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_rebased_subview_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingExternFieldRebasedBorrowReturnedStructFieldInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct SlicePair:
	items: view[Holder]
	total: i32

@borrows_return_field_rebased(items, src)
extern slice_pair(src: view[Holder], start: usize, end: usize, total: i32) -> SlicePair

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	pair: SlicePair = slice_pair(view, 1, 2, 9)
	alias: i32& = pair.items[0].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_rebased_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected field-rebased invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternFieldRebasedSiblingScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct SlicePair:
	items: view[Holder]
	total: i32

@borrows_return_field_rebased(items, src)
extern slice_pair(src: view[Holder], start: usize, end: usize, total: i32) -> SlicePair

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	pair: SlicePair = slice_pair(view, 1, 2, 9)
	restore scratch from cp
	return pair.total
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_field_rebased_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsExternBorrowsReturnFieldRebasedOnNonStructReturn(t *testing.T) {
	src := `@borrows_return_field_rebased(items, source)
extern bad(source: i32&) -> i32&
`
	_, errs := parseAndAnalyze(t, "extern_function_field_rebased_non_struct_reject.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return_field_rebased on extern function \"bad\" requires a concrete struct return type, got i32&") {
		t.Fatalf("expected field-rebased non-struct diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingExternNestedFieldBorrowReturnedAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Meta:
	items: view[Holder]
	total: i32

struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field(meta.items, src)
extern wrap_meta(src: view[Holder], total: i32, tag: i32) -> Wrapper

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	wrapped: Wrapper = wrap_meta(view, 9, 5)
	alias: i32& = wrapped.meta.items[0].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_invalid.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected nested field-path invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternNestedFieldBorrowReturnedSiblingScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Meta:
	items: view[Holder]
	total: i32

struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field(meta.items, src)
extern wrap_meta(src: view[Holder], total: i32, tag: i32) -> Wrapper

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	wrapped: Wrapper = wrap_meta(view, 9, 5)
	restore scratch from cp
	return wrapped.meta.total + wrapped.tag
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_scalar_ok.llcontext", src)
	requireNoErrors(t, errs)
}
