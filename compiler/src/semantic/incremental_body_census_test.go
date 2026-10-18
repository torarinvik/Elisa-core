//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"
)

// Opt-in syntax census identifies admission barriers without changing the
// production cache or treating a syntactic candidate as safe body reuse.
func TestLocalIncrementalBodyAdmissionCensus(t *testing.T) {
	path := os.Getenv("ELISA_LOOKUP_QUERY_PROBE_SNAPSHOT")
	if path == "" {
		t.Skip("set frozen project snapshot")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct{ Filename, Source string }
	if err = json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	lex := lexer.New(snapshot.Filename, []byte(snapshot.Source))
	p := parser.New(lex.Tokenize())
	file := p.ParseFile(snapshot.Filename)
	if len(lex.Errors()) != 0 || len(p.Errors()) != 0 {
		t.Fatal("frozen syntax changed")
	}
	counts := map[string]int{}
	var visit func([]ast.Decl)
	visit = func(decls []ast.Decl) {
		for _, decl := range decls {
			switch fn := decl.(type) {
			case *ast.NamespaceDecl:
				visit(fn.Decls)
			case *ast.ImplDecl:
				for _, member := range fn.Members {
					if nested, ok := member.(ast.Decl); ok {
						visit([]ast.Decl{nested})
					}
				}
			case *ast.FuncDecl:
				counts["functions"]++
				// This is a syntax opportunity census, not authorization to reuse.
				// Inspect top-level scalar locals independently of return types,
				// unused non-scalar parameters and unrelated sibling statements.
				scalarNames := map[string]bool{}
				for _, param := range fn.Params {
					name := predicateBuiltinSyntaxName(param.Type)
					if (name == "int" || name == "i64") && !param.Mutable && param.DefaultValue == nil {
						scalarNames[param.Name] = true
					}
				}
				prospectiveLocals, multiNodeLocals := 0, 0
				for _, stmt := range fn.Body {
					decl, ok := stmt.(*ast.VarDeclStmt)
					if !ok || decl == nil || decl.Value == nil {
						continue
					}
					counts["direct-local-declarations"]++
					counts["direct-local-value-shape/"+fmt.Sprintf("%T", decl.Value)]++
					declared := decl.Type
					_, mutableType := declared.(*ast.MutableType)
					if wrapped, ok := declared.(*ast.MutableType); ok {
						declared = wrapped.Elem
					}
					name := predicateBuiltinSyntaxName(declared)
					if name != "int" && name != "i64" {
						continue
					}
					counts["direct-scalar-locals/value-shape/"+fmt.Sprintf("%T", decl.Value)]++
					if decl.Mutable || mutableType {
						counts["direct-mutable-scalar-locals"]++
					} else {
						counts["direct-immutable-scalar-locals"]++
					}
					copy := *decl
					copy.Position, copy.Name, copy.Type, copy.Value = lexer.Pos{}, "", nil, nil
					copy.BindingExplicit = false
					copy.Mutable = false // target mutability is counted, not authorized for reuse
					if !reflect.ValueOf(copy).IsZero() {
						continue
					}
					if nodes, _, eligible := primitiveI64ExpressionNodes(decl.Value, scalarNames, 0); eligible {
						prospectiveLocals++
						if len(nodes) > 1 {
							multiNodeLocals++
						}
					}
					if !decl.Mutable && !mutableType {
						scalarNames[decl.Name] = true
					}
				}
				if prospectiveLocals > 0 {
					counts["prospective-top-level-scalar-locals"] += prospectiveLocals
					counts["prospective-multi-node-scalar-locals"] += multiNodeLocals
					if _, admitted := primitiveLocalInitializers(fn); !admitted {
						counts["functions-with-excluded-scalar-locals"]++
						counts["excluded-top-level-scalar-locals"] += prospectiveLocals
						counts["excluded-multi-node-scalar-locals"] += multiNodeLocals
						if multiNodeLocals > 0 {
							t.Logf("excluded multi-node local candidate: %s locals=%d multi-node=%d", fn.Name, prospectiveLocals, multiNodeLocals)
						}
					}
				}

				if _, ok := predicateReturnExpressions(fn); ok {
					counts["predicate-return-syntax"]++
					t.Logf("predicate syntax candidate: %s", fn.Name)
				}

				if _, ok := primitiveReturnExpressions(fn); ok {
					counts["admitted-return-syntax"]++
				}
				prospective := *fn
				prospective.Params = append([]ast.ParamDecl(nil), fn.Params...)
				widenInt := func(input ast.TypeExpr) ast.TypeExpr {
					switch typ := input.(type) {
					case *ast.NamedType:
						if typ != nil && typ.Name == "int" {
							copy := *typ
							copy.Name = "i64"
							return &copy
						}
					case *ast.BuiltinTypeExpr:
						if typ != nil && typ.Name == "int" {
							copy := *typ
							copy.Name = "i64"
							return &copy
						}
					}
					return input
				}
				prospective.ReturnType = widenInt(fn.ReturnType)
				for i := range prospective.Params {
					prospective.Params[i].Type = widenInt(prospective.Params[i].Type)
				}
				if _, ok := primitiveReturnExpressions(&prospective); ok {
					counts["prospective-int-return-syntax"]++
					t.Logf("int syntax candidate: %s", fn.Name)
				}

				if _, ok := primitiveLocalInitializers(fn); ok {
					counts["admitted-local-syntax"]++
				}
				if len(fn.Body) != 1 {
					continue
				}
				ret, ok := fn.Body[0].(*ast.ReturnStmt)
				if !ok || ret.Value == nil {
					continue
				}
				counts["single-return"]++
				name := fmt.Sprintf("%T", fn.ReturnType)
				switch typ := fn.ReturnType.(type) {
				case *ast.NamedType:
					name = typ.Name
				case *ast.BuiltinTypeExpr:
					name = typ.Name
				}
				counts["return-type/"+name]++
				counts["return-expression/"+name+"/"+fmt.Sprintf("%T", ret.Value)]++
				if binary, ok := ret.Value.(*ast.BinaryExpr); ok {
					counts["return-binary-op/"+name+"/"+fmt.Sprintf("%v", binary.Op)]++
				}
				counts["expression/"+fmt.Sprintf("%T", ret.Value)]++
				header := *fn
				header.Params = append([]ast.ParamDecl(nil), fn.Params...)
				header.ReturnType = &ast.NamedType{Name: "i64"}
				for i := range header.Params {
					header.Params[i].Type = &ast.NamedType{Name: "i64"}
				}
				header.Body = []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}
				if _, ok := primitiveReturnHeader(&header); ok {
					counts["otherwise-simple-header/"+name]++
				}
			}
		}
	}
	visit(file.Decls)
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		t.Logf("%s=%d", key, counts[key])
	}
}
