package statecache

import (
	"strings"
	"testing"
)

func TestParallelChecksumFramedRoundTrip(t *testing.T) {
	r := framedGraphRegistry()
	root := &graph{Name: "root"}
	root.Next = root
	data, err := r.EncodeFramedWithCapacity(root, parallelChecksumMin)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), parallelChecksumFormat) {
		t.Fatal("large reservation did not select the parallel checksum format")
	}
	restored, err := r.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	got := restored.(*graph)
	if got.Next != got || got.Name != root.Name {
		t.Fatal("restored graph lost its cycle or name")
	}
	data[len(data)-1] ^= 1
	if _, err := r.Decode(data); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("corrupted digest accepted: %v", err)
	}
}
