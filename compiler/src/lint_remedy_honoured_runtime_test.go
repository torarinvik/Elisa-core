package main

import (
	"strings"
	"testing"
)

// Both lints below advertise a specific remedy. A lint that keeps firing after the
// programmer applies its own advice is worse than no lint: it trains the reader to
// ignore the category. These tests pin BOTH directions — the finding is still made
// when the remedy is absent, and silenced when it is present — because a
// suppression that also swallows the real case is the obvious way to get this
// wrong.

const uninferredReserveBound = "cannot infer a safe reserve bound"

// A nested fill has no inferable bound, so the lint fires and tells the programmer
// to write an explicit reserve before the loop.
const nestedFillBody = `        for chunk in src:
            for x in chunk:
                xs.push(x)
        return xs.count
`

func TestRunCLIUninferredReserveBoundFiresWithoutExplicitReserve(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "uninferred_reserve_bare.elisa",
		`def builds(src: darray[darray[i64]]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
`+nestedFillBody)
	if !strings.Contains(out, uninferredReserveBound) {
		t.Fatalf("a nested fill with no reserve must warn, got:\n%s", out)
	}
}

func TestRunCLIUninferredReserveBoundHonoursExplicitReserve(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "uninferred_reserve_honoured.elisa",
		`def builds(src: darray[darray[i64]]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.reserve(src.count * 8)
`+nestedFillBody)
	if strings.Contains(out, uninferredReserveBound) {
		t.Fatalf("the lint's own remedy must silence it, got:\n%s", out)
	}
}

// A capture list with an initializer lowers to a VarDecl between the reserve and
// the loop. A declaration cannot undo a reserve, so it must not hide one.
func TestRunCLIUninferredReserveBoundLooksThroughDeclarations(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "uninferred_reserve_through_decl.elisa",
		`def builds(src: darray[darray[i64]]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        xs: mutable darray[i64] = []
        xs.reserve(src.count * 8)
        seen: usize = 0
`+nestedFillBody)
	if strings.Contains(out, uninferredReserveBound) {
		t.Fatalf("a declaration between reserve and loop must not hide the reserve, got:\n%s", out)
	}
}

const unboundedStringCast = "unbounded string"

// `buf.push(0)` then `(&buf[0]).cast[cstr]` is the NUL-terminated C-string idiom,
// and it is exactly the proof the cast warning asks for: a scan from element 0
// stops at the pushed NUL, which is inside the buffer. Passing a cstr is also
// unavoidable at a C boundary, so there is no alternative to advise.
func TestRunCLIUnboundedStringCastFiresOnUnterminatedBuffer(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "cstr_cast_unterminated.elisa",
		`extern puts(text: cstr) -> i64

def main() -> i64:
    can Memory.Allocate, Abort.Panic, Unsafe.PointerCast:
        buf: mutable darray[u8] = []
        buf.push('h'.u8())
        text: cstr = (&buf[0]).cast[cstr]
        return puts(text)
`)
	if !strings.Contains(out, unboundedStringCast) {
		t.Fatalf("casting an unterminated buffer to cstr must warn, got:\n%s", out)
	}
}

func TestRunCLIUnboundedStringCastAllowsNulTerminatedBuffer(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "cstr_cast_terminated.elisa",
		`extern puts(text: cstr) -> i64

def main() -> i64:
    can Memory.Allocate, Abort.Panic, Unsafe.PointerCast:
        buf: mutable darray[u8] = []
        buf.push('h'.u8())
        buf.push(0)
        text: cstr = (&buf[0]).cast[cstr]
        return puts(text)
`)
	if strings.Contains(out, unboundedStringCast) {
		t.Fatalf("a NUL-terminated buffer is the proof the warning asks for, got:\n%s", out)
	}
}

// Shortening the buffer can remove the terminator, so the fact must not survive it.
func TestRunCLIUnboundedStringCastForgetsTerminatorAfterTruncate(t *testing.T) {
	t.Parallel()
	out := compileAndCaptureStderr(t, "cstr_cast_truncated.elisa",
		`extern puts(text: cstr) -> i64

def main() -> i64:
    can Memory.Allocate, Abort.Panic, Unsafe.PointerCast:
        buf: mutable darray[u8] = []
        buf.push('h'.u8())
        buf.push(0)
        buf.truncate(1)
        text: cstr = (&buf[0]).cast[cstr]
        return puts(text)
`)
	if !strings.Contains(out, unboundedStringCast) {
		t.Fatalf("truncating away the terminator must restore the warning, got:\n%s", out)
	}
}
