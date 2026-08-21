//go:build cgo

package backend

import (
	"strings"
	"testing"
)

func TestGenerateCHeaderFunctionPointerField(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "header_function_pointer.elisa", `struct CallbackTable layout(c):
	user: mutable void&?
	callback: fn(mutable void&?, i32) -> void

export type CallbackTable as PublicCallbackTable
`)
	header, err := GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	if !strings.Contains(header, "void (*callback)(void *arg0, int32_t arg1);") {
		t.Fatalf("expected C function-pointer field, got:\n%s", header)
	}
}

func TestGenerateCHeaderFunctionPointerCStrParameter(t *testing.T) {
	result := parseAndAnalyzeBackendTest(t, "header_function_pointer_cstr.elisa", `struct CallbackTable layout(c):
	callback: fn(cstr) -> void
	returns_cstr: fn() -> cstr

export type CallbackTable as PublicCallbackTable
`)
	header, err := GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	if !strings.Contains(header, "void (*callback)(const char *arg0);") {
		t.Fatalf("expected C function-pointer cstr parameter, got:\n%s", header)
	}
	if !strings.Contains(header, "const char * (*returns_cstr)(void);") {
		t.Fatalf("expected C function-pointer cstr return, got:\n%s", header)
	}
}
