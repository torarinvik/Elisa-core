package semantic

import "testing"

func TestFreshShapeDiagnosticsAreDeterministicAcrossParallelBodies(t *testing.T) {
	src := []byte(`extern resize(array: darray[i32, shape_in], size: usize) -> darray[i32, shape_out]

def grow(array: darray[i32, row]) -> darray[i32, shape_after]:
	return resize(array, 8)

def same(left: darray[i32, shape_pair], right: darray[i32, shape_pair]) -> void:
	pass

def bad(array: darray[i32, row]) -> void:
	same(grow(array), grow(array))
`)

	var want string
	t.Run("serial_reference", func(t *testing.T) {
		t.Setenv("ELISA_PARALLEL_BODIES", "1")
		want = fuzzAnalyzeText(src)
	})
	for _, workers := range []string{"2", "3", "4"} {
		t.Run("workers_"+workers, func(t *testing.T) {
			t.Setenv("ELISA_PARALLEL_BODIES", workers)
			for i := 0; i < 24; i++ {
				if got := fuzzAnalyzeText(src); got != want {
					t.Fatalf("fresh-shape diagnostics differ from serial analysis (run %d):\n%s\n=== vs ===\n%s", i+1, want, got)
				}
			}
		})
	}
}
