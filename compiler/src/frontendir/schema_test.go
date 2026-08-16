package frontendir

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// TestSchemaRootsMatchSource is the anti-rot guard on the generated root list.
// Reflection cannot enumerate the implementations of an interface, so the node
// types have to be named somewhere; this fails when a type is added to package
// ast and the list is not regenerated, instead of letting the omission surface as
// an encode failure on a user's file.
func TestSchemaRootsMatchSource(t *testing.T) {
	names, err := ScanASTStructNames("../ast")
	if err != nil {
		t.Fatalf("scan ../ast: %v", err)
	}
	listed := map[string]bool{}
	for _, rt := range schemaRootTypes {
		listed[rt.Name()] = true
	}
	var missing []string
	for _, name := range names {
		if !listed[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("schema_roots_gen.go is stale — run `go run ./src/frontendir/gen`; missing %d type(s): %v", len(missing), missing)
	}
	if len(listed) != len(names) {
		t.Errorf("root list has %d types, package ast declares %d", len(listed), len(names))
	}
}

// TestSchemaMatchesRegistry checks that every type and field the compiler can
// emit has a committed ID. LoadSchema fails loudly on a gap, which is what makes
// adding a field a deliberate schema change rather than a silent format break.
func TestSchemaMatchesRegistry(t *testing.T) {
	if _, err := LoadSchema(); err != nil {
		t.Fatalf("%v", err)
	}
}

// TestRegistryRegenerationIsStable checks that regenerating produces the
// committed file byte for byte. A diff here means the working tree's registry was
// hand-edited or is stale.
func TestRegistryRegenerationIsStable(t *testing.T) {
	got, err := RegenerateRegistry()
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if got != registrySource {
		t.Errorf("schema_registry.txt is not what regeneration produces — run `go run ./src/frontendir/gen`")
	}
}

// TestRegistryRejectsIDReuse pins the append-only rule the format depends on: an
// ID that changes meaning silently reinterprets every bundle already on disk.
func TestRegistryRejectsIDReuse(t *testing.T) {
	for _, bad := range []struct{ name, text string }{
		{"type ID reused", "T 1 Alpha\nT 1 Beta\n"},
		{"type assigned twice", "T 1 Alpha\nT 2 Alpha\n"},
		{"field ID reused", "T 1 Alpha\nF Alpha 1 One\nF Alpha 1 Two\n"},
		{"field assigned twice", "T 1 Alpha\nF Alpha 1 One\nF Alpha 2 One\n"},
	} {
		if _, err := parseRegistry(bad.text); err == nil {
			t.Errorf("%s: accepted, want an error", bad.name)
		}
	}
}

// TestBundlePreservesNodeIdentity is the regression test for the defect that
// motivated the v2 format.
//
// `ast.File.DeclVisibility` is keyed by the decl POINTER. gob has no notion of
// node identity — it wrote each key as a fresh value — so after a round-trip the
// map's keys matched nothing in Decls, every lookup fell through to the default,
// and a `public:` section inside a `private module` lost its mark. The same
// program then compiled from source and was REJECTED from its own `.elisair`.
func TestBundlePreservesNodeIdentity(t *testing.T) {
	decl := &ast.ConstDecl{Name: "K", Position: lexer.Pos{File: "probe.elisa", Line: 3, Col: 5}}
	file := &ast.File{
		Filename:       "probe.elisa",
		Decls:          []ast.Decl{decl},
		DeclVisibility: map[ast.Decl]string{decl: "public"},
	}
	blob, err := Encode(&Bundle{SourceFilename: "probe.elisa", File: file})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.File.Decls) != 1 {
		t.Fatalf("decls = %d, want 1", len(got.File.Decls))
	}
	visibility, ok := got.File.DeclVisibility[got.File.Decls[0]]
	if !ok {
		t.Fatalf("decoded Decls[0] is not a key of decoded DeclVisibility: node identity was lost")
	}
	if visibility != "public" {
		t.Errorf("visibility = %q, want public", visibility)
	}
}

