//go:build cgo

package semantic

import "testing"

const fixedArraySliceRequires = `
def exact(v: view[i64], n: usize) -> usize:
    requires v.len == n
    return n
`

func TestSMTFixedArraySliceLengthRequires(t *testing.T) {
	src := fixedArraySliceRequires + `
def in_bounds(lo: usize, hi: usize) -> usize:
    data: mutable i64[8] = zeroed
    return 0 if lo > hi or hi > 8
    span: view[i64] = data[lo:hi]
    return exact(span, hi - lo)

def clamped(lo: usize, hi: usize) -> usize:
    requires lo == 6 and hi == 10
    data: i64[8] = zeroed
    return exact(data[lo:hi], 2.usize())

def reversed(lo: usize, hi: usize) -> usize:
    requires lo == 6 and hi == 2
    data: i64[8] = zeroed
    return exact(data[lo:hi], 0.usize())
`
	result := analyzeContractStrict(t, "fixed_array_slice_requires.elisa", src)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("expected fixed-array slice lengths to discharge: %v", errors)
	}
}

func TestSMTFixedArraySliceLengthRejectsUnclampedBounds(t *testing.T) {
	src := fixedArraySliceRequires + `
def out_of_range(lo: usize, hi: usize) -> usize:
    requires lo == 6 and hi == 10
    data: i64[8] = zeroed
    return exact(data[lo:hi], hi - lo)
`
	result := analyzeContractStrict(t, "fixed_array_slice_unclamped.elisa", src)
	if preconditionFailures(result.Errors()) != 1 {
		t.Fatalf("expected the unclamped length to fail: %v", result.Errors())
	}
}
