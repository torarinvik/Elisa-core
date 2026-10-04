package frontendir

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestBundleRoundTripsTypestateModes(t *testing.T) {
	for _, derived := range []bool{false, true} {
		t.Run(map[bool]string{false: "protocol", true: "derived_block"}[derived], func(t *testing.T) {
			decl := &ast.StructDecl{
				Name: "File", HasStateParam: true, StateParamCount: 1,
				NamedStateCases: []string{"Closed", "Open"}, Affine: true,
				HasDerivedStateBlock: derived,
			}
			if !derived {
				decl.StateTransitions = []ast.StateTransitionDecl{
					{Position: lexer.Pos{File: "states.elisa", Line: 4, Col: 9}, From: "Closed", To: "Open"},
					{Position: lexer.Pos{File: "states.elisa", Line: 5, Col: 9}, From: "Open", To: "Closed"},
				}
			}
			data, err := Encode(&Bundle{File: &ast.File{Decls: []ast.Decl{decl}}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := Decode(data)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.File.Decls) != 1 || !reflect.DeepEqual(got.File.Decls[0], decl) {
				t.Fatalf("typestate mode or transition graph lost during IR round-trip: %#v", got.File.Decls)
			}
		})
	}
}
