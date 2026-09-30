package semantic

import (
	"strings"
	"testing"
)

// A slice of a fixed-size array (a local, a struct field of a local, or a by-value parameter) is a
// view into the callee's stack frame; returning it dangles. Owned-storage slices stay allowed.
func TestFrameArraySliceReturnRejected(t *testing.T) {
	cases := map[string]string{
		"local_array": `def f1() -> view[u8]:
    buf: u8[4] = [104, 105, 0, 0]
    v: view[u8] = buf[0:2]
    return v
`,
		"direct_return": `def f1() -> view[u8]:
    buf: u8[4] = [104, 105, 0, 0]
    return buf[0:2]
`,
		"by_value_param": `def f1(buf: u8[4]) -> view[u8]:
    return buf[0:2]
`,
		"struct_field": `struct S:
    buf: u8[4]

def f1() -> view[u8]:
    s: S = S{buf: [1, 2, 3, 4]}
    return s.buf[0:2]
`,
	}
	for name, src := range cases {
		result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "frame_array_slice_"+name+".elisa", src)
		if len(result.Errors()) == 0 {
			t.Errorf("%s: expected a local-borrow escape error, got none", name)
		}
	}
}

func TestFrameArraySliceUseAllowed(t *testing.T) {
	src := `def f1() -> i32:
    buf: u8[4] = [104, 105, 0, 0]
    v: view[u8] = buf[0:2]
    return v[0].i32()

def f2(xs: darray[u8]) -> view[u8]:
    return xs[0:1]
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "frame_array_slice_ok.elisa", src)
	if all := strings.Join(result.Errors(), "\n"); all != "" {
		t.Fatalf("unexpected errors:\n%s", all)
	}
}

// A dict element read through the rewritten arena_dict_get helper shares the dict's backing, so a
// darray field of it pushed into a caller's container must be treated like the owner's storage.
func TestDictGetElementFieldStoreRejected(t *testing.T) {
	src := `struct Box:
    items: darray[u8]

def arena_dict_get[T](m: dict[i32, T]&, key: i32) -> T&?:
    return null

def interp(out: mutable darray[darray[u8]]&) -> void:
    can Memory.Allocate:
        d: mutable dict[i32, Box] = {}
        d.put(1, Box{items: [65, 66]})
        v: Box& = get d.get(1) else return
        out.push(v.items)
`
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "dict_get_field_store.elisa", src)
	if all := strings.Join(result.Errors(), "\n"); !strings.Contains(all, "reference into") && !strings.Contains(all, "outlive") && !strings.Contains(all, "escape") {
		t.Fatalf("expected escape error, got: %v", result.Errors())
	}
}
