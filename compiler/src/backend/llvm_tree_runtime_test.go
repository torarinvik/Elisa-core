//go:build cgo

package backend

import "testing"

func TestTreeInitialRowCapacityTracksRowSize(t *testing.T) {
	tests := []struct {
		rowSize uint64
		want    uint64
	}{
		{rowSize: 0, want: 0},
		{rowSize: 1, want: 16},
		{rowSize: 16, want: 16},
		{rowSize: 17, want: 8},
		{rowSize: 64, want: 8},
		{rowSize: 65, want: 4},
		{rowSize: 256, want: 4},
		{rowSize: 257, want: 2},
	}
	for _, test := range tests {
		if got := treeInitialRowCapacity(test.rowSize); got != test.want {
			t.Fatalf("treeInitialRowCapacity(%d) = %d, want %d", test.rowSize, got, test.want)
		}
	}
}