// TestBundlePreservesSharedNodes checks the other half of identity: a node
// reachable by two paths must stay ONE node, not be duplicated into two.
func TestBundlePreservesSharedNodes(t *testing.T) {
	shared := &ast.Ident{Name: "x"}
	file := &ast.File{
		Filename: "shared.elisa",
		Decls: []ast.Decl{
			&ast.ConstDecl{Name: "a", Value: shared},
			&ast.ConstDecl{Name: "b", Value: shared},
		},
	}
	blob, err := Encode(&Bundle{File: file})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	first := got.File.Decls[0].(*ast.ConstDecl).Value
	second := got.File.Decls[1].(*ast.ConstDecl).Value
	if first != second {
		t.Errorf("a shared node decoded as two distinct nodes (%p vs %p)", first, second)
	}
}

// TestBundleEncodesNilSliceElements pins a case gob could not encode at all:
// `gob: encodeArray: nil element`, which made `-emit ir` fail outright on real
// programs. A nil element is just node reference 0.
func TestBundleEncodesNilSliceElements(t *testing.T) {
	file := &ast.File{
		Filename: "nil.elisa",
		Decls: []ast.Decl{
			&ast.ConstDecl{Name: "a", Value: &ast.ListLitExpr{Elems: []ast.Expr{nil, &ast.Ident{Name: "y"}}}},
		},
	}
	blob, err := Encode(&Bundle{File: file})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := Decode(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	list := got.File.Decls[0].(*ast.ConstDecl).Value.(*ast.ListLitExpr)
	if len(list.Elems) != 2 {
		t.Fatalf("elements = %d, want 2", len(list.Elems))
	}
	if list.Elems[0] != nil {
		t.Errorf("element 0 = %v, want nil", list.Elems[0])
	}
	if name, ok := list.Elems[1].(*ast.Ident); !ok || name.Name != "y" {
		t.Errorf("element 1 = %v, want Ident y", list.Elems[1])
	}
}

// TestDecodeRejectsForeignData checks the header guard, so a mistaken input fails
// with a clear message rather than a reflect panic.
func TestDecodeRejectsForeignData(t *testing.T) {
	for _, bad := range [][]byte{nil, []byte("not an elisair file"), append(append([]byte{}, Magic...), 0xff)} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("Decode(%q) succeeded, want an error", bad)
		}
	}
}

// TestUnknownFieldsAreSkipped is the forward-compatibility guarantee that lets an
// implementation with a smaller AST read these files: a field the reader does not
// model is skipped, and everything around it still decodes.
func TestUnknownFieldsAreSkipped(t *testing.T) {
	schema, err := LoadSchema()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	file := &ast.File{
		Filename: "skip.elisa",
		Decls:    []ast.Decl{&ast.ConstDecl{Name: "a", Position: lexer.Pos{Line: 9}}},
	}
	blob, err := encodeBundle(schema, &Bundle{File: file})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// A reader that does not know ConstDecl.Position at all.
	reduced, err := LoadSchema()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	target, _ := reduced.TypeByName("ConstDecl")
	kept := target.Fields[:0]
	for _, f := range target.Fields {
		if f.Name != "Position" {
			kept = append(kept, f)
		}
	}
	target.Fields = kept

	got, err := decodeBundle(reduced, blob)
	if err != nil {
		t.Fatalf("a bundle carrying an unmodelled field failed to decode: %v", err)
	}
	decl := got.File.Decls[0].(*ast.ConstDecl)
	if decl.Name != "a" {
		t.Errorf("Name = %q, want a", decl.Name)
	}
	if decl.Position.Line != 0 {
		t.Errorf("Position survived a reader that does not model it: %+v", decl.Position)
	}
}

// TestEncodeIsDeterministic guards reproducible output: map iteration order is
// randomised in Go, so an unsorted encoder would make byte-identical rebuilds
// impossible.
func TestEncodeIsDeterministic(t *testing.T) {
	build := func() *ast.File {
		a := &ast.ConstDecl{Name: "a"}
		b := &ast.ConstDecl{Name: "b"}
		c := &ast.ConstDecl{Name: "c"}
		return &ast.File{
			Filename:       "det.elisa",
			Decls:          []ast.Decl{a, b, c},
			DeclVisibility: map[ast.Decl]string{a: "public", b: "private", c: "public"},
		}
	}
	first, err := Encode(&Bundle{File: build()})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	for i := 0; i < 20; i++ {
		next, err := Encode(&Bundle{File: build()})
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if !reflect.DeepEqual(first, next) {
			t.Fatalf("encoding is not deterministic (attempt %d differs)", i)
		}
	}
}
