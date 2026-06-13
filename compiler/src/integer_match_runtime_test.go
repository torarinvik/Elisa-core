package main

import "testing"

// Integer `match` (open scalar match: integer-literal arms + required `_`) lowers through the same
// generic scalar-literal drivers as string match. This exercises the full path end-to-end —
// analyzer routing, contextual literal typing at the scrutinee width, and the backend ICmp-EQ arm —
// for u8 and u16 scrutinees, both as a dispatch statement and feeding a returned value.
const integerMatchBody = `
def decode_u8(op: u8) -> i32:
    out: mutable i32 = 0
    match op:
        0xA9:
            out <- 1
        0x8D:
            out <- 2
        0x00:
            out <- 3
        _:
            out <- 99
    return out

def decode_u16(addr: u16) -> i32:
    out: mutable i32 = 0
    match addr:
        0x2000:
            out <- 10
        0x4016:
            out <- 20
        _:
            out <- 30
    return out

@test
def integer_match_dispatch() -> void:
    can Abort.Panic:
        if decode_u8(0xA9u8) != 1:
            panic("u8 0xA9")
        if decode_u8(0x8Du8) != 2:
            panic("u8 0x8D")
        if decode_u8(0x00u8) != 3:
            panic("u8 0x00")
        if decode_u8(0x42u8) != 99:
            panic("u8 default")
        if decode_u16(0x2000u16) != 10:
            panic("u16 0x2000")
        if decode_u16(0x4016u16) != 20:
            panic("u16 0x4016")
        if decode_u16(0x1234u16) != 30:
            panic("u16 default")
`

func TestIntegerMatchDispatch(t *testing.T) {
	exit, stdout, stderr := runStressProgram(t, "integer_match", integerMatchBody)
	assertAllPassed(t, exit, stdout, stderr, "integer_match_dispatch")
}
