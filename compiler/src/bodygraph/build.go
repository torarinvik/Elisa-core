// Package bodygraph builds conservative body scheduling dependencies.
package bodygraph

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"sort"
)

type Graph struct {
	Deps          [][]int
	Weight        []int
	Functions     []*ast.FuncDecl
	FunctionDeps  [][]int
	FuncRecursive []bool
}

func Build(bodies []ast.Decl, walk func([]ast.Stmt, func(any))) *Graph {
	definers := map[string][]int{}
	for i, scoped := range bodies {
		for _, name := range BodyDefinedNames(scoped) {
			definers[name] = append(definers[name], i)
		}
	}
	g := &Graph{Deps: make([][]int, len(bodies)), Weight: make([]int, len(bodies))}
	functionIndexByDecl := map[*ast.FuncDecl]int{}
	functionDefiners := map[string][]int{}
	for _, scoped := range bodies {
		for _, fn := range BodyFunctions(scoped) {
			functionIndex := len(g.Functions)
			g.Functions = append(g.Functions, fn)
			functionIndexByDecl[fn] = functionIndex
			name := LastSegment(fn.Name)
			functionDefiners[name] = append(functionDefiners[name], functionIndex)
		}
	}
	g.FunctionDeps = make([][]int, len(g.Functions))
	g.FuncRecursive = make([]bool, len(g.Functions))
	// Dependency targets are dense indices. Reuse generation-mark arrays rather
	// than allocating a hash set for each body and function.
	bodySeen := make([]int, len(bodies))
	functionSeen := make([]int, len(g.Functions))
	functionGeneration := 0
	for i, scoped := range bodies {
		bodyGeneration := i + 1
		for _, fn := range BodyFunctions(scoped) {
			functionIndex := functionIndexByDecl[fn]
			functionGeneration++
			generation := functionGeneration
			note := func(name string) {
				for _, j := range definers[LastSegment(name)] {
					if j != i && bodySeen[j] != bodyGeneration {
						bodySeen[j] = bodyGeneration
						g.Deps[i] = append(g.Deps[i], j)
					}
				}
				for _, target := range functionDefiners[LastSegment(name)] {
					if target == functionIndex {
						g.FuncRecursive[functionIndex] = true
						continue
					}
					if functionSeen[target] != generation {
						functionSeen[target] = generation
						g.FunctionDeps[functionIndex] = append(g.FunctionDeps[functionIndex], target)
					}
				}
			}
			noteBodyDependency := func(name string) {
				for _, j := range definers[LastSegment(name)] {
					if j != i && bodySeen[j] != bodyGeneration {
						bodySeen[j] = bodyGeneration
						g.Deps[i] = append(g.Deps[i], j)
					}
				}
			}
			scan := func(stmts []ast.Stmt, functionRefs bool) {
				walk(stmts, func(node any) {
					g.Weight[i]++
					dependency := noteBodyDependency
					if functionRefs {
						dependency = note
					}
					switch n := node.(type) {
					case *ast.Ident:
						dependency(n.Name)
					case *ast.FieldExpr:
						dependency(n.Field)
					case *ast.BinaryExpr:
						if method := BinaryOperatorMethod(n.Op); method != "" {
							noteBodyDependency(method)
						}
						if n.Op == lexer.TOKEN_PLUS {
							// Span algebra can also lower `+` to a protocol method or a
							// visible helper after this pre-analysis graph is built.
							noteBodyDependency("combine")
							noteBodyDependency("combine_span")
							noteBodyDependency("lua_span_union")
						}
					case *ast.UnaryExpr:
						if method := UnaryOperatorMethod(n.Op); method != "" {
							noteBodyDependency(method)
						}
					case ast.Expr:
						if name, _, ok := ast.PrepassCallShape(n); ok {
							dependency(name)
						}
					}
				})
			}
			scan(FuncScanStmts(fn), true)
			scan(FuncDefaultArgStmts(fn), false)
			sort.Ints(g.FunctionDeps[functionIndex])
		}
		sort.Ints(g.Deps[i])
	}
	return g
}
