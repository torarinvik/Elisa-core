package semantic

import (
	"sort"
	"strings"
	"testing"
)

// Reassignment reclaim: only a reassignment whose old darray buffer provably has no other header
// copy, view or retained borrow may be marked. Lines tagged `# RECLAIM` must be marked; every
// other reassignment must not be.
func TestReassignReclaimMarksOnlyProvablyDeadBuffers(t *testing.T) {
	src := `def encode(input: darray[u8]&) -> darray[u8]:
    can Memory.Allocate, Abort.Panic:
        out: mutable darray[u8] = []
        out.resize(input.count + 1)
        for i in 0..<input.count |out|:
            out[i] <- input[i] + 1
        return out

def first_byte(bytes: darray[u8]&) -> u8:
    return bytes[0]

def keep_param(input: darray[u8]) -> darray[u8]:
    return input

def copy_into(dst: mutable darray[u8]&, src: darray[u8]&) -> void:
    can Memory.Allocate, Abort.Panic:
        for b in src |dst|:
            dst.push(b)

def ok_loop(seed: darray[u8]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        total: mutable usize = 0
        for _ in 0..<10 |enc, total|:
            enc <- encode(seed)  # RECLAIM
            total <- total + enc.count + first_byte(&enc).usize()
        sink: mutable darray[u8] = []
        copy_into(&sink, &enc)
        return total + sink.count

def copied(seed: darray[u8]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        other: darray[u8] = enc
        enc <- encode(seed)
        return other.count

def iterated(seed: darray[u8]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        total: mutable usize = 0
        for b in enc |total|:
            total <- total + b.usize()
        enc <- encode(seed)
        return total + enc.count

def not_fresh(seed: darray[u8]&, held: darray[u8]) -> usize:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        enc <- keep_param(held)
        return enc.count

def mentions_self(seed: darray[u8]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        enc <- encode(&enc)
        return enc.count

def mutable_arg(seed: darray[u8]&) -> usize:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        copy_into(&enc, seed)
        enc <- encode(seed)
        return enc.count

def indexed(seed: darray[u8]&) -> u8:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        r: u8 = enc[0]
        enc <- encode(seed)
        return r + enc[0]

def returned(seed: darray[u8]&) -> darray[u8]:
    can Memory.Allocate, Abort.Panic:
        enc: mutable darray[u8] = encode(seed)
        enc <- encode(seed)
        return enc
`
	result := analyzeFunctionAnalysisTestSource(t, "reassign_reclaim.elisa", src)
	want := map[int]bool{}
	for i, line := range strings.Split(src, "\n") {
		if strings.Contains(line, "# RECLAIM") {
			want[i+1] = true
		}
	}
	var got []int
	for assign := range result.ReassignReclaims {
		got = append(got, assign.Pos().Line)
	}
	sort.Ints(got)
	gotSet := map[int]bool{}
	for _, line := range got {
		gotSet[line] = true
		if !want[line] {
			t.Errorf("line %d: reassignment marked reclaimable, but its old buffer may still be referenced", line)
		}
	}
	for line := range want {
		if !gotSet[line] {
			t.Errorf("line %d: provably dead old buffer not marked reclaimable (marked: %v)", line, got)
		}
	}
}
