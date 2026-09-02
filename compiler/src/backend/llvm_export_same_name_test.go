package backend

import (
	"strings"
	"testing"
)

// A same-name export (`export fn foo(...) = foo`) is the implementation when its
// lowering already matches the C ABI, and a wrapper over `foo.impl` when it does
// not; `export type T as T` and `export global G as G` register nothing new.
func TestGenerateLLVMIRSameNameExports(t *testing.T) {
	src := `struct Vec2 layout(c):
    x: i32
    y: i32
export type Vec2 as Vec2

global MAGIC: i32 = 1337
export global MAGIC as MAGIC

def add(a: i32, b: i32) -> i32:
    return a + b
export fn add(a: i32, b: i32) -> i32 = add

def vec2_sum(v: Vec2) -> i32:
    return v.x + v.y
export fn vec2_sum(v: Vec2) -> i32 = vec2_sum

def uses_internally() -> i32:
    v: Vec2 = Vec2{x: 3, y: 4}
    return add(vec2_sum(v), MAGIC)
export fn uses_internally() -> i32 = uses_internally
`
	result := parseAndAnalyzeBackendTest(t, "same_name_exports.elisa", src)
	output, err := generateLLVMIRWithDefaultPackedLoweringForTest(result)
	if err != nil {
		t.Fatalf("generateLLVMIRForTest returned error: %v", err)
	}
	// identity lowering: the implementation IS the export, under its own name
	if !strings.Contains(output, "define i32 @add(i32") {
		t.Fatalf("expected add to be exported directly under its own name, got:\n%s", output)
	}
	if strings.Contains(output, "@add.impl") {
		t.Fatalf("expected no wrapper for a scalar same-name export, got:\n%s", output)
	}
	// coercing lowering: implementation renamed, wrapper owns the public symbol
	if !strings.Contains(output, "define i32 @vec2_sum.impl(%Vec2") {
		t.Fatalf("expected the aggregate implementation to move to vec2_sum.impl, got:\n%s", output)
	}
	if !strings.Contains(output, "define i32 @vec2_sum(i64") {
		t.Fatalf("expected the public vec2_sum wrapper to take the 8-byte aggregate as i64, got:\n%s", output)
	}
	// internal callers still reach the implementation
	if !strings.Contains(output, "call i32 @vec2_sum.impl(") {
		t.Fatalf("expected the internal caller to call vec2_sum.impl, got:\n%s", output)
	}
	// the same-name global is one symbol, not an alias to itself
	if strings.Count(output, "@MAGIC =") != 1 {
		t.Fatalf("expected exactly one definition of @MAGIC, got:\n%s", output)
	}
}
