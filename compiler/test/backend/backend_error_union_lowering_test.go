package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRLowersErrorUnionsTryAndRaise(t *testing.T) {
	src := `error MemoryError:
	OutOfMemory

error IoError:
	NotFound

extern alloc(size: usize) -> heap void&?
extern read_file(path: u8&) -> cstr[file_text] error[IoError]
extern empty_file_text() -> cstr[file_text]

def checked_alloc(size: usize) -> heap void& error[MemoryError]:
	ptr: heap void& = get alloc(size) else raise MemoryError.OutOfMemory
	return ptr

def load_text(path: u8&) -> cstr[file_text] error[IoError]:
	text: cstr[file_text] = try read_file(path)
	return text

def load_with_fallback(path: u8&) -> cstr[file_text]:
	text: cstr[file_text] = try read_file(path) else empty_file_text()
	return text

def load_with_default(path: u8&) -> cstr[file_text]:
	text: cstr[file_text] = try read_file(path) else empty_file_text()
	return text
`
	result := parseAndAnalyze(t, "backend_error_handling.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__IoError__cstr_file_text = type { i32, ptr }",
		"%ErrUnion__MemoryError__heap_void = type { i32, ptr }",
		"declare ptr @alloc(i64)",
		"declare i32 @read_file(ptr, ptr)",
		"define i32 @checked_alloc(ptr ",
		"define i32 @load_text(ptr ",
		"define ptr @load_with_fallback(ptr ",
		"define ptr @load_with_default(ptr ",
		"extractvalue %ErrUnion__IoError__cstr_file_text",
		"insertvalue %ErrUnion__IoError__cstr_file_text",
		"icmp eq i32",
		"phi ptr",
		"ret i32",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPayloadErrorRaiseAndCatch(t *testing.T) {
	src := `error BackendError:
	UnsupportedType(span: i64, code: i32)
	Other

def fail(span: i64) -> i64 error[BackendError]:
	raise BackendError.UnsupportedType(span, 7)

def recover(span: i64) -> i64:
	return catch fail(span):
		lowered:
			lowered
		error e:
			9
`
	result := parseAndAnalyze(t, "backend_payload_error_handling.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	for _, check := range []string{
		"%ErrSet__BackendError = type { i32, i64, i32 }",
		"%ErrUnion__BackendError__i64 = type { %ErrSet__BackendError, ptr }",
		"insertvalue %ErrSet__BackendError",
		"extractvalue %ErrSet__BackendError",
		"define %ErrSet__BackendError @fail(",
		"define i64 @recover(",
		"catch.error",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPayloadErrorSuccessReturn(t *testing.T) {
	src := `error BackendError:
	UnsupportedType(span: i64, code: i32)

def ok() -> i64 error[BackendError]:
	return 7
`
	result := parseAndAnalyze(t, "backend_payload_error_success.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	for _, check := range []string{
		"%ErrSet__BackendError = type { i32, i64, i32 }",
		"define %ErrSet__BackendError @ok(ptr",
		"store i64 7",
		"ret %ErrSet__BackendError",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRLowersPayloadErrorMatch(t *testing.T) {
	src := `error BackendError:
	UnsupportedType(span: i64, code: i32)
	Other

def recover(e: BackendError) -> i64:
	match e:
		BackendError.UnsupportedType(span, code):
			return span + code.i64()
		BackendError.Other:
			return 0
`
	result := parseAndAnalyze(t, "backend_payload_error_match.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}
	for _, check := range []string{
		"%ErrSet__BackendError = type { i32, i64, i32 }",
		"define i64 @recover(",
		"errset.code",
		"errset.payload",
		"match.error.payload",
		"llvm.sadd.with.overflow.i64",
	} {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}

func TestGenerateLLVMIRRemapsErrorCodesWhenWideningErrorSets(t *testing.T) {
	src := `error SourceError:
	NotFound
	PermissionDenied

error BroadError:
	PermissionDenied
	Busy
	NotFound

extern read_value() -> int error[SourceError]

def bubble() -> int error[BroadError]:
	return try read_value()

def fail_now() -> int error[BroadError]:
	raise SourceError.NotFound
`
	result := parseAndAnalyze(t, "backend_error_set_widening.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__SourceError__int = type { i32, ptr }",
		"%ErrUnion__BroadError__int = type { i32, ptr }",
		"declare i32 @read_value(ptr)",
		"define i32 @bubble(ptr ",
		"define i32 @fail_now(ptr ",
		"errmap_is_SourceError_NotFound",
		"errmap_is_SourceError_PermissionDenied",
		"ret i32 3",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
func TestGenerateLLVMIRComposesMultipleErrorFamilies(t *testing.T) {
	src := `error FileError:
	NotFound
	PermissionDenied

error NetworkError:
	Timeout
	NotFound

extern read_disk() -> int error[FileError]
extern read_network() -> int error[NetworkError.Timeout]

def bubble_disk() -> int error[FileError, NetworkError]:
	return try read_disk()

def bubble_network() -> int error[FileError, NetworkError]:
	return try read_network()
`
	result := parseAndAnalyze(t, "backend_error_multi_family.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%ErrUnion__FileError__int = type { i32, ptr }",
		"declare i32 @read_disk(ptr)",
		"declare i32 @read_network(ptr)",
		"define i32 @bubble_disk(ptr ",
		"define i32 @bubble_network(ptr ",
		"errmap_is_FileError_NotFound",
		"errmap_is_FileError_PermissionDenied",
		"errmap_is_NetworkError_Timeout",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
}
