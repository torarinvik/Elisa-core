package semantic

import (
	"fmt"
	"os"
	"sort"

	"elisacore/src/ast"
)

// bodyGraph is the conservative dependency graph of body declarations used for
// callee-first parallel analysis: body i depends on body j when i names anything
// j defines (a function, or a method of an impl), matched by last name segment.
// Missing an edge would let a caller read facts a callee's worker is writing, so
// the match is deliberately generous.
type bodyGraph struct {
	deps   [][]int // deps[i]: bodies i depends on
	weight []int   // AST nodes per body
}

func bodyDefinedNames(d ast.Decl) []string {
	switch n := d.(type) {
	case *ast.FuncDecl:
		return []string{fillMayAdoptLastSegment(n.Name)}
	case *ast.ImplDecl:
		var names []string
		for _, m := range n.Members {
			if fn, ok := m.(*ast.FuncDecl); ok {
				names = append(names, fillMayAdoptLastSegment(fn.Name))
			}
		}
		return names
	}
	return nil
}

func bodyStmts(d ast.Decl) []ast.Stmt {
	switch n := d.(type) {
	case *ast.FuncDecl:
		return n.Body
	case *ast.ImplDecl:
		var stmts []ast.Stmt
		for _, m := range n.Members {
			if fn, ok := m.(*ast.FuncDecl); ok {
				stmts = append(stmts, fn.Body...)
			}
		}
		return stmts
	}
	return nil
}

func buildBodyGraph(bodies []scopedDecl) *bodyGraph {
	definers := map[string][]int{}
	for i, scoped := range bodies {
		for _, name := range bodyDefinedNames(scoped.Decl) {
			definers[name] = append(definers[name], i)
		}
	}
	g := &bodyGraph{deps: make([][]int, len(bodies)), weight: make([]int, len(bodies))}
	for i, scoped := range bodies {
		seen := map[int]bool{i: true}
		note := func(name string) {
			for _, j := range definers[fillMayAdoptLastSegment(name)] {
				if !seen[j] {
					seen[j] = true
					g.deps[i] = append(g.deps[i], j)
				}
			}
		}
		walkStoreScanNodes(bodyStmts(scoped.Decl), func(node any) {
			g.weight[i]++
			switch n := node.(type) {
			case *ast.Ident:
				note(n.Name)
			case *ast.FieldExpr:
				note(n.Field)
			case ast.Expr:
				if name, _, ok := ast.PrepassCallShape(n); ok {
					note(name)
				}
			}
		})
		sort.Ints(g.deps[i])
	}
	return g
}

// sccs returns the strongly connected components in reverse topological order
// (every component after all components it depends on), members in source order.
func (g *bodyGraph) sccs() [][]int {
	n := len(g.deps)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var comps [][]int
	var st []int
	next := 0
	type frame struct{ v, edge int }
	for root := 0; root < n; root++ {
		if index[root] >= 0 {
			continue
		}
		call := []frame{{root, 0}}
		index[root], low[root] = next, next
		next++
		st = append(st, root)
		onStack[root] = true
		for len(call) > 0 {
			f := &call[len(call)-1]
			if f.edge < len(g.deps[f.v]) {
				w := g.deps[f.v][f.edge]
				f.edge++
				if index[w] < 0 {
					index[w], low[w] = next, next
					next++
					st = append(st, w)
					onStack[w] = true
					call = append(call, frame{w, 0})
				} else if onStack[w] && index[w] < low[f.v] {
					low[f.v] = index[w]
				}
				continue
			}
			v := f.v
			call = call[:len(call)-1]
			if len(call) > 0 && low[v] < low[call[len(call)-1].v] {
				low[call[len(call)-1].v] = low[v]
			}
			if low[v] == index[v] {
				var comp []int
				for {
					w := st[len(st)-1]
					st = st[:len(st)-1]
					onStack[w] = false
					comp = append(comp, w)
					if w == v {
						break
					}
				}
				sort.Ints(comp)
				comps = append(comps, comp)
			}
		}
	}
	return comps
}

func reportBodyGraph(bodies []scopedDecl) {
	if os.Getenv("ELISA_PARALLEL_GRAPH_REPORT") == "" {
		return
	}
	g := buildBodyGraph(bodies)
	comps := g.sccs()
	compOf := make([]int, len(bodies))
	for c, comp := range comps {
		for _, v := range comp {
			compOf[v] = c
		}
	}
	total, largest, largestW := 0, 0, 0
	finish := make([]int, len(comps)) // longest weighted path ending at each component
	critical := 0
	for c, comp := range comps {
		w, start := 0, 0
		for _, v := range comp {
			w += g.weight[v]
			for _, d := range g.deps[v] {
				if dc := compOf[d]; dc != c && finish[dc] > start {
					start = finish[dc]
				}
			}
		}
		finish[c] = start + w
		if finish[c] > critical {
			critical = finish[c]
		}
		total += w
		if len(comp) > largest {
			largest, largestW = len(comp), w
		}
	}
	fmt.Fprintf(os.Stderr, "[parallel-graph] %d bodies, %d units, largest SCC %d bodies (%.1f%% of weight), critical path %.1f%% of weight -> max speedup %.1fx\n",
		len(bodies), len(comps), largest, 100*float64(largestW)/float64(total), 100*float64(critical)/float64(total), float64(total)/float64(critical))
}
