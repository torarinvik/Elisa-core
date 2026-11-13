package semantic

import (
	"container/heap"
	"elisacore/src/ast"
	"elisacore/src/bodygraph"
	"elisacore/src/graphalgo"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
)

// bodyGraph is the conservative dependency graph of body declarations used for
// callee-first parallel analysis: body i depends on body j when i names anything
// j defines (a function, or a method of an impl), matched by last name segment.
// Missing an edge would let a caller read facts a callee's worker is writing, so
// the match is deliberately generous.
type bodyGraph struct {
	deps          [][]int // deps[i]: bodies i depends on
	weight        []int   // AST nodes per body
	functions     []*ast.FuncDecl
	functionDeps  [][]int // function-level graph, including methods grouped in one impl body
	funcRecursive []bool  // direct or function-value self-reference
}

func bodyFunctions(d ast.Decl) []*ast.FuncDecl        { return bodygraph.BodyFunctions(d) }
func bodyDefinedNames(d ast.Decl) []string            { return bodygraph.BodyDefinedNames(d) }
func bodyStmts(d ast.Decl) []ast.Stmt                 { return bodygraph.BodyStmts(d) }
func funcScanStmts(fn *ast.FuncDecl) []ast.Stmt       { return bodygraph.FuncScanStmts(fn) }
func funcDefaultArgStmts(fn *ast.FuncDecl) []ast.Stmt { return bodygraph.FuncDefaultArgStmts(fn) }

func buildBodyGraph(bodies []scopedDecl) *bodyGraph {
	declarations := make([]ast.Decl, len(bodies))
	for i, body := range bodies {
		declarations[i] = body.Decl
	}
	graph := bodygraph.Build(declarations, walkStoreScanNodes)
	return &bodyGraph{deps: graph.Deps, weight: graph.Weight, functions: graph.Functions, functionDeps: graph.FunctionDeps, funcRecursive: graph.FuncRecursive}
}

func (g *bodyGraph) cyclicFunctions() map[*ast.FuncDecl]bool {
	functionGraph := &bodyGraph{deps: g.functionDeps}
	cyclic := map[*ast.FuncDecl]bool{}
	for _, component := range functionGraph.sccs() {
		isCycle := len(component) > 1
		if len(component) == 1 {
			isCycle = g.funcRecursive[component[0]]
		}
		if isCycle {
			for _, index := range component {
				cyclic[g.functions[index]] = true
			}
		}
	}
	return cyclic
}

// sccs returns the strongly connected components in reverse topological order
// (every component after all components it depends on), members in source order.
func (g *bodyGraph) sccs() [][]int { return graphalgo.Components(g.deps, true) }

func reportBodyGraph(bodies []scopedDecl) {
	if os.Getenv("ELISA_PARALLEL_GRAPH_REPORT") == "" {
		return
	}
	g := buildBodyGraph(bodies)
	comps := g.sccs()
	total, largest, largestW, critical := graphalgo.ComponentMetrics(g.deps, g.weight, comps)
	for _, comp := range comps {
		if len(comp) >= 20 {
			var names []string
			for _, v := range comp {
				names = append(names, bodyDefinedNames(bodies[v].Decl)...)
			}
			fmt.Fprintf(os.Stderr, "[parallel-scc] %d: %v\n", len(comp), names)
		}
	}
	fmt.Fprintf(os.Stderr, "[parallel-graph] %d bodies, %d units, largest SCC %d bodies (%.1f%% of weight), critical path %.1f%% of weight -> max speedup %.1fx\n",
		len(bodies), len(comps), largest, 100*float64(largestW)/float64(total), 100*float64(critical)/float64(total), float64(total)/float64(critical))
}

// bodySchedule is a deterministic callee-first plan: units (call-graph SCCs) are
// list-scheduled by estimated weight, and each worker runs its units in
// dependency order, waiting for any foreign callee unit before starting.
type bodySchedule struct {
	units      [][]int // bodies per unit, dependency order
	unitOf     []int
	worker     []int   // per unit
	deps       [][]int // foreign-or-local units each unit depends on
	done       []chan struct{}
	exported   []map[int]reflect.Value // per unit: map path index -> map of decl entries
	funcs      [][]*ast.FuncDecl       // function declarations per unit
	cyclic     map[*ast.FuncDecl]bool  // declarations that need recursive summary seeding
	took       []time.Duration
	nodes      [][][]nodeFact // per unit, then per node-keyed map
	stmts      [][]ast.Stmt
	sccOf      []int
	sccUnits   map[int][]int
	sccLeft    map[int]int
	sccPublish map[int][][2]*FuncType
	rdeps      [][]int // units waiting on each unit
	priority   []int   // estimated remaining path weight through each unit
	mu         sync.Mutex
	cond       *sync.Cond
	pending    []int // indegree per unit
	ready      readyHeap
	left       int
}

