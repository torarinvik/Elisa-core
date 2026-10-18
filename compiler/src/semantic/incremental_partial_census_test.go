//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/incremental"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIncrementalPartialOwnerCensusLifecycle(t *testing.T) {
	origin, err := filepath.Abs("partial-census.elisa")
	if err != nil {
		t.Fatal(err)
	}
	makeFile := func(names []string, invalidSuffix bool) *ast.File {
		f := &ast.File{Filename: origin}
		for _, name := range names {
			var value ast.Expr = &ast.Ident{Name: "value"}
			if name == "live_suffix" && invalidSuffix {
				value = &ast.BoolLit{Value: true}
			}
			f.Decls = append(f.Decls, &ast.FuncDecl{Name: name, Params: []ast.ParamDecl{{Name: "value", Type: &ast.NamedType{Name: "i64"}}}, ReturnType: &ast.NamedType{Name: "i64"}, Body: []ast.Stmt{&ast.ReturnStmt{Value: value}}})
		}
		return f
	}
	ownerKey := func(name string) incremental.Key {
		fn := makeFile([]string{name}, false).Decls[0].(*ast.FuncDecl)
		id, _, _ := preparePrimitiveOwnerCandidate(fn, name, origin, nil)
		return primitiveInput("function-source", primitiveOwnerCandidateName(id))
	}
	session := NewIncrementalAnalysisSession()
	session.maxTrackedBodies = 3
	if r := session.Analyze(makeFile([]string{"kept", "deleted", "live_suffix"}, false), AnalyzeOptions{}); len(r.Errors()) > 0 {
		t.Fatal(r.Errors())
	}
	deletedKey, suffixKey, replacementKey := ownerKey("deleted"), ownerKey("live_suffix"), ownerKey("replacement")
	if !session.previous[deletedKey] || !session.previous[suffixKey] {
		t.Fatal("seed owners not tracked")
	}
	names := []string{"kept", "replacement"}
	for len(names) < primitiveOwnerCandidateLimit {
		names = append(names, fmt.Sprintf("filler%d", len(names)))
	}
	names = append(names, "live_suffix")
	cold := Analyze(makeFile(names, true))
	if len(cold.Errors()) == 0 {
		t.Fatal("invalid suffix control did not diagnose")
	}
	for revision := 0; revision < 2; revision++ {
		file := makeFile(names, true)
		candidates, complete := preparePrimitiveOwnerCandidates(file.Decls, origin, session.previous, primitiveOwnerCandidateLimit, false)
		if complete || len(candidates) != primitiveOwnerCandidateLimit {
			t.Fatalf("did not exercise production cutoff: complete=%v candidates=%d", complete, len(candidates))
		}
		before := session.Stats()
		r := session.Analyze(file, AnalyzeOptions{})
		after := session.Stats()
		if !reflect.DeepEqual(r.Errors(), cold.Errors()) {
			t.Fatalf("partial suffix diagnostic differs: warm=%v cold=%v", r.Errors(), cold.Errors())
		}
		if after.CapacityFallbackAnalyses == before.CapacityFallbackAnalyses || after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
			t.Fatalf("expected only retained prefix replay: before=%+v after=%+v", before, after)
		}
		if !session.previous[deletedKey] || !session.previous[suffixKey] || session.previous[replacementKey] {
			t.Fatal("partial census changed conservative unmatched-owner policy")
		}
		if len(session.previous) > session.trackedBodyLimit() {
			t.Fatal("tracked cap exceeded")
		}
		t.Logf("partial%d owners=%d retained=%d deleted_key=true live_suffix_key=true replacement_key=false; cold diagnostic parity", revision, len(names), len(session.previous))
	}
	complete := makeFile([]string{"kept", "replacement", "live_suffix"}, false)
	complete.Decls[2].(*ast.FuncDecl).Body[0].(*ast.ReturnStmt).Value = &ast.IntLit{Value: "7"}
	before := session.Stats()
	r := session.Analyze(complete, AnalyzeOptions{})
	after := session.Stats()
	if len(r.Errors()) > 0 {
		t.Fatal(r.Errors())
	}
	if session.previous[deletedKey] || !session.previous[replacementKey] || !session.previous[suffixKey] {
		t.Fatal("complete census failed retirement/recovery")
	}
	if after.PrimaryBodiesReused-before.PrimaryBodiesReused != 1 {
		t.Fatal("changed suffix reused its old typing product on complete census")
	}
	for key := range session.productBytes {
		if key.Name == deletedKey.Name {
			t.Fatal("deleted owner's retained product bytes were not retired")
		}
	}
	t.Logf("complete retained=%d deleted_key=false replacement_key=true; changed suffix checked cold", len(session.previous))
}
