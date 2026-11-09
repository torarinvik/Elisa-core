package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersCatchExprOverErrorUnion(t *testing.T) {
	src := `error FileError:
	NotFound
	Busy

extern read_value(flag: bool) -> int error[FileError]

def load(flag: bool) -> int:
	return catch read_value(flag):
		value:
			value
		NotFound:
			1
		Busy:
			2
`
	result := parseAndAnalyze(t, "backend_catch_expr.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	body := functionIR(output, "load")
	for _, check := range []string{
		"extractvalue %ErrUnion__FileError__int",
		"switch i32",
		"catchphi = phi i64",
	} {
		if !strings.Contains(body, check) {
			t.Fatalf("expected catch-lowered function to contain %q, got:\n%s", check, body)
		}
	}
}
func TestGenerateLLVMIRExpandsMixedRowStyleFamilies(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def bubble_disk() -> int error[FileError, NetworkError]:
	return try read_disk()

def bubble_network() -> int error[FileError, NetworkError]:
	return try read_network()

def fail_disk() -> int error[FileError, NetworkError]:
	raise FileError.PermissionDenied
`
	result := parseAndAnalyze(t, "backend_error_mixed_row_style.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @bubble_disk(ptr ",
		"define i32 @bubble_network(ptr ",
		"define i32 @fail_disk(ptr ",
		"errmap_is_FileError_NotFound",
		"errmap_is_FileError_PermissionDenied",
		"errmap_is_NetworkError_Timeout",
		"ret i32 2",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRCanonicalizesErrorUnionNames(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	Disconnected

extern read_value() -> int error[NetworkError, FileError]

def by_reverse_family_order() -> int error[NetworkError, FileError]:
	return try read_value()
`
	result := parseAndAnalyze(t, "backend_error_canonicalization.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	if !strings.Contains(output, "%ErrUnion__error_FileError__NetworkError__int = type { i32, ptr }") {
		t.Fatalf("expected canonical error union struct name, got:\n%s", output)
	}
}
func TestGenerateLLVMIRDistinguishesOptionalErrorUnionPayloadNames(t *testing.T) {
	src := `error ProbeError:
	Bad

def raw(flag: bool) -> int error[ProbeError]:
	if flag:
		raise ProbeError.Bad
	return 1

def maybe(flag: bool) -> int? error[ProbeError]:
	if flag:
		raise ProbeError.Bad
	return null
`
	result := parseAndAnalyze(t, "backend_error_union_optional_payload_names.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Optional__int = type { i1, i64 }",
		"define i32 @raw(ptr ",
		"define i32 @maybe(ptr ",
		"store i64 1, ptr %errunion.payload",
		"store %Optional__int %errunion.payload3, ptr %0",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRAcceptsBareFamilyErrorSetShorthand(t *testing.T) {
	src := `error IoError:
	NotFound

extern read_file(path: u8&) -> cstr[file_text] error[IoError]

def load_text(path: u8&) -> cstr[file_text] error[IoError]:
	text: cstr[file_text] = try read_file(path)
	return text
`
	result := parseAndAnalyze(t, "backend_error_set_wildcard.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__IoError__cstr_file_text = type { i32, ptr }",
		"declare i32 @read_file(ptr, ptr)",
		"define i32 @load_text(ptr ",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersValueOptionalsAndTryElse(t *testing.T) {
	src := `def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null


def fallback_value(flag: bool) -> int:
	return get maybe_value(flag) else 11
`
	result := parseAndAnalyze(t, "backend_value_optionals.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Optional__int = type { i1, i64 }",
		"define %Optional__int @maybe_value(i1",
		"define i64 @fallback_value(i1",
		"extractvalue %Optional__int",
		"phi i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersUnifiedElseRecovery(t *testing.T) {
	src := `error FileError:
	NotFound

extern read_value(flag: bool) -> int error[FileError]

def maybe_value(flag: bool) -> int?:
	if flag:
		return 7
	return null

def optional_return(flag: bool) -> int:
	return get maybe_value(flag) else return 11

def try_error_binding(flag: bool) -> int:
	return try read_value(flag) else err:
		return 13
`
	result := parseAndAnalyze(t, "backend_unified_else_recovery.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @optional_return(i1",
		"define i64 @try_error_binding(i1",
		"get.fallback",
		"try.fallback",
		"ret i64 11",
		"ret i64 13",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersOptionalNullChecksAndSmartCastUse(t *testing.T) {
	src := `struct Box:
	value: i32


def maybe_box(flag: bool) -> Box?:
	if flag:
		return Box{value: 7}
	return null


def unwrap_or(flag: bool) -> i32:
	value: Box? = maybe_box(flag)
	if value == null:
		return 11
	return value.value
`
	result := parseAndAnalyze(t, "backend_value_optionals_smart_cast.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%Optional__Box = type { i1, %Box }",
		"define i32 @unwrap_or(i1",
		"extractvalue %Optional__Box",
		"getelementptr inbounds nuw %Optional__Box",
		"getelementptr inbounds nuw %Box",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRIndexesRuntimeBackedArraysAndViews(t *testing.T) {
	src := `struct DynArray[T]:
	items: mutable T&?
    count: mutable usize
    capacity: mutable usize

struct DynArrayView:
	items: mutable void&?
    count: mutable usize

def read_array(values: darray[i32, row]) -> i32:
    return values[1]

def read_view(view: view[i32]) -> i32:
    return view[2]
`
	result := parseAndAnalyze(t, "backend_runtime_index.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%DynArray__i32 = type { ptr, i64, i64 }",
		"%DynArrayView = type { ptr, i64 }",
		"define i32 @read_array(%DynArray__i32",
		"define i32 @read_view(%DynArrayView",
		"getelementptr inbounds nuw %DynArray__i32",
		"getelementptr inbounds nuw %DynArrayView",
		"getelementptr inbounds i32, ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRIndexesDStrViaRuntimeHelper(t *testing.T) {
	src := `def read_codepoint(text: cstr[row]) -> char:
    return text[1]
`
	result := parseAndAnalyze(t, "backend_runtime_cstr_index.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i64 @read_codepoint(ptr",
		"declare i64 @ctx_string_index(ptr, i64)",
		"call i64 @ctx_string_index(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRAcceptsShapeErasingDStrShorthand(t *testing.T) {
	src := `def keep(text: cstr) -> cstr:
    return text

def erase(text: cstr[row]) -> cstr:
    return text
`
	result := parseAndAnalyze(t, "backend_cstr_shorthand.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define ptr @keep(ptr",
		"define ptr @erase(ptr",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRIndexesStringViewViaRuntimeHelper(t *testing.T) {
	src := `def read_view(view: StringView) -> char:
    return view[1]
`
	result := parseAndAnalyze(t, "backend_runtime_string_view_index.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%StringView = type { ptr, i64 }",
		"define i64 @read_view(%StringView",
		"declare i64 @ctx_string_view_index(%StringView, i64)",
		"call i64 @ctx_string_view_index(%StringView",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRLowersRuntimeStringEqualityHelpers(t *testing.T) {
	src := `def same_text(left: cstr[row], right: cstr[col]) -> bool:
	return left == right

def same_view_text(view: StringView, text: cstr[row]) -> bool:
	return view == text

def same_text_view(text: cstr[row], view: StringView) -> bool:
	return text == view

def different_views(left: StringView, right: StringView) -> bool:
	return left != right
`
	result := parseAndAnalyze(t, "backend_runtime_string_equality.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"declare i64 @ctx_streq(ptr, ptr)",
		"declare i64 @ctx_string_view_eq(%StringView, ptr)",
		"declare i64 @ctx_string_views_eq(%StringView, %StringView)",
		"call i64 @ctx_streq(ptr",
		"call i64 @ctx_string_view_eq(%StringView",
		"call i64 @ctx_string_views_eq(%StringView",
		"icmp ne i64",
		"icmp eq i64",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
