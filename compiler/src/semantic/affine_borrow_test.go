package semantic

import (
	"strings"
	"testing"
)

// An `affine struct` is borrowable like a `linear struct`: a borrow neither copies nor
// consumes the owner. These cases pin the rules that keep that sound — no copy or move
// out through the reference, no use of a borrow after its owner moves, no reference to
// function-local storage escaping — and the one affine carrier that stays unborrowable.

const affineBorrowPrelude = `affine struct K:
    value: mutable i64

def finish(v: K) -> i64:
    k: K = move v
    k.value

def peek(o: K&) -> i64:
    o.value

`

func TestAffineStructBorrowsAreAccepted(t *testing.T) {
	cases := map[string]string{
		"param_read": `def run() -> i64:
    o: K = K{value: 1}
    x: i64 = peek(&o)
    x + finish(move o)
`,
		"mutable_param": `def bump(o: mutable K&) -> void:
    o.value <- o.value + 1

def run() -> i64:
    o: mutable K = K{value: 1}
    bump(&o)
    finish(move o)
`,
		"local_borrow_used_before_move": `def run() -> i64:
    o: K = K{value: 1}
    r: K& = &o
    x: i64 = peek(r)
    x + finish(move o)
`,
		"returned_param_borrow": `def lend(o: K&) -> K&:
    o

def run() -> i64:
    o: K = K{value: 1}
    x: i64 = peek(lend(&o))
    x + finish(move o)
`,
		"borrow_stored_in_field": `struct Holder:
    r: K&

def run() -> i64:
    o: K = K{value: 1}
    h: Holder = Holder{r: &o}
    x: i64 = peek(h.r)
    x + finish(move o)
`,
		// Affine, not linear: a borrowed value may still be dropped unconsumed.
		"dropped_after_borrow": `def run() -> i64:
    o: K = K{value: 1}
    peek(&o)
`,
		"forwarded_borrow": `def outer(o: mutable K&) -> i64:
    peek(o)
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			analyzeTreeTestSource(t, "affine_borrow_"+name+".elisa", affineBorrowPrelude+body)
		})
	}
}

func TestAffineStructBorrowMisuseIsRejected(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"return_copy_out", "def dup(o: K&) -> K:\n    o\n", "return type expects K, got K&"},
		{"local_copy_out", "def dup(o: K&) -> i64:\n    y: K = o\n    finish(move y)\n", `variable "y" expects K, got K&`},
		{"move_out", "def steal(o: K&) -> i64:\n    finish(move o)\n", `argument 1 to "finish" expects K, got K&`},
		{"borrow_used_after_owner_move", `def run() -> i64:
    o: K = K{value: 1}
    r: K& = &o
    y: i64 = finish(move o)
    peek(r) + y
`, `"r" cannot be used`},
		{"returned_borrow_used_after_owner_move", `def lend(o: K&) -> K&:
    o

def run() -> i64:
    o: K = K{value: 1}
    r: K& = lend(&o)
    y: i64 = finish(move o)
    peek(r) + y
`, `"r" cannot be used`},
		{"field_borrow_used_after_owner_move", `struct Holder:
    r: K&

def run() -> i64:
    o: K = K{value: 1}
    h: Holder = Holder{r: &o}
    y: i64 = finish(move o)
    peek(h.r) + y
`, `"h.r" cannot be used`},
		{"borrow_used_after_owner_rebind", `def run() -> i64:
    o: K = K{value: 1}
    r: K& = &o
    p: K = move o
    x: i64 = peek(r)
    x + finish(move p)
`, `"r" cannot be used`},
		{"borrow_of_local_returned", "def bad() -> K&:\n    o: K = K{value: 1}\n    &o\n", "returning a reference into function-local storage"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := analyzeTreeTestSourceWithSemanticErrors(t, "affine_borrow_"+tc.name+".elisa", affineBorrowPrelude+tc.body)
			if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, tc.want) {
				t.Fatalf("expected %q; got:\n%s", tc.want, all)
			}
		})
	}
}

// A `Pooled[T]` handle stays unborrowable: release recycles its slot, and the interior
// alias check only follows `ptr` read directly off the owning local.
func TestPooledHandleStaysUnborrowable(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "pooled_borrow_reject.elisa", poolBorrowPrelude+`def get_ptr(h: Pooled[Node]&) -> mutable heap Node&:
    h.ptr

def via_borrow_uaf() -> void:
    h: Pooled[Node] = zeroed
    b: mutable heap Node& = get_ptr(&h)
    release(move h)
    b.val <- 9
`)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "references to values containing linear handles are not supported; got Pooled[Node]&") {
		t.Fatalf("expected Pooled[Node]& to stay rejected; got:\n%s", all)
	}
}
