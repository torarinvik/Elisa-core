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

func actualDictInactiveSource(t *testing.T) string {
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
		boundary := regexp.MustCompile(`(?m)^(?:def |struct |extern |include |@)`)
		next := boundary.FindStringIndex(source[start+len(prefix):])
		if next == nil {
			t.Fatal("missing actual source boundary", prefix)
		}
		return source[start:start+len(prefix)+next[0]] + "\n"
	}
	collections, arena := read("collections.elisa"), read("arena.elisa")
	return "extern memset(dest: mutable void&, val: i32, n: usize) -> mutable void&\n" +
		extract(collections, "struct DictBucket[") + extract(collections, "def collections_mutable_void_from_items[") + extract(arena, "def arena_checked_mul_usize(") + extract(collections, "def arena_dict_zero_buckets[")
}

func TestActualDictInactiveStorageSpecializesSViewAndNonZeroPayload(t *testing.T) {
	source := actualDictInactiveSource(t) + `struct Payload:
    owner: i64&
    text: sview

def probe(items: mutable DictBucket[sview, Payload]&) -> usize:
    return size_of(Payload)
`
	root := analyzeTreeTestSource(t, "dict-inactive.elisa", source)
	probe := retainedProofNamedDecl(t, root, "probe").(*ast.FuncDecl)
	args := root.analyzer.funcDeclSymbols[probe].Type.(*FuncType).Params[0].(*RefType).Elem.(*GenericInstanceType).Args
	helper := retainedProofNamedDecl(t, root, "arena_dict_zero_buckets").(*ast.FuncDecl)
	root.BeginSharedCodegen()
	defer root.EndSharedCodegen()
	product, err := root.SpecializeFunction(helper, args)
	if err != nil || product == nil {
		t.Fatal("inactive storage clearing constructed an invalid live payload", err)
	}
	if len(product.Result.Errors()) != 0 {
		t.Fatal(product.Result.Errors())
	}
}

func TestActualDictInactiveStorageDoesNotRelaxZeroedSView(t *testing.T) {
	source := actualDictInactiveSource(t) + `def invalid() -> sview:
    trusted Unsafe.PointerCast:
        return zeroed
`
	root := analyzeTreeTestSourceWithSemanticErrors(t, "dict-inactive-invalid.elisa", source)
	if errors := strings.Join(root.Errors(), "\n"); !strings.Contains(errors, "`zeroed` cannot construct an `sview`") {
		t.Fatal("raw storage fix weakened ordinary sview validity", errors)
	}
}
