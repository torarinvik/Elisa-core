package main

import (
	"strings"
	"testing"
)

// `.len` on a compile-time string lowers to its byte length. The analyzer folded it from the
// start, but the backend's own const-evaluator had no `len` case, so every runtime use —
// `return "abc".len`, `GREETING.len`, a loop bound — failed to compile with a location-less
// "field access requires a struct type, got u8". Only a global `const N = "abc".len` worked,
// because the analyzer evaluated that one itself.
const stringLiteralLenBody = `
const GREETING = "hello"
const DOUBLED = GREETING.len * 2

@test
def string_literal_len_lowers() -> void:
    can Abort.Panic:
        if "abc".len != 3:
            panic("a string literal's len")
        if ("").len != 0:
            panic("an empty literal's len")
        if GREETING.len != 5:
            panic("a const string's len")
        if DOUBLED != 10:
            panic("a const folded from a const string's len")
        total: mutable i64 = 0
        for i in 0..<"abcdefg".len |total|:
            total <- total + 1
        if total != 7:
            panic("a literal's len as a loop bound")
`

func TestStringLiteralLenLowersToConstant(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "string_literal_len", stringLiteralLenBody)
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "string_literal_len_lowers")
}

// A string literal's GLOBAL must hold every byte of the literal. It was emitted through
// LLVMBuildGlobalStringPtr, which takes a C string, so `"ab\0cd"` became a 3-byte global `ab\0`
// while the view built over it still said len 5 — and `s[3]` read past the end of the global.
// Not a crash: a plausible wrong byte, in safe code. A const sview had the same defect; a dstr
// literal did not (it copies from a byte-exact global), and is pinned here so it stays that way.
const stringLiteralEmbeddedNulBody = `
const VIEW: sview = "xy\0zw"
const CS: cstr = "ab\0cd"
const CS_LEN = CS.len

@test
def cstr_const_len_is_strlen_at_compile_and_run_time() -> void:
    can Abort.Panic:
        if CS.len != 2:
            panic("a cstr's runtime len is strlen")
        if CS_LEN != CS.len:
            panic("a cstr const's folded len disagrees with its runtime len")
        static if CS.len != 2:
            panic("a static if saw a different cstr len than the running program")

@test
def string_literal_embedded_nul_keeps_every_byte() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        s: sview = "ab\0cd"
        if s.len != 5 or s[2] != 0.u8() or s[3] != 99.u8() or s[4] != 100.u8():
            panic("an sview literal lost the bytes after its NUL")
        if VIEW.len != 5 or VIEW[3] != 122.u8() or VIEW[4] != 119.u8():
            panic("a const sview lost the bytes after its NUL")
        if "ab\0cd".len != 5:
            panic("a literal's len stopped at its NUL")
        d: dstr = "ab\0cd"
        if d.count != 5 or d[3] != 99.u8() or d[4] != 100.u8():
            panic("a dstr literal lost the bytes after its NUL")
`

func TestStringLiteralEmbeddedNulKeepsEveryByte(t *testing.T) {
	t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
	exit, stdout, stderr := runStressProgram(t, "string_literal_embedded_nul", stringLiteralEmbeddedNulBody, "-link", "-fsanitize=address")
	if strings.Contains(stderr, "clang not available") {
		t.Skip("clang not available")
	}
	assertAllPassed(t, exit, stdout, stderr, "string_literal_embedded_nul_keeps_every_byte", "cstr_const_len_is_strlen_at_compile_and_run_time")
}
