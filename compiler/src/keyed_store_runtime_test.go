package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The KeyedStore sub-protocol (docs/69): a store addressed by a key whose lookup is PARTIAL
// — `store_find` returns an optional element because the key may be absent. dict implements
// it (Key = K, Elem = V), so generic `[S: KeyedStore]` code works on a dict: a present key
// resolves, a missing key returns null. This is the orthogonality the total Store preserves
// — dict is keyed/partial, not a total handle->element store.
func TestRunCLIKeyedStoreOverDict(t *testing.T) {
	repoRoot := repoRootFromMainTest(t)
	std := filepath.Join(repoRoot, "compiler", "runtime", "elisacore_std")
	fixtureDir := t.TempDir()
	rel := func(name string) string {
		p, err := filepath.Rel(fixtureDir, filepath.Join(std, name))
		if err != nil {
			t.Fatalf("rel include %s: %v", name, err)
		}
		return filepath.ToSlash(p)
	}
	preamble := fmt.Sprintf("# include %q\n# include %q\n",
		rel("test.elisa"), rel("elisacore_runtime.elisa"))
	src := preamble + `
def find[S: KeyedStore](s: S&, k: S.Key) -> S.Elem&?:
    return S.store_find(s, k)

def size[S: KeyedStore](s: S&) -> usize:
    return S.store_count(s)

@test
def keyed_store_over_dict() -> void:
    can Memory.Allocate, Abort.Panic:
        region r:
            a: mutable Arena& = &r
            m: mutable dict[i64, i64] = arena_dict_new[i64, i64](a, 8)
            _ = arena_dict_get_or_insert_checked[i64, i64](a, m, 1, 100)
            _ = arena_dict_get_or_insert_checked[i64, i64](a, m, 2, 200)
            hit: i64&? = find[dict[i64, i64]](m, 1)
            miss: i64&? = find[dict[i64, i64]](m, 99)
            if hit == null:
                panic("KeyedStore.find missed a present key")
            if miss != null:
                panic("KeyedStore.find found an absent key")
            if size[dict[i64, i64]](m) != 2:
                panic("KeyedStore.store_count over dict wrong")
`
	fixturePath := filepath.Join(fixtureDir, "keyed_store_over_dict.elisa")
	if err := os.WriteFile(fixturePath, []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if exitCode := runCLI([]string{"-emit", "test", fixturePath}, &stdout, &stderr); exitCode != 0 {
		t.Fatalf("expected KeyedStore-over-dict test to pass, stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] keyed_store_over_dict") {
		t.Fatalf("expected keyed_store_over_dict to pass, got:\n%s", stdout.String())
	}
}
