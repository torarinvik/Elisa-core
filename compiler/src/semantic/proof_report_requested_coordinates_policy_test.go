package semantic

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	goast "go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRequestedProofCoordinatesKeepFrozenFullRendererAndTraversal(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Dir(filename)
	old, err := os.ReadFile(filepath.Join(dir, "proof_report_coordinates.go"))
	if err != nil {
		t.Fatal(err)
	}
	reference := string(old)
	start := strings.Index(reference, "func proofReportSyntaxRenderer(")
	if start < 0 {
		t.Fatal("full oracle missing")
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimRight(reference[start:], "\n")))) != "91c48a7639365425bd60d206d4ef376fdb7fbcd3ffaca686d45d155c914b68c5" {
		t.Fatal("full frozen renderer changed; review the actual source oracle")
	}
	filtered, err := os.ReadFile(filepath.Join(dir, "proof_report_requested_coordinates.go"))
	if err != nil {
		t.Fatal(err)
	}

	parse := func(path string, source []byte) *goast.File {
		t.Helper()
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	findFunction := func(file *goast.File, name string) *goast.FuncDecl {
		t.Helper()
		for _, decl := range file.Decls {
			if fn, ok := decl.(*goast.FuncDecl); ok && fn.Name.Name == name {
				return fn
			}
		}
		t.Fatal("required traversal function missing", name)
		return nil
	}
	render := func(node goast.Node) string {
		var output bytes.Buffer
		if err := printer.Fprint(&output, token.NewFileSet(), node); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
	// Normalize only closure plumbing: the reflected visitor's complete branch
	// body must remain exactly the frozen oracle, not a shorter hand-picked walk.
	oracle := findFunction(parse("oracle", old), "proofReportSyntaxRenderer")
	var frozenVisitor *goast.BlockStmt
	goast.Inspect(oracle.Body, func(node goast.Node) bool {
		assignment, ok := node.(*goast.AssignStmt)
		if !ok || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
			return true
		}
		name, ok := assignment.Lhs[0].(*goast.Ident)
		if !ok || name.Name != "visit" {
			return true
		}
		literal, ok := assignment.Rhs[0].(*goast.FuncLit)
		if ok {
			frozenVisitor = literal.Body
		}
		return true
	})
	if frozenVisitor == nil {
		t.Fatal("frozen complete reflective visitor missing")
	}
	goast.Inspect(frozenVisitor, func(node goast.Node) bool {
		if call, ok := node.(*goast.CallExpr); ok {
			if name, ok := call.Fun.(*goast.Ident); ok && name.Name == "visit" {
				name.Name = "proofCoordinateWalkReflect"
				call.Args = append(call.Args, goast.NewIdent("add"), goast.NewIdent("seen"))
			}
		}
		if name, ok := node.(*goast.Ident); ok && name.Name == "addPosition" {
			name.Name = "add"
		}
		return true
	})
	fallbackSource, err := os.ReadFile(filepath.Join(dir, "proof_coordinate_walk.go"))
	if err != nil {
		t.Fatal(err)
	}
	fallback := findFunction(parse("fallback", fallbackSource), "proofCoordinateWalkReflect")
	if render(frozenVisitor) != render(fallback.Body) {
		t.Fatal("unknown/inaccessible coordinate fallback differs from frozen complete Node.Pos/Pos-field oracle")
	}

	capture := findFunction(parse("capture", filtered), "captureProofReportCoordinateWitnesses")
	rootCalls, sharedSeen := 0, false
	goast.Inspect(capture.Body, func(node goast.Node) bool {
		if assignment, ok := node.(*goast.AssignStmt); ok && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
			if name, ok := assignment.Lhs[0].(*goast.Ident); ok && name.Name == "seen" && render(assignment.Rhs[0]) == "map[uintptr]bool{}" {
				sharedSeen = true
			}
		}
		if call, ok := node.(*goast.CallExpr); ok {
			if name, ok := call.Fun.(*goast.Ident); ok && name.Name == "proofCoordinateASTWalkTyped" {
				rootCalls++
				if render(call) != "proofCoordinateASTWalkTyped(file, addPosition, seen)" {
					t.Fatal("typed capture no longer walks complete source root with shared pointer seen set")
				}
			}
		}
		return true
	})
	if rootCalls != 1 || !sharedSeen {
		t.Fatal("complete typed source root/shared pointer traversal missing")
	}
	generatedSource, err := os.ReadFile(filepath.Join(dir, "proof_coordinate_walk_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	entry := findFunction(parse("generated", generatedSource), "proofCoordinateASTWalkTyped")
	if len(entry.Body.List) != 1 || render(entry.Body.List[0]) != "proofCoordinateWalkPtr_ast_File(root, add, seen)" {
		t.Fatal("typed entry no longer delegates full File with identical callback and seen set")
	}
	// The normal astwalkgen currentness gate ranges every mode. Require this
	// generated file to remain in that inventory; exact stream tests independently
	// compare its all-node output with the frozen reflection oracle.
	generatorSource, err := os.ReadFile(filepath.Join(dir, "../../internal/astwalkgen/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	generator := parse("generator", generatorSource)
	covered := false
	goast.Inspect(generator, func(node goast.Node) bool {
		literal, ok := node.(*goast.CompositeLit)
		if !ok {
			return true
		}
		file, enabled := "", false
		for _, element := range literal.Elts {
			field, ok := element.(*goast.KeyValueExpr)
			if !ok {
				continue
			}
			name, ok := field.Key.(*goast.Ident)
			if !ok {
				continue
			}
			if name.Name == "File" {
				if value, ok := field.Value.(*goast.BasicLit); ok {
					file = strings.Trim(value.Value, "\"")
				}
			}
			if name.Name == "ProofCoordinate" {
				enabled = render(field.Value) == "true"
			}
		}
		if file == "src/semantic/proof_coordinate_walk_gen.go" && enabled {
			covered = true
		}
		return true
	})
	if !covered {
		t.Fatal("coordinate traversal omitted from generated-currentness mode inventory")
	}
	generatorGate, err := os.ReadFile(filepath.Join(dir, "../../internal/astwalkgen/main_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"for _, m := range modes", "want, err := generate(m)", "bytes.Equal(got, want)"} {
		if !strings.Contains(string(generatorGate), required) {
			t.Fatal("all-mode generated currentness gate missing", required)
		}
	}
}
