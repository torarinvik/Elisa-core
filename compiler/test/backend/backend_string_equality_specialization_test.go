package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesSameExtentRuntimeStringEquality(t *testing.T) {
	src := `struct StringView:
	data: mutable u8&
	len: mutable i64

def sview(value: u8&?, start: i64, end: i64) -> StringView:
	_ = value
	_ = start
	trusted Unsafe.PointerCast:
		return StringView{data: "".cast[u8&], len: end - start}

def ctx_string_view(value: cstr[shape_in], start: i64, end: i64) -> StringView:
	return sview(value, start, end)

def ctx_string_view_prefix(view: StringView, end: i64) -> StringView:
	return sview(view.data, 0, end)

def ctx_string_view_suffix(view: StringView, start: i64) -> StringView:
	return sview(view.data, start, view.len)

def same_shape_text(left: cstr[row], right: cstr[row]) -> bool:
	return left == right

def same_bounds_view(left: cstr[row], right: cstr[col]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 2)
	return left_view == right_view

def fresh_disjoint_raw_views() -> bool:
	region scratch(1024)
	return sview(new[scratch] 1, 0, 1) == sview(new[scratch] 2, 0, 1)

def split_disjoint_views(text: cstr[row]) -> bool:
	base: StringView = ctx_string_view(text, 0, 4)
	return ctx_string_view_prefix(base, 2) == ctx_string_view_suffix(base, 2)

def different_bounds_view(left: cstr[row], right: cstr[col]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 3)
	return left_view == right_view
`
	result := parseAndAnalyze(t, "backend_runtime_string_same_extent.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i32 @memcmp(ptr, ptr, i64)",
		"declare i64 @ctx_strlen(ptr)",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}

	sameShapeBody := functionIR(output, "same_shape_text")
	if assertSourceHelperFallback(t, sameShapeBody) {
		return
	}
	if sameShapeBody == "" {
		t.Fatalf("expected to find same_shape_text body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i32 @memcmp(ptr"} {
		if !strings.Contains(sameShapeBody, want) {
			t.Fatalf("expected same_shape_text to contain %q, got:\n%s", want, sameShapeBody)
		}
	}
	if strings.Contains(sameShapeBody, "call i64 @ctx_streq") {
		t.Fatalf("expected same_shape_text to avoid ctx_streq helper, got:\n%s", sameShapeBody)
	}

	sameBoundsBody := functionIR(output, "same_bounds_view")
	if assertSourceHelperFallback(t, sameBoundsBody) {
		return
	}
	if sameBoundsBody == "" {
		t.Fatalf("expected to find same_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(sameBoundsBody, "call i32 @memcmp(ptr") {
		t.Fatalf("expected same_bounds_view to use memcmp fast path, got:\n%s", sameBoundsBody)
	}
	if strings.Contains(sameBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected same_bounds_view to avoid ctx_string_views_eq helper, got:\n%s", sameBoundsBody)
	}

	disjointBoundsBody := functionIR(output, "fresh_disjoint_raw_views")
	if assertSourceHelperFallback(t, disjointBoundsBody) {
		return
	}
	if disjointBoundsBody == "" {
		t.Fatalf("expected to find fresh_disjoint_raw_views body, got:\n%s", output)
	}
	if !strings.Contains(disjointBoundsBody, "call i32 @memcmp(ptr noalias") {
		t.Fatalf("expected fresh_disjoint_raw_views to mark memcmp operands noalias, got:\n%s", disjointBoundsBody)
	}

	splitBoundsBody := functionIR(output, "split_disjoint_views")
	if assertSourceHelperFallback(t, splitBoundsBody) {
		return
	}
	if splitBoundsBody == "" {
		t.Fatalf("expected to find split_disjoint_views body, got:\n%s", output)
	}
	if !strings.Contains(splitBoundsBody, "call i32 @memcmp(ptr noalias") {
		t.Fatalf("expected split_disjoint_views to use disjoint memcmp fast path, got:\n%s", splitBoundsBody)
	}
	if strings.Contains(splitBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected split_disjoint_views to avoid ctx_string_views_eq helper, got:\n%s", splitBoundsBody)
	}

	differentBoundsBody := functionIR(output, "different_bounds_view")
	if assertSourceHelperFallback(t, differentBoundsBody) {
		return
	}
	if differentBoundsBody == "" {
		t.Fatalf("expected to find different_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(differentBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected different_bounds_view to keep helper fallback, got:\n%s", differentBoundsBody)
	}
}
func TestGenerateLLVMIRSpecializesDirectRuntimeStringEqualityHelpers(t *testing.T) {
	src := `struct StringView:
	data: mutable u8&
	len: mutable i64

extern ctx_string_view(value: cstr[row], start: i64, end: i64) -> StringView
extern ctx_string_view_prefix(view: StringView, end: i64) -> StringView
extern ctx_string_view_suffix(view: StringView, start: i64) -> StringView
extern ctx_string_slice(value: cstr[row], start: i64, end: i64) -> cstr[shape_out]
extern ctx_streq(left: cstr[row], right: cstr[row]) -> int
extern ctx_string_view_eq(view: StringView, text: cstr[shape_other]) -> int
extern ctx_string_views_eq(left: StringView, right: StringView) -> int

def direct_same_shape_text(left: cstr[row], right: cstr[row]) -> bool:
	return ctx_streq(left, right) != 0

def direct_same_bounds_view_text(left: cstr[row], right: cstr[row]) -> bool:
	view: StringView = ctx_string_view(left, 0, 4)
	return ctx_string_view_eq(view, ctx_string_slice(right, 0, 4)) != 0

def direct_split_disjoint_views(text: cstr[row]) -> bool:
	base: StringView = ctx_string_view(text, 0, 4)
	return ctx_string_views_eq(ctx_string_view_prefix(base, 2), ctx_string_view_suffix(base, 2)) != 0

def direct_different_bounds_view(left: cstr[row], right: cstr[row]) -> bool:
	left_view: StringView = ctx_string_view(left, 0, 2)
	right_view: StringView = ctx_string_view(right, 0, 3)
	return ctx_string_views_eq(left_view, right_view) != 0
`
	result := parseAndAnalyze(t, "backend_runtime_string_direct_helper_eq.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	directTextBody := functionIR(output, "direct_same_shape_text")
	if assertSourceHelperFallback(t, directTextBody) {
		return
	}
	if directTextBody == "" {
		t.Fatalf("expected to find direct_same_shape_text body, got:\n%s", output)
	}
	for _, want := range []string{"call i64 @ctx_strlen(ptr", "call i32 @memcmp(ptr"} {
		if !strings.Contains(directTextBody, want) {
			t.Fatalf("expected direct_same_shape_text to contain %q, got:\n%s", want, directTextBody)
		}
	}
	if strings.Contains(directTextBody, "call i64 @ctx_streq") {
		t.Fatalf("expected direct_same_shape_text to avoid ctx_streq helper fallback, got:\n%s", directTextBody)
	}

	directViewTextBody := functionIR(output, "direct_same_bounds_view_text")
	if assertSourceHelperFallback(t, directViewTextBody) {
		return
	}
	if directViewTextBody == "" {
		t.Fatalf("expected to find direct_same_bounds_view_text body, got:\n%s", output)
	}
	if !strings.Contains(directViewTextBody, "call i32 @memcmp(ptr") {
		t.Fatalf("expected direct_same_bounds_view_text to use memcmp fast path, got:\n%s", directViewTextBody)
	}
	if strings.Contains(directViewTextBody, "call i64 @ctx_string_view_eq") {
		t.Fatalf("expected direct_same_bounds_view_text to avoid ctx_string_view_eq helper fallback, got:\n%s", directViewTextBody)
	}

	directSplitViewsBody := functionIR(output, "direct_split_disjoint_views")
	if assertSourceHelperFallback(t, directSplitViewsBody) {
		return
	}
	if directSplitViewsBody == "" {
		t.Fatalf("expected to find direct_split_disjoint_views body, got:\n%s", output)
	}
	if !strings.Contains(directSplitViewsBody, "call i32 @memcmp(ptr noalias") {
		t.Fatalf("expected direct_split_disjoint_views to use disjoint memcmp fast path, got:\n%s", directSplitViewsBody)
	}
	if strings.Contains(directSplitViewsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected direct_split_disjoint_views to avoid ctx_string_views_eq helper fallback, got:\n%s", directSplitViewsBody)
	}

	differentBoundsBody := functionIR(output, "direct_different_bounds_view")
	if assertSourceHelperFallback(t, differentBoundsBody) {
		return
	}
	if differentBoundsBody == "" {
		t.Fatalf("expected to find direct_different_bounds_view body, got:\n%s", output)
	}
	if !strings.Contains(differentBoundsBody, "call i64 @ctx_string_views_eq") {
		t.Fatalf("expected direct_different_bounds_view to keep helper fallback, got:\n%s", differentBoundsBody)
	}
}
