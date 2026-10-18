package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIAutoRegionCompositeReturnOwnership(t *testing.T) {
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang unavailable")
	}
	std, err := filepath.Abs(filepath.Join(repoRootFromMainTest(t), "compiler", "runtime", "elisacore_std", "elisacore_runtime.elisa"))
	if err != nil {
		t.Fatal(err)
	}
	source := `include "` + std + `"

struct RegionBag:
    items: darray[i64] = []

def make_region_pair() -> (items: darray[i64], count: usize) can[Memory.Allocate, Abort.Panic]:
    return ([7], 1)

def choose_region_pair(flag: bool) -> (items: darray[i64], count: usize) can[Memory.Allocate, Abort.Panic]:
    return ([7], 1) if flag else ([9], 1)

def explicit_region_pair[@r](seed: darray[i64] @r) -> (items: darray[i64], count: usize) @r can[Memory.Allocate, Abort.Panic]:
    return ([11], 1)

def explicit_field_pair[@r](seed: darray[i64] @r) -> (items: darray[i64] @r, count: usize) can[Memory.Allocate, Abort.Panic]:
    return ([13], 1)

def make_region_bag() -> RegionBag can[Memory.Allocate, Abort.Panic]:
    return RegionBag{items: [17]}

def churn_return_region() -> i64 can[Memory.Allocate, Abort.Panic]:
    values: mutable darray[i64] = []
    for i in 0..<1024:
        values.push(23)
    return values[0]

@test
def composite_return_region_runtime_test() -> void:
    can Memory.Allocate, Abort.Panic:
        region caller:
            seed: mutable darray[i64] = []
            pair = make_region_pair()
            left = choose_region_pair(true)
            right = choose_region_pair(false)
            explicit = explicit_region_pair(seed)
            field = explicit_field_pair(seed)
            bag = make_region_bag()
            if churn_return_region() != 23:
                panic("local arena churn failed")
            if pair.items[0] != 7 or left.items[0] != 7 or right.items[0] != 9:
                panic("tuple return backing did not survive")
            if explicit.items[0] != 11 or field.items[0] != 13:
                panic("explicit return region was ignored")
            if bag.items[0] != 17:
                panic("struct return backing did not survive")
`
	path := filepath.Join(t.TempDir(), "composite_return.elisa")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if status := runCLI([]string{"-emit", "test", path}, &stdout, &stderr); status != 0 {
		t.Fatalf("status=%d stdout=%s stderr=%s", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "[       OK ] composite_return_region_runtime_test") {
		t.Fatal(stdout.String())
	}
}
