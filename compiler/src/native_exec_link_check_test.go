package main

import (
	"reflect"
	"testing"
)

// Mirrors `nm -m` output: the CUSA07399 hazard is the `_mprotect.1` undefined,
// dynamically-looked-up duplicate that sits next to the real two-level `_mprotect`.
func TestFindSplitNullBoundSymbols(t *testing.T) {
	nmOut := `                 (undefined) external _mprotect (from libSystem)
                 (undefined) external _mprotect.1 (dynamically looked up)
0000000100003f00 (__TEXT,__text) external _main
                 (undefined) external _munmap.2 (dynamically looked up)
                 (undefined) external _malloc (from libSystem)`
	got := findSplitNullBoundSymbols(nmOut)
	want := []string{"_mprotect.1", "_munmap.2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findSplitNullBoundSymbols = %v, want %v", got, want)
	}
}

// A clean binary (no `.N` split symbols) must produce no warnings.
func TestFindSplitNullBoundSymbolsClean(t *testing.T) {
	nmOut := `                 (undefined) external _mprotect (from libSystem)
0000000100003f00 (__TEXT,__text) external _main
                 (undefined) external _malloc (from libSystem)`
	if got := findSplitNullBoundSymbols(nmOut); len(got) != 0 {
		t.Fatalf("expected no split symbols on clean binary, got %v", got)
	}
}
