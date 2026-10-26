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

func TestProofBindingGeneratorHandlesNamedStringsAndScalarSlices(t *testing.T) {
	pkg := types.NewPackage("elisacore/src/ast", "ast")
	namedString := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "TestSpelling", nil), types.Typ[types.String], nil)
	parent := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "TestRecord", nil), types.NewStruct(nil, nil), nil)
	var out bytes.Buffer
	field := types.NewVar(token.NoPos, pkg, "Name", namedString)
	if err := emitProofBindingField(&out, parent, field); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("string(p.Name)")) {
		t.Fatal("named string must be converted for the event callback")
	}
	out.Reset()
	if err := emitProofBindingValue(&out, "p.Flags", types.NewSlice(types.Typ[types.Bool]), 0); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("scalar-only slice should emit no loop: %s", out.String())
	}
	out.Reset()
	if err := emitProofBindingValue(&out, "p.Names", types.NewSlice(types.Typ[types.String]), 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("proofBindingVisitString")) {
		t.Fatal("string slices must retain source-name blocker events")
	}
}

func TestCoordinateWalkerPreservesLeavesAndScalarPointerVisits(t *testing.T) {
	var output bytes.Buffer
	namedScalar := types.NewNamed(types.NewTypeName(token.NoPos, types.NewPackage("elisacore/src/ast", "ast"), "NamedScalar", nil), types.Typ[types.String], nil)
	for _, typ := range []types.Type{namedScalar, types.Typ[types.String], types.NewArray(types.Typ[types.Int], 2), types.NewMap(types.Typ[types.String], types.Typ[types.Int])} {
		if err := emitProofCoordinateValue(&output, "value", typ, 0); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() != 0 {
		t.Fatal("coordinate generator visited scalar/array/map leaves")
	}
	if err := emitProofCoordinateValue(&output, "pointer", types.NewPointer(types.Typ[types.Int]), 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("proofCoordinateWalkReflect")) {
		t.Fatal("scalar pointer bypassed original pointer identity guard")
	}
}

func TestCoordinateWalkerDefinedPointersAndInaccessibleASTStructsUseReflection(t *testing.T) {
	pkg := types.NewPackage("elisacore/src/ast", "ast")
	exported := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "Exported", nil), types.NewStruct(nil, nil), nil)
	hidden := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "hidden", nil), types.NewStruct(nil, nil), nil)
	definedPointer := types.NewNamed(types.NewTypeName(token.NoPos, pkg, "DefinedPointer", nil), types.NewPointer(exported), nil)
	for _, typ := range []types.Type{definedPointer, hidden, types.NewPointer(hidden)} {
		var output bytes.Buffer
		if err := emitProofCoordinateValue(&output, "value", typ, 0); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(output.Bytes(), []byte("proofCoordinateWalkReflect")) || bytes.Contains(output.Bytes(), []byte("proofCoordinateWalkPtr_")) || bytes.Contains(output.Bytes(), []byte("proofCoordinateWalkVal_")) {
			t.Fatalf("defined pointer or inaccessible AST shape bypassed exact reflection: %s", output.String())
		}
	}
}
