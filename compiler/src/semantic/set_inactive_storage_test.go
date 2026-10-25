//go:build cgo

package semantic

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func actualSetInactiveSource(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("missing source path")
	}
	directory := filepath.Join(filepath.Dir(file), "../../runtime/elisacore_std")
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	extract := func(source, prefix string) string {
		start := strings.Index(source, prefix)
		if start < 0 {
			t.Fatal("missing actual source", prefix)
		}
		next := regexp.MustCompile(`(?m)^(?:def |struct |extern |include |@)`).FindStringIndex(source[start+len(prefix):])
		if next == nil {
			t.Fatal("missing actual source boundary", prefix)
		}
		return source[start:start+len(prefix)+next[0]] + "\n"
	}
	collections, arena := read("collections.elisa"), read("arena.elisa")
	return "extern memset(dest: mutable void&, val: i32, n: usize) -> mutable void&\n" +
		extract(collections, "def collections_mutable_void_from_items[") + extract(arena, "def arena_checked_mul_usize(") + extract(collections, "def arena_set_zero_buckets[")
}

func TestActualSetInactiveStorageSpecializesNonZeroRawBucket(t *testing.T) {
	// Raw bucket storage can contain sview without asserting that runtime set[sview]
	// is a supported public key type. SetBucket is the actual builtin carrier.
	source := actualSetInactiveSource(t) + `def probe(items: mutable SetBucket[sview]&) -> usize:
    return size_of(sview)
`
	root := analyzeTreeTestSource(t, "set-inactive.elisa", source)
	probe := retainedProofNamedDecl(t, root, "probe").(*ast.FuncDecl)
	args := root.analyzer.funcDeclSymbols[probe].Type.(*FuncType).Params[0].(*RefType).Elem.(*GenericInstanceType).Args
	helper := retainedProofNamedDecl(t, root, "arena_set_zero_buckets").(*ast.FuncDecl)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	product, err := root.SpecializeFunction(helper, args)
	if err != nil || product == nil {
		t.Fatal("inactive raw clearing constructed a live invalid key", err)
	}
}

func TestActualSetInactiveStorageKeepsOrdinaryZeroedRejection(t *testing.T) {
	root := analyzeTreeTestSourceWithSemanticErrors(t, "set-inactive-invalid.elisa", actualSetInactiveSource(t)+`def invalid() -> sview:
    trusted Unsafe.PointerCast:
        return zeroed
`)
	if errors := strings.Join(root.Errors(), "\n"); !strings.Contains(errors, "`zeroed` cannot construct an `sview`") {
		t.Fatal("raw clearing weakened live-value validity", errors)
	}
}
