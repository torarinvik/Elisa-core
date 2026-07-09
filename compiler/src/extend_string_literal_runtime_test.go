package main

import "testing"

// A string literal is a BOUNDED extend source (its length is a compile-time constant),
// so `buf.extend("abc")` memcpies exactly its bytes onto the darray tail — appending, not
// overwriting. Compiled and RUN natively via `-emit test`: this checks the bytes actually
// land in order (the lowering test only checks that an arena_memcpy is emitted).
func TestExtendStringLiteralSourceRuntime(t *testing.T) {
	body := `@test
def literal_bytes_appended() -> void:
    can Abort.Panic, Memory.Allocate:
        buf: mutable darray[u8] = []
        buf.extend("abc")
        buf.extend("de")
        if buf.count != 5:
            panic("extend of two literals must append to 5 bytes")
        if buf[0] != 'a' or buf[1] != 'b' or buf[2] != 'c' or buf[3] != 'd' or buf[4] != 'e':
            panic("literal bytes copied in the wrong order")

@test
def empty_literal_is_noop() -> void:
    can Abort.Panic, Memory.Allocate:
        buf: mutable darray[u8] = []
        buf.extend("xy")
        buf.extend("")
        buf.extend("z")
        if buf.count != 3:
            panic("empty-literal extend must be a no-op")
        if buf[0] != 'x' or buf[1] != 'y' or buf[2] != 'z':
            panic("empty literal corrupted the surrounding bytes")

@test
def literal_and_view_sources_agree() -> void:
    can Abort.Panic, Memory.Allocate:
        src: mutable darray[u8] = []
        src.extend("world")
        buf: mutable darray[u8] = []
        buf.extend("hello ")
        buf.extend(src[0:5])
        if buf.count != 11:
            panic("literal + view extend must total 11 bytes")
        if buf[0] != 'h' or buf[6] != 'w' or buf[10] != 'd':
            panic("literal and view sources must concatenate")
`
	exit, stdout, stderr := runStressProgram(t, "extend_string_literal", body)
	assertAllPassed(t, exit, stdout, stderr,
		"literal_bytes_appended", "empty_literal_is_noop", "literal_and_view_sources_agree")
}
