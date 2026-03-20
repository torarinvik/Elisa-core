package semantic_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeStage1StringViewWrappersSupportBoundedViews(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

def string_view(value: any u8&?, start: i64, end: i64) -> StringView:
	return StringView("", 0)

def string_view_len(view: StringView) -> i64:
	return view.len

def string_view_index(view: StringView, index: i64) -> i64:
	return -1

def string_view_copy(view: StringView) -> any u8&:
	return view.data

def ctx_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_len(view: StringView) -> i64:
	return string_view_len(view)

def ctx_string_view_index(view: StringView, index: i64) -> i64:
	return string_view_index(view, index)

def ctx_string_from_view(view: StringView) -> dstr[shape_out]:
	return string_view_copy(view)

def probe(text: dstr[row]) -> i64:
	view: StringView = ctx_string_view(text, 0, 2)
	_ = ctx_string_view_index(view, 0)
	return ctx_string_view_len(view)

def bad(text: dstr[row]) -> dstr[row]:
	view: StringView = ctx_string_view(text, 0, 2)
	return ctx_string_from_view(view)
`
	_, errs := parseAndAnalyze(t, "stage1_string_view_wrappers.llcontext", src)
	if len(errs) == 0 {
		t.Fatal("expected semantic error, got none")
	}
	all := strings.Join(errs, "\n")
	if !strings.Contains(all, "return type expects dstr[row], got dstr[shape_out#") || !strings.Contains(all, "note: ctx_string_from_view returns a fresh logical shape for shape_out") {
		t.Fatalf("expected bounded string view fresh-shape diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeStage1StringViewHelpersAcceptSubviewAndEquality(t *testing.T) {
	src := `repr(c) struct StringView:
	data: mutable any u8&
	len: mutable i64

def string_view(value: any u8&?, start: i64, end: i64) -> StringView:
	return StringView("", 0)

def string_view_len(view: StringView) -> i64:
	return view.len

def string_view_slice(view: StringView, start: i64, end: i64) -> StringView:
	return view

def string_view_eq(view: StringView, other: any u8&?) -> int:
	return 1

def string_views_eq(lhs: StringView, rhs: StringView) -> int:
	return 1

def ctx_string_view(value: dstr[shape_in], start: i64, end: i64) -> StringView:
	return string_view(value, start, end)

def ctx_string_view_len(view: StringView) -> i64:
	return string_view_len(view)

def ctx_string_view_slice(view: StringView, start: i64, end: i64) -> StringView:
	return string_view_slice(view, start, end)

def ctx_string_view_eq(view: StringView, other: dstr[shape_other]) -> int:
	return string_view_eq(view, other)

def ctx_string_views_eq(lhs: StringView, rhs: StringView) -> int:
	return string_views_eq(lhs, rhs)

def probe(text: dstr[row], other: dstr[col]) -> int:
	view: StringView = ctx_string_view(text, 0, 4)
	sub: StringView = ctx_string_view_slice(view, 1, 3)
	if ctx_string_view_eq(sub, other) != 0:
		return ctx_string_views_eq(sub, view)
	return ctx_string_view_len(sub)
`
	_, errs := parseAndAnalyze(t, "stage1_string_view_helpers.llcontext", src)
	requireNoErrors(t, errs)
}

func TestAnalyzeStage1RuntimeFileAcceptsShapeTypedWrappers(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "contextlang_runtime.llcontext")
	src := loadSourceWithIncludes(t, fixture, map[string]bool{})
	_, errs := parseAndAnalyze(t, fixture, src)
	requireNoErrors(t, errs)
}

func TestAnalyzePointerFixture(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "test_programs", "pointer_alloc.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeShapeOpsFixture(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "test_programs", "shape_ops.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read shape ops fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeArenaRuntimeFile(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "arena.llcontext")
	src, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("failed to read arena runtime fixture: %v", err)
	}
	_, errs := parseAndAnalyze(t, fixture, string(src))
	requireNoErrors(t, errs)
}

func TestAnalyzeContextRuntimeFile(t *testing.T) {
	fixture := filepath.Join(repoRootFromTestFile(t), "Code", "contextlang_runtime.llcontext")
	src := loadSourceWithIncludes(t, fixture, map[string]bool{})
	_, errs := parseAndAnalyze(t, fixture, src)
	requireNoErrors(t, errs)
}
