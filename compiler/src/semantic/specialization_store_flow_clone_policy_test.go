package semantic

import (
	"crypto/sha256"
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStoreFlowCloneKeepsOrdinaryCloneAndOwnedBoundary(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(filename), "parallel_bodies.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*goast.FuncDecl)
		if !ok || fn.Name.Name != "cloneStoreFlowSummariesForBodyWorker" {
			continue
		}
		found = true
		start, end := set.Position(fn.Pos()).Offset, set.Position(fn.End()).Offset
		if fmt.Sprintf("%x", sha256.Sum256(source[start:end])) != "78dac3a17912ff5223dd956992b387de9e3037d034a8537352e4fd1a85aab3ec" {
			t.Fatal("ordinary clone changed; requalify exact reference policy")
		}
	}
	if !found {
		t.Fatal("ordinary reference helper missing")
	}
	if !strings.Contains(string(source), "if ownedFunctionAnalyses {\n\t\t\t\tsettable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreFlowSummariesForSpecialization(a.storeFlowSummaries)))") {
		t.Fatal("owned-only specialization clone boundary changed")
	}
}
