package checkpointhistory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"
)

func TestRoundTripAndIntegrity(t *testing.T) {
	common := strings.Repeat("shared expression ", 6000)
	want := []string{common + "one", common + "two", "other"}
	prefixes, capacity := Plan(want, 4096)
	if prefixes == nil {
		t.Fatal("expected compact history")
	}
	graph := bytes.Repeat([]byte{42}, sha256.Size)
	data := Append(graph, want, prefixes, capacity)
	gotGraph, history, err := Split(data)
	if err != nil || !bytes.Equal(gotGraph, graph) {
		t.Fatalf("split: %v", err)
	}
	got, err := Restore(history, 4096)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("restore: %v", err)
	}
	data[len(graph)] ^= 1
	if _, _, err := Split(data); err == nil {
		t.Fatal("accepted corrupt history")
	}
}

func TestRejectsInvalidHistory(t *testing.T) {
	if _, err := Restore([]byte{1, 1, 0}, 4096); err == nil {
		t.Fatal("accepted invalid prefix")
	}
	if _, err := Restore([]byte{2}, 1); err == nil {
		t.Fatal("accepted excess count")
	}
}

func TestCheckpointHistoryWireFormat(t *testing.T) {
	graph := bytes.Repeat([]byte{42}, sha256.Size)
	data := Append(graph, []string{"alpha", "alpine"}, []int{0, 3}, 0)
	const want = "020005616c7068610303696e650d000000000000003aa4383fe16856c2337a0b1803c7f3eed2e81fabefc2f7df54189f26ae7306ab454c495341534d5431"
	if got := hex.EncodeToString(data[len(graph):]); got != want {
		t.Fatalf("wire format changed: %s", got)
	}
}
