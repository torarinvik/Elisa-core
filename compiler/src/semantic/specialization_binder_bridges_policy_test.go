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

func TestStoreScanBinderOwnedReflectionBridgesRemainClosed(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	golden := map[string]string{"region_store_scan_pattern_binder.go|storeScanPatternBinderType": "8a5566b998aaeffc9d0cdd25caee38dff9565609949efc1b2fa9f15f888c5acb", "analyzer_store_scan_binder_overlay.go|ownedStoreScanBinderRow": "3cd7ac101ffd193c66d151c92b1c3c56dbae77d551284858a548c7a5e1f097aa", "analyzer_store_scan_binder_overlay.go|mutableStoreScanBinderCache": "1b3ae8a0f2c3fe9f42d341b0980aa743302a15746da7b3081078caa8d944786f", "analyzer_store_scan_binder_overlay.go|rekeyStoreScanBinderRow": "396b1cc44f364800b65c77bb9707bf7f30a513f99e64f757bbc2cf32e1c7d7e7", "parallel_ownership.go|rekeyFuncDeclEntries": "3510a97e46c5c4d3c9de4efaee7e1f19823bcd3213377cf931a812715701f4af", "parallel_ownership.go|readDecl": "b957d59f91e2262e265fed92acb12524c8f134e07a21704898eab9a276178188", "specialization_view.go|copyDecl": "5375681fb60ede4a402b02bba9579f7810d64a056470a816b75aa0d372f49393"}

	for key, expected := range golden {
		file, name, _ := strings.Cut(key, "|")
		path := filepath.Join(directory, file)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		set := token.NewFileSet()
		parsed, err := parser.ParseFile(set, path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*goast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			found = true
			start, end := set.Position(fn.Pos()).Offset, set.Position(fn.End()).Offset
			if fmt.Sprintf("%x", sha256.Sum256(source[start:end])) != expected {
				t.Errorf("review complete ownership/source bridge before accepting change: %s", key)
			}
		}
		if !found {
			t.Fatal("reviewed bridge missing", key)
		}
	}
}

func TestStoreScanBinderCopyDeclSourcesAreCanonical(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	directory := filepath.Dir(filename)
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	constructors := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		set := token.NewFileSet()
		source, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(set, entry.Name(), source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*goast.FuncDecl)
			if !ok {
				continue
			}
			goast.Inspect(function.Body, func(node goast.Node) bool {
				if literal, ok := node.(*goast.CompositeLit); ok {
					if name, ok := literal.Type.(*goast.Ident); ok && name.Name == "specializationContext" {
						constructors++
						if entry.Name() != "specialization_view.go" || function.Name.Name != "specializePrivateFunction" {
							t.Errorf("new specialization base producer requires source-authority review at %s", set.Position(node.Pos()))
						}
						baseIsReceiver := false
						for _, element := range literal.Elts {
							if pair, ok := element.(*goast.KeyValueExpr); ok {
								key, _ := pair.Key.(*goast.Ident)
								value, _ := pair.Value.(*goast.Ident)
								if key != nil && value != nil && key.Name == "base" && value.Name == "r" {
									baseIsReceiver = true
								}
							}
						}
						if !baseIsReceiver {
							t.Error("context no longer uses validated canonical receiver")
						}
					}
				}
				if assignment, ok := node.(*goast.AssignStmt); ok {
					for _, lhs := range assignment.Lhs {
						if selector, ok := lhs.(*goast.SelectorExpr); ok && selector.Sel.Name == "base" {
							if receiver, ok := selector.X.(*goast.Ident); ok && (receiver.Name == "c" || receiver.Name == "ctx") {
								t.Errorf("specialization base rebinding requires review at %s", set.Position(lhs.Pos()))
							}
						}
					}
				}
				return true
			})
		}
	}
	if constructors != 1 {
		t.Fatal("unexpected specialization context producer count", constructors)
	}
	source, err := os.ReadFile(filepath.Join(directory, "specialization_view.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	factory := strings.Index(text, "func (r *Result) specializePrivateFunction(")
	context := strings.Index(text[factory:], "ctx := &specializationContext")
	if factory < 0 || context < 0 {
		t.Fatal("factory/context boundary missing")
	}
	if guard := strings.Index(text[factory:], "r.analyzer.storeScanBinderOverlay != nil"); guard < 0 || guard >= context {
		t.Fatal("worker overlay can become copyDecl canonical source")
	}
	if !strings.Contains(text, "root = r.specializationRoot") || !strings.Contains(text, "root.specializePrivateFunction(fn, args)") {
		t.Fatal("private views no longer route construction through canonical root")
	}
}
