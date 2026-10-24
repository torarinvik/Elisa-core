package backend

import (
	"runtime"
	"testing"
)

func TestNativeObjectPartitions(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"", min(64, max(1, runtime.NumCPU()))}, {"invalid", min(64, max(1, runtime.NumCPU()))},
		{" 32 ", 32}, {"0", 1}, {"-8", 1}, {"65", 65}, {"128", 128}, {"129", 128},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("ELISACORE_OBJECT_PARTITIONS", tc.value)
			if got := NativeObjectPartitions(); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
