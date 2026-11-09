package backend_test

import (
	"elisacore/src/backend"
	"strings"
	"testing"
)

func TestGenerateLLVMIRSpecializesGenericFunctions(t *testing.T) {
	src := `def identity[T](value: T) -> T:
    return value

def use_identity(value: i32) -> i32:
    return identity(value)
`
	result := parseAndAnalyze(t, "backend_generic_specialization.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"define i32 @use_identity(i32",
		"define i32 @\"identity$generic_v1$",
		"call i32 @\"identity$generic_v1$",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	identityFunction := genericV1Symbol(t, output, `@"identity$generic_v1$`)
	assertLLVMIRContains(t, output,
		"define i32 "+identityFunction+"(i32",
		"call i32 "+identityFunction+"(i32",
	)
}
func TestGenerateLLVMIRLowersExportWrappers(t *testing.T) {
	src := `struct Vec[T] layout(c):
	x: mutable T
	y: mutable T

export type Vec[i32] as Vec2i

global seed: i32 = 7

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export global seed as ctx_seed
export fn vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add_i32
export fn vec2i_keep_left(left: Vec2i, right: Vec2i) -> Vec2i = keep_left[Vec[i32]]
`
	result := parseAndAnalyze(t, "backend_export_wrappers.elisa", src)
	output, err := backend.GenerateLLVMIR(result)
	if err != nil {
		t.Fatalf("GenerateLLVMIR returned error: %v", err)
	}

	checks := []string{
		"%\"Vec$generic_v1$",
		"@seed = global i32 7",
		"@ctx_seed = alias i32, ptr @seed",
		"define %\"Vec$generic_v1$",
		"@vec_add_i32(%\"Vec$generic_v1$",
		"@\"keep_left$generic_v1$",
		"define i64 @vec2i_add(i64",
		"define i64 @vec2i_keep_left(i64",
		"@vec_add_i32(%\"Vec$generic_v1$",
		"@\"keep_left$generic_v1$",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Fatalf("expected output to contain %q, got:\n%s", check, output)
		}
	}
	vecType := genericV1Symbol(t, output, `%"Vec$generic_v1$`)
	keepLeftFunction := genericV1Symbol(t, output, `@"keep_left$generic_v1$`)
	assertLLVMIRContains(t, output,
		vecType+" = type { i32, i32 }",
		"define "+vecType+" @vec_add_i32("+vecType,
		"call "+vecType+" @vec_add_i32("+vecType,
		"define "+vecType+" "+keepLeftFunction+"("+vecType,
		"call "+vecType+" "+keepLeftFunction+"("+vecType,
	)
}
func TestGenerateCHeaderForExportedVec2i(t *testing.T) {
	src := `struct Vec[T] layout(c):
	x: mutable T
	y: mutable T

export type Vec[i32] as Vec2i

global seed: i32 = 7

def vec_add_i32(left: Vec[i32], right: Vec[i32]) -> Vec[i32]:
	result: Vec[i32] = zeroed
	result.x <- left.x + right.x
	result.y <- left.y + right.y
	return result

def keep_left[T](left: T, right: T) -> T:
	return left

export global seed as ctx_seed
export fn vec2i_add(left: Vec2i, right: Vec2i) -> Vec2i = vec_add_i32
export fn vec2i_keep_left(left: Vec2i, right: Vec2i) -> Vec2i = keep_left[Vec[i32]]
`
	result := parseAndAnalyze(t, "backend_export_header.elisa", src)
	header, err := backend.GenerateCHeader(result)
	if err != nil {
		t.Fatalf("GenerateCHeader returned error: %v", err)
	}
	checks := []string{
		"#ifndef BACKEND_EXPORT_HEADER_H",
		"#include <stdint.h>",
		"typedef struct Vec2i Vec2i;",
		"struct Vec2i {",
		"int32_t x;",
		"int32_t y;",
		"extern int32_t ctx_seed;",
		"Vec2i vec2i_add(Vec2i arg0, Vec2i arg1);",
		"Vec2i vec2i_keep_left(Vec2i arg0, Vec2i arg1);",
	}
	for _, check := range checks {
		if !strings.Contains(header, check) {
			t.Fatalf("expected header to contain %q, got:\n%s", check, header)
		}
	}
	if strings.Contains(header, "Vec$generic_v1$") {
		t.Fatalf("expected public header not to leak backend mangled aggregate names, got:\n%s", header)
	}
}
