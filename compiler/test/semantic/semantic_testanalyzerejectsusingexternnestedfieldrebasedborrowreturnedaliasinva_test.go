package semantic_test

import (
	"elisacore/src/semantic"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeRejectsUsingExternNestedFieldRebasedBorrowReturnedAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Meta:
	items: view[Holder]
	total: i32

struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Holder], start: usize, end: usize, total: i32, tag: i32) -> Wrapper

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	wrapped: Wrapper = wrap_submeta(view, 1, 2, 9, 5)
	alias: i32& = wrapped.meta.items[0].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_rebased_invalid.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected nested field-path rebased invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsExternNestedFieldRebasedSiblingScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Meta:
	items: view[Holder]
	total: i32

struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field_rebased(meta.items, src)
extern wrap_submeta(src: view[Holder], start: usize, end: usize, total: i32, tag: i32) -> Wrapper

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	view: view[Holder] = items[0:2]
	wrapped: Wrapper = wrap_submeta(view, 1, 2, 9, 5)
	restore scratch from cp
	return wrapped.meta.total + wrapped.tag
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_extern_nested_field_rebased_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsExternBorrowsReturnFieldOnUnknownNestedReturnPath(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Meta:
	items: view[Holder]
	total: i32

struct Wrapper:
	meta: Meta
	tag: i32

@borrows_return_field(meta.missing, src)
extern wrap_meta(src: view[Holder]) -> Wrapper
`
	_, errs := parseAndAnalyze(t, "extern_function_nested_field_path_reject.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "@borrows_return_field on extern function \"wrap_meta\" references unknown return field path \"meta.missing\" in Wrapper") {
		t.Fatalf("expected nested return-field-path diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingMoveAsPackedVariantBoundReferenceInvalidatedByRestore(t *testing.T) {
	src := `packed enum Expr:
	Hold(value: i32&, count: i32)

def bad(owner: Arena) -> i32:
	region scratch
	mark scratch as cp
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Hold(value: new[scratch] 1, count: 7)
	move node in store as Expr.Hold(alias, count)
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_packed_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for packed move-as alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsMoveAsPackedVariantScalarAfterRestore(t *testing.T) {
	src := `packed enum Expr:
	Hold(value: i32&, count: i32)

def ok(owner: Arena) -> i32:
	region scratch
	mark scratch as cp
	store: Expr.Store[Local] = Expr.Store(owner)
	node: Expr = new[store] Expr.Hold(value: new[scratch] 1, count: 7)
	move node in store as Expr.Hold(alias, count)
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_packed_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingIndexedStructFieldAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	alias: i32& = items[0].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_indexed_struct_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for indexed struct alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsIndexedStructScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	count: i32 = items[0].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_indexed_struct_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingSlicedViewAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: view[Holder] = items[0:2]
	alias: i32& = window[0].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_sliced_view_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for sliced-view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsSlicedViewScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: view[Holder] = items[0:2]
	count: i32 = window[0].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_sliced_view_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingNestedStructViewAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]

def bad() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = Window(items[0:2])
	which: usize = 1
	alias: i32& = window.items[which].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_nested_struct_view_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for nested struct view alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsNestedStructViewScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

struct Window:
	items: view[Holder]

def ok() -> i32:
	region scratch
	mark scratch as cp
	items: array[Holder, 2] = [Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)]
	window: Window = Window(items[0:2])
	which: usize = 1
	count: i32 = window.items[which].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_nested_struct_view_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsUsingMoveAsEnumBoundIndexedAliasInvalidatedByRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

enum Bucket:
	Keep(items: array[Holder, 2])

def bad() -> i32:
	region scratch
	mark scratch as cp
	value: Bucket = Bucket.Keep([Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)])
	move value as Bucket.Keep(items)
	which: usize = 1
	alias: i32& = items[which].value
	restore scratch from cp
	return alias[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalid_move_enum_indexed_alias.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"alias\" cannot be used: region dependency facts were invalidated by restore of region \"scratch\" from checkpoint \"cp\"") {
		t.Fatalf("expected restore invalidation diagnostic for move-as enum indexed alias, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsMoveAsEnumBoundIndexedScalarAfterRestore(t *testing.T) {
	src := `struct Holder:
	value: i32&
	count: i32

enum Bucket:
	Keep(items: array[Holder, 2])

def ok() -> i32:
	region scratch
	mark scratch as cp
	value: Bucket = Bucket.Keep([Holder(new[scratch] 1, 7), Holder(new[scratch] 2, 8)])
	move value as Bucket.Keep(items)
	which: usize = 1
	count: i32 = items[which].count
	restore scratch from cp
	return count
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_move_enum_indexed_scalar_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeRejectsRestoringCheckpointFromWrongRegion(t *testing.T) {
	src := `def bad() -> void:
	region left
	region right
	mark left as cp
	restore right from cp
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_wrong_region.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "checkpoint \"cp\" belongs to region \"left\", not \"right\"") {
		t.Fatalf("expected wrong-region checkpoint diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsRestoringResetCheckpoint(t *testing.T) {
	src := `def bad() -> void:
	region scratch
	mark scratch as cp
	reset scratch
	restore scratch from cp
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_reset_checkpoint.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "checkpoint \"cp\" is invalid after reset of region \"scratch\"") {
		t.Fatalf("expected invalid-checkpoint diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsUsingReferenceInvalidatedByReset(t *testing.T) {
	src := `def bad() -> i32:
	region scratch
	value: i32& = new[scratch] 1
	reset scratch
	return value[0]
`
	_, errs := parseAndAnalyze(t, "manual_regions_reset_invalid_ref.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "reference \"value\" cannot be used: region dependency facts were invalidated by reset of region \"scratch\"") {
		t.Fatalf("expected reset invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeRejectsRestoringCheckpointInvalidatedByEarlierRestore(t *testing.T) {
	src := `def bad() -> void:
	region scratch
	mark scratch as outer
	mark scratch as inner
	restore scratch from outer
	restore scratch from inner
`
	_, errs := parseAndAnalyze(t, "manual_regions_restore_invalidated_checkpoint.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "checkpoint \"inner\" is invalid after restore of region \"scratch\" from checkpoint \"outer\"") {
		t.Fatalf("expected nested-checkpoint invalidation diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeAcceptsFrontendStressFixture(t *testing.T) {
	repoRoot := repoRootFromTestFile(t)
	src := loadSourceWithIncludes(t, filepath.Join(repoRoot, "Code", "test_programs", "frontend_stress.elisa"), map[string]bool{})
	_, errs := parseAndAnalyze(t, "frontend_stress.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsErrorDeclarationsAndTryRecovery(t *testing.T) {
	src := `error MemoryError:
	OutOfMemory

error IoError:
	NotFound

extern alloc(size: usize) -> heap void&?
extern read_file(path: u8&) -> cstr[file_text] error[IoError.NotFound, ...]

def checked_alloc(size: usize) -> heap void& error[MemoryError.OutOfMemory, ...]:
	ptr: heap void& = alloc(size) else raise MemoryError.OutOfMemory
	return ptr

def load_text(path: u8&) -> cstr[file_text] error[IoError.NotFound, ...]:
	text: cstr[file_text] = try read_file(path)
	return text

def load_with_fallback(path: u8&) -> u8&:
	text: u8& = try read_file(path) else "" as u8&
	return text
`
	_, errs := parseAndAnalyze(t, "error_handling_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsErrorSetWideningAndTagRaise(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error AppError:
	PermissionDenied
	Busy
	NotFound

error OneTagError:
	NotFound

extern read_value() -> int error[FileError.NotFound, ...]

def bubble() -> int error[AppError.NotFound, ...]:
	return try read_value()

def fail_now() -> int error[OneTagError.NotFound]:
	raise FileError.NotFound
`
	_, errs := parseAndAnalyze(t, "error_set_widening_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsWildcardErrorSetShorthand(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

extern read_value() -> int error[FileError]

def bubble() -> int error[FileError, ...]:
	return try read_value()
`
	_, errs := parseAndAnalyze(t, "error_set_wildcard_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsMultiFamilyErrorComposition(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	NotFound

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def load_any(use_disk: bool) -> int error[FileError, NetworkError]:
	if use_disk:
		return try read_disk()
	return try read_network()
`
	_, errs := parseAndAnalyze(t, "error_multi_family_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeAcceptsMixedRowStyleFamilyExpansion(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def load_any(use_disk: bool) -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	if use_disk:
		return try read_disk()
	return try read_network()

def fail_disk() -> int error[FileError.NotFound, NetworkError.Timeout, ...]:
	raise FileError.PermissionDenied
`
	_, errs := parseAndAnalyze(t, "error_mixed_row_expansion_ok.elisa", src)
	requireNoErrors(t, errs)
}
func TestAnalyzeCanonicalizesEquivalentErrorSetSpellings(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

def by_full_subset() -> int error[FileError.NotFound, FileError.PermissionDenied]:
	return 1

def by_reverse_family_order() -> int error[NetworkError, FileError]:
	return 2
`
	result, errs := parseAndAnalyze(t, "error_canonicalization.elisa", src)
	requireNoErrors(t, errs)

	fullSubset, ok := result.GlobalScope.Lookup("by_full_subset")
	if !ok {
		t.Fatal("expected by_full_subset symbol")
	}
	fullSubsetFn, ok := fullSubset.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", fullSubset.Type)
	}
	if fullSubsetFn.Return.String() != "int | FileError" {
		t.Fatalf("expected canonical single-family return type, got %s", fullSubsetFn.Return.String())
	}

	reversed, ok := result.GlobalScope.Lookup("by_reverse_family_order")
	if !ok {
		t.Fatal("expected by_reverse_family_order symbol")
	}
	reversedFn, ok := reversed.Type.(*semantic.FuncType)
	if !ok {
		t.Fatalf("expected function type, got %T", reversed.Type)
	}
	if reversedFn.Return.String() != "int | error[FileError, NetworkError]" {
		t.Fatalf("expected canonical multi-family return type, got %s", reversedFn.Return.String())
	}
}
func TestAnalyzeRejectsAmbiguousCrossFamilyRaiseIntoMultiFamilySet(t *testing.T) {
	src := `error LegacyError:
	NotFound

error FileError:
	NotFound

error NetworkError:
	NotFound

def fail() -> int error[FileError, NetworkError]:
	raise LegacyError.NotFound
`
	_, errs := parseAndAnalyze(t, "error_multi_family_ambiguous_raise.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise cannot propagate tag \"LegacyError.NotFound\" into error[FileError, NetworkError]") {
		t.Fatalf("expected ambiguous multi-family raise diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
func TestAnalyzeCanonicalizesRaiseDestinationInDiagnostics(t *testing.T) {
	src := `error LegacyError:
	Busy

error FileError:
	NotFound

error NetworkError:
	Disconnected

def fail() -> int error[NetworkError, FileError]:
	raise LegacyError.Busy
`
	_, errs := parseAndAnalyze(t, "error_canonical_raise_diag.elisa", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	if !strings.Contains(strings.Join(errs, "\n"), "raise cannot propagate tag \"LegacyError.Busy\" into error[FileError, NetworkError]") {
		t.Fatalf("expected canonical raise destination diagnostic, got:\n%s", strings.Join(errs, "\n"))
	}
}
