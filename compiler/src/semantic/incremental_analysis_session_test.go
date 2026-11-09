//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
	"fmt"
	"reflect"
	"testing"
)

func parseIncrementalSessionFile(t *testing.T, source string) *ast.File {
	t.Helper()
	l := lexer.New("incremental_session.elisa", []byte(source))
	tokens := l.Tokenize()
	if len(l.Errors()) != 0 {
		t.Fatal(l.Errors())
	}
	p := parser.New(tokens)
	file := p.ParseFile("incremental_session.elisa")
	if len(p.Errors()) != 0 {
		t.Fatal(p.Errors())
	}
	return file
}
func assertIncrementalResultParity(t testing.TB, cold, warm *Result, extra ...map[PositionedFunctionNode]PositionedFunctionNode) {
	t.Helper()
	// Re-key public source-node maps onto the current invocation, then compare
	// every published field except invocation objects/costs. No output inventory
	// is silently limited to the maps restored by the cache.
	mapping := map[PositionedFunctionNode]PositionedFunctionNode{}
	for _, supplied := range extra {
		for source, current := range supplied {
			mapping[source] = current
		}
	}
	mapIncrementalCurrentAST(t, reflect.ValueOf(cold.File), reflect.ValueOf(warm.File), mapping)
	collect := func(file *ast.File) map[string]*ast.FuncDecl {
		out := map[string]*ast.FuncDecl{}
		var walk func([]ast.Decl, string)
		walk = func(decls []ast.Decl, namespace string) {
			for ordinal, decl := range decls {
				path := fmt.Sprintf("%s/%d", namespace, ordinal)
				switch node := decl.(type) {
				case *ast.NamespaceDecl:
					walk(node.Decls, path)
				case *ast.ImplDecl:
					for memberIndex, member := range node.Members {
						if fn, ok := member.(*ast.FuncDecl); ok {
							out[fmt.Sprintf("%s/member/%d", path, memberIndex)] = fn
						}
					}
				case *ast.FuncDecl:
					out[path] = node
				}
			}
		}
		walk(file.Decls, "")
		return out
	}
	currentFunctions := collect(warm.File)
	for name, fn := range collect(cold.File) {
		other := currentFunctions[name]
		digest, nodes, err := functionSyntaxFingerprint(fn)
		if err != nil {
			t.Fatal(err)
		}
		currentDigest, current, err := functionSyntaxFingerprint(other)
		if err != nil || digest != currentDigest || len(nodes) != len(current) {
			t.Fatal("current syntax differs")
		}
		for i, node := range nodes {
			mapping[node] = current[i]
		}
		left := cold.analyzer.funcDeclSymbols[fn]
		right := warm.analyzer.funcDeclSymbols[other]
		if left == nil || right == nil {
			// Named contracts are non-callable specifications. Their paired
			// syntax was checked above; neither analysis may invent a symbol.
			if fn.IsContract && other.IsContract && left == nil && right == nil {
				continue
			}
			t.Fatalf("missing function symbol at declaration path %s", name)
		}
		if !reflect.DeepEqual(left.Type, right.Type) {
			t.Fatalf("function type/summary differs: %s", name)
		}
	}
	left, right := reflect.ValueOf(*cold), reflect.ValueOf(*warm)
	baseMap, reverseBaseMap := map[string]string{}, map[string]string{}
	baseNode, reverseBaseNode := map[string]any{}, map[string]any{}
	for i := 0; i < left.NumField(); i++ {
		name := left.Type().Field(i).Name
		switch name {
		case "File", "LoweredFile", "GlobalScope", "SMTProfile", "analyzer", "shared":
			continue
		case "specializationRoot", "specializationCanonical", "specializationCurrent",
			"specializationNodes", "specializationOwned", "specializationProofMask",
			"specializationSpanProofs", "specializationQueries":
			// These are private codegen products, never canonical incremental
			// analysis state. Check their absence without Interface on private
			// fields; retain exact public metadata comparisons below.
			if !left.Field(i).IsZero() || !right.Field(i).IsZero() {
				t.Fatalf("canonical parity input retains specialization context: %s: coldZero=%v warmZero=%v", name, left.Field(i).IsZero(), right.Field(i).IsZero())
			}
			continue
		}
		a, b := left.Field(i), right.Field(i)
		if a.Kind() == reflect.Map && !a.IsNil() {
			remapped := reflect.MakeMapWithSize(a.Type(), a.Len())
			iter := a.MapRange()
			for iter.Next() {
				key := iter.Key()
				if node, ok := key.Interface().(PositionedFunctionNode); ok {
					current, found := mapping[node]
					if !found {
						t.Fatalf("unaccounted source key in %s: %T", name, node)
					}
					value := reflect.ValueOf(current)
					if !value.Type().AssignableTo(a.Type().Key()) {
						t.Fatalf("invalid key kind in %s", name)
					}
					key = value
				}
				value := iter.Value()
				if name == "ExprFacts" {
					fact := value.Interface().(OptimizationFacts)
					current := warm.ExprFacts[key.Interface().(ast.Expr)]
					if (fact.base == "") != (current.base == "") {
						t.Fatalf("optimization base presence differs at %s: cold=%#v warm=%#v", incrementalParitySourceKey(warm.File, key.Interface()), fact, current)
					}
					if fact.base != "" {
						if prior, ok := baseMap[fact.base]; ok && prior != current.base {
							t.Fatalf("optimization alias relation differs at %s: cold base=%q warm base=%q; prior node=%s mapped same cold base to %q; cold=%#v warm=%#v", incrementalParitySourceKey(warm.File, key.Interface()), fact.base, current.base, incrementalParitySourceKey(warm.File, baseNode[fact.base]), prior, fact, current)
						}
						if prior, ok := reverseBaseMap[current.base]; ok && prior != fact.base {
							t.Fatalf("optimization alias relation merged at %s: cold base=%q warm base=%q; prior node=%s mapped different cold base=%q to same warm base; cold=%#v warm=%#v", incrementalParitySourceKey(warm.File, key.Interface()), fact.base, current.base, incrementalParitySourceKey(warm.File, reverseBaseNode[current.base]), prior, fact, current)
						}
						baseMap[fact.base] = current.base
						reverseBaseMap[current.base] = fact.base
						baseNode[fact.base] = key.Interface()
						reverseBaseNode[current.base] = key.Interface()
						fact.base = current.base // invocation-local identity; preserve the alias relation
					}
					value = reflect.ValueOf(fact)
				}
				remapped.SetMapIndex(key, value)
			}
			a = remapped
		}
		if !reflect.DeepEqual(a.Interface(), b.Interface()) {
			t.Fatalf("published metadata differs: %s: %s", name, retainedCanonicalFirstDifference(a, b))
		}
	}
}

func incrementalParitySourceKey(file *ast.File, node any) string {
	for _, decl := range retainedWalkDecls(file) {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ordinal, label := 0, ""
		visit := func(value any) {
			if current, ok := value.(PositionedFunctionNode); ok {
				if current == node {
					label = fmt.Sprintf("%s/body-node[%d] %T %+v", fn.Name, ordinal, current, current)
				}
				ordinal++
			}
		}
		walkStoreScanNodes(funcScanStmts(fn), visit)
		walkStoreScanNodes(funcDefaultArgStmts(fn), visit)
		if label != "" {
			return label
		}
	}
	return fmt.Sprintf("%T %+v", node, node)
}
