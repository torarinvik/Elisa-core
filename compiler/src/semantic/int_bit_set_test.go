package semantic

import (
	"reflect"
	"testing"
)

func TestIntBitSetTracksInlineAndOverflowWords(t *testing.T) {
	set := intBitSetOf(0, 1, 63, 64, 130)
	for _, index := range []int{0, 1, 63, 64, 130} {
		if !set.Contains(index) {
			t.Fatalf("expected set to contain %d", index)
		}
	}
	for _, index := range []int{-1, 2, 62, 65, 129, 131} {
		if set.Contains(index) {
			t.Fatalf("expected set not to contain %d", index)
		}
	}
	if got := set.Count(); got != 5 {
		t.Fatalf("expected count 5, got %d", got)
	}
	var seen []int
	set.ForEach(func(index int) {
		seen = append(seen, index)
	})
	if want := []int{0, 1, 63, 64, 130}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("unexpected iteration order: got %v want %v", seen, want)
	}
}

func TestIntBitSetCloneIsIndependent(t *testing.T) {
	original := intBitSetOf(1, 64)
	clone := original.Clone()
	clone.Add(129)
	if clone.IsEmpty() {
		t.Fatal("expected cloned set to remain non-empty")
	}
	if original.Contains(129) {
		t.Fatal("expected clone mutation not to affect original set")
	}
	if !clone.Contains(129) {
		t.Fatal("expected cloned set to contain the newly added index")
	}
	if got := original.Count(); got != 2 {
		t.Fatalf("expected original count 2, got %d", got)
	}
	if got := clone.Count(); got != 3 {
		t.Fatalf("expected clone count 3, got %d", got)
	}
}
