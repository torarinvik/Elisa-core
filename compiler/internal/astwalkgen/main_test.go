package main

import (
	"bytes"
	"go/token"
	"go/types"
	"os"
	"testing"
)

// The checked-in walker must be the generator's current output: a field added to an AST struct
// that the walker does not visit would silently drop nodes from every region/store analysis.
// Regenerate with: (cd compiler && go run ./internal/astwalkgen)
func TestGeneratedWalkerIsCurrent(t *testing.T) {
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	for _, m := range modes {
		want, err := generate(m)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(m.File)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s is stale; regenerate with go run ./internal/astwalkgen", m.File)
		}
	}
}

func TestCanonicalWalkerDelegatesMapsAndInaccessibleStructs(t *testing.T) {
	g := &gen{maps: true, opaqueFields: true, valFuncs: map[string]bool{}}
	var output bytes.Buffer
	g.emitWalk(&output, "table", types.NewMap(types.Typ[types.String], types.Typ[types.Int]), 0)
	if !bytes.Contains(output.Bytes(), []byte("fillMayAdoptWalkReflect")) {
		t.Fatal("canonical map walk bypassed complete key/value identity policy")
	}
	pkg := types.NewPackage("elisacore/src/ast", "ast")
	hidden := types.NewVar(token.NoPos, pkg, "hidden", types.NewPointer(types.Typ[types.Int]))
	name := types.NewTypeName(token.NoPos, pkg, "OpaqueRecord", nil)
	structure := types.NewNamed(name, types.NewStruct([]*types.Var{hidden}, nil), nil)
	g.needVal(structure)
	for _, generate := range g.queue {
		generate()
	}
	if !bytes.Contains(g.out.Bytes(), []byte("reflect.ValueOf(&*p).Elem()")) || bytes.Contains(g.out.Bytes(), []byte("p.hidden")) {
		t.Fatal("inaccessible AST record did not delegate its full structure")
	}
}