type readyHeap struct {
	units    []int
	priority []int
}

func (h readyHeap) Len() int { return len(h.units) }
func (h readyHeap) Less(i, j int) bool {
	a, b := h.units[i], h.units[j]
	if h.priority[a] != h.priority[b] {
		return h.priority[a] > h.priority[b]
	}
	return a < b
}
func (h readyHeap) Swap(i, j int) { h.units[i], h.units[j] = h.units[j], h.units[i] }
func (h *readyHeap) Push(x any)   { h.units = append(h.units, x.(int)) }
func (h *readyHeap) Pop() any {
	n := len(h.units) - 1
	x := h.units[n]
	h.units = h.units[:n]
	return x
}

// claim blocks until a unit is ready and hands it to worker w; -1 when all are taken.
func (s *bodySchedule) claim(w int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.ready.Len() == 0 && s.left > 0 {
		s.cond.Wait()
	}
	if s.ready.Len() == 0 {
		return -1
	}
	u := heap.Pop(&s.ready).(int)
	s.left--
	s.worker[u] = w
	if s.left == 0 {
		s.cond.Broadcast()
	}
	return u
}

// finish releases the units that were waiting only on u.
func (s *bodySchedule) finish(u int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.rdeps[u] {
		if s.pending[r]--; s.pending[r] == 0 {
			heap.Push(&s.ready, r)
		}
	}
	s.cond.Broadcast()
}

// depClosure lists every unit u transitively depends on, dependencies first, in an
// order fixed by the graph alone. A unit for which have reports true is left out with
// its own dependencies: the caller already holds them (it ran or imported that unit
// after importing the unit's closure), so each worker walks each unit once.
func (s *bodySchedule) depClosure(u int, have func(int) bool) []int {
	return graphalgo.DependencyClosure(s.deps, u, have)
}

func planBodySchedule(bodies []scopedDecl, workers int) *bodySchedule {
	g := buildBodyGraph(bodies)
	components := g.sccs()
	// Members of a recursive cycle cannot see each other's finished facts in any
	// order, so a large cycle is dealt in chunks that run side by side; each chunk
	// still waits for the cycle's outside callees, and callers wait for every chunk.
	units, unitOf, sccOf, sccUnits, unitDeps, weights := graphalgo.WorkUnits(g.deps, g.weight, components, parallelSCCChunkSize())
	s := &bodySchedule{units: units, unitOf: unitOf, worker: make([]int, len(units)),
		deps: unitDeps, done: make([]chan struct{}, len(units)),
		exported: make([]map[int]reflect.Value, len(units)), funcs: make([][]*ast.FuncDecl, len(units)), took: make([]time.Duration, len(units)), nodes: make([][][]nodeFact, len(units)), stmts: make([][]ast.Stmt, len(units))}
	s.cyclic = g.cyclicFunctions()
	s.sccOf = sccOf
	s.sccUnits = sccUnits
	s.sccLeft = map[int]int{}
	for c, us := range s.sccUnits {
		s.sccLeft[c] = len(us)
	}
	s.sccPublish = map[int][][2]*FuncType{}
	for u, unit := range units {
		s.done[u] = make(chan struct{})
		for _, b := range unit {
			s.stmts[u] = append(s.stmts[u], bodyStmts(bodies[b].Decl)...)
			switch n := bodies[b].Decl.(type) {
			case *ast.FuncDecl:
				s.funcs[u] = append(s.funcs[u], n)
			case *ast.ImplDecl:
				for _, m := range n.Members {
					if fn, ok := m.(*ast.FuncDecl); ok {
						s.funcs[u] = append(s.funcs[u], fn)
					}
				}
			}
		}
	}
	s.rdeps, s.pending, s.priority = graphalgo.ScheduleDependencies(s.deps, weights, make([]int, len(units)))
	s.cond = sync.NewCond(&s.mu)
	s.ready.priority = s.priority
	s.left = len(s.units)
	for u := range s.units {
		if s.pending[u] == 0 {
			heap.Push(&s.ready, u)
		}
	}
	return s
}
