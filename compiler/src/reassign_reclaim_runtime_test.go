package main

import "testing"

// `enc <- encode(seed)` in a loop: the old buffer is returned to the arena (the reassignment is
// provably the only header), so the next encode reuses it. Reuse must never touch a LIVE buffer:
// `keep` (a different local built the same way) and the current `enc` are checked byte for byte
// after every iteration, and the loop runs long enough that a missing reclaim would be visible
// in arena growth rather than correctness (checked separately on the Base64 benchmark).
func TestReassignReclaimReusesOnlyDeadBuffers(t *testing.T) {
	src := `
def encode(input: darray[u8]&, salt: u8) -> darray[u8]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[u8] = []
        out.resize(input.count * 2)
        for i in 0..<input.count |out|:
            out[2 * i] <- input[i] + salt
            out[2 * i + 1] <- salt
        return out

def check(bytes: darray[u8]&, seed: darray[u8]&, salt: u8) -> bool:
    if bytes.count != seed.count * 2:
        return false
    for i in 0..<seed.count:
        if bytes[2 * i] != seed[i] + salt or bytes[2 * i + 1] != salt:
            return false
    return true

@test
def bt() -> void:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        seed: mutable darray[u8] = []
        for i in 0..<4096 |seed|:
            seed.push((i % 97).u8())
        keep: darray[u8] = encode(&seed, 7)
        enc: mutable darray[u8] = encode(&seed, 1)
        ok: mutable bool = true
        for round in 0..<300 |enc, ok|:
            enc <- encode(&seed, (round % 50 + 2).u8())
            if not check(&enc, &seed, (round % 50 + 2).u8()) or not check(&keep, &seed, 7):
                ok <- false
        if not ok or enc.count != 8192:
            panic("reclaimed a live buffer")
`
	buildAndRunSoATest(t, "reassign_reclaim", src, "bt")
}
