package main

import (
	"reflect"
	"testing"
)

// Mirrors `nm -m` output: the CUSA07399 hazard is the `_mprotect.1` undefined,
// dynamically-looked-up duplicate that sits next to the real two-level `_mprotect`.
func TestFindSplitNullBoundSymbols(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	nmOut := `                 (undefined) external _mprotect (from libSystem)
0000000100003f00 (__TEXT,__text) external _main
                 (undefined) external _malloc (from libSystem)`
	if got := findSplitNullBoundSymbols(nmOut); len(got) != 0 {
		t.Fatalf("expected no split symbols on clean binary, got %v", got)
	}
}

// The stripped-runtime-helper hazard: an elisa runtime helper (ctx_*/arena_*/packed_store_*)
// left undefined and dynamically looked up has no provider anywhere, so it binds to NULL and
// the first call segfaults (e.g. ctx_aos_store_new missing from the default runtime export
// whitelist). System symbols resolved from libSystem must not be flagged.
func TestFindNullBoundRuntimeHelperSymbols(t *testing.T) {
	t.Parallel()
	nmOut := `                 (undefined) external _ctx_aos_store_new (dynamically looked up)
                 (undefined) external _ctx_aos_store_alloc (dynamically looked up)
                 (undefined) external _arena_alloc (from libSystem)
                 (undefined) external _packed_store_append_index (dynamically looked up)
0000000100003f00 (__TEXT,__text) external _ctx_packed_store_count
                 (undefined) external _malloc (from libSystem)`
	got := findNullBoundRuntimeHelperSymbols(nmOut)
	want := []string{"_ctx_aos_store_alloc", "_ctx_aos_store_new", "_packed_store_append_index"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("findNullBoundRuntimeHelperSymbols = %v, want %v", got, want)
	}
}

// Defined helpers and non-helper dynamic lookups must not be flagged.
func TestFindNullBoundRuntimeHelperSymbolsClean(t *testing.T) {
	t.Parallel()
	nmOut := `0000000100001000 (__TEXT,__text) external _ctx_aos_store_new
                 (undefined) external _some_user_extern (dynamically looked up)
                 (undefined) external _malloc (from libSystem)`
	if got := findNullBoundRuntimeHelperSymbols(nmOut); len(got) != 0 {
		t.Fatalf("expected no helper symbols on clean binary, got %v", got)
	}
}
