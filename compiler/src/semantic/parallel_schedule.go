package semantic

import (
	"container/heap"
	"fmt"
	"os"
	"reflect"
	"sort"
	"sync"
	"time"

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
	units    [][]int // bodies per unit, dependency order
	unitOf   []int
	worker   []int   // per unit
	deps     [][]int // foreign-or-local units each unit depends on
	done     []chan struct{}
	exported []map[int]reflect.Value // per unit: map path index -> map of decl entries
	funcs    [][]*ast.FuncDecl       // function declarations per unit
	took     []time.Duration
	rdeps    [][]int // units waiting on each unit
	priority []int   // estimated remaining path weight through each unit
	mu       sync.Mutex
	cond     *sync.Cond
	pending  []int // indegree per unit
	ready    readyHeap
	left     int
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

func planBodySchedule(bodies []scopedDecl, workers int) *bodySchedule {
	g := buildBodyGraph(bodies)
	// Members of a recursive cycle cannot see each other's finished facts in any
	// order, so a large cycle is dealt in chunks that run side by side; each chunk
	// still waits for the cycle's outside callees, and callers wait for every chunk.
	const chunk = 8
	var units [][]int
	var sccOf []int
	for c, comp := range g.sccs() {
		for from := 0; from < len(comp); from += chunk {
			units = append(units, comp[from:min(from+chunk, len(comp))])
			sccOf = append(sccOf, c)
		}
	}
	s := &bodySchedule{units: units, unitOf: make([]int, len(bodies)), worker: make([]int, len(units)),
		deps: make([][]int, len(units)), done: make([]chan struct{}, len(units)),
		exported: make([]map[int]reflect.Value, len(units)), funcs: make([][]*ast.FuncDecl, len(units)), took: make([]time.Duration, len(units))}
	for u, unit := range units {
		for _, b := range unit {
			s.unitOf[b] = u
		}
	}
	weights := make([]int, len(units))
	for u, unit := range units {
		s.done[u] = make(chan struct{})
		w := 0
		seen := map[int]bool{u: true}
		for _, b := range unit {
			w += g.weight[b] + 50
			for _, d := range g.deps[b] {
				if du := s.unitOf[d]; !seen[du] && sccOf[du] != sccOf[u] {
					seen[du] = true
					s.deps[u] = append(s.deps[u], du)
				}
			}
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
		sort.Ints(s.deps[u])
		weights[u] = w
	}
	s.rdeps = make([][]int, len(s.units))
	s.pending = make([]int, len(s.units))
	s.priority = make([]int, len(s.units))
	for u := range s.units {
		s.pending[u] = len(s.deps[u])
		for _, d := range s.deps[u] {
			s.rdeps[d] = append(s.rdeps[d], u)
		}
	}
	for u := len(s.units) - 1; u >= 0; u-- {
		best := 0
		for _, r := range s.rdeps[u] {
			best = max(best, s.priority[r])
		}
		s.priority[u] = best + weights[u]
	}
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

// exportUnit records the owner's final declaration-keyed facts for a finished
// unit and publishes its function types; closing done orders both before any
// importer's reads.
func (s *bodySchedule) exportUnit(a *Analyzer, u int) {
	out := map[int]reflect.Value{}
	root := settable(reflect.ValueOf(a).Elem())
	for pi, path := range funcDeclKeyedMapPaths() {
		v := root
		for _, i := range path {
			v = settable(v.Field(i))
		}
		if v.IsNil() || holdsLazyCache(v.Type().Elem()) {
			continue
		}
		var m reflect.Value
		for _, fn := range s.funcs[u] {
			if val := v.MapIndex(reflect.ValueOf(fn)); val.IsValid() {
				if !m.IsValid() {
					m = reflect.MakeMap(v.Type())
				}
				m.SetMapIndex(reflect.ValueOf(fn), deepCloneValue(val))
			}
		}
		if m.IsValid() {
			out[pi] = m
		}
	}
	s.exported[u] = out
	if p := a.parallel; p != nil {
		for _, fn := range s.funcs[u] {
			if sym, ok := a.symbolForFuncDecl(fn); ok && sym != nil {
				if shared, _ := sym.Type.(*FuncType); shared != nil {
					if private := p.funcTypes[shared]; private != nil && p.funcTypeOwned[shared] {
						*shared = *private
					}
				}
			}
		}
	}
	close(s.done[u])
}

// importUnit waits for a foreign unit and copies its exported facts in.
func (s *bodySchedule) importUnit(a *Analyzer, u int) time.Duration {
	tw := time.Now()
	<-s.done[u]
	waited := time.Since(tw)
	root := settable(reflect.ValueOf(a).Elem())
	paths := funcDeclKeyedMapPaths()
	for pi, m := range s.exported[u] {
		v := root
		for _, i := range paths[pi] {
			v = settable(v.Field(i))
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		iter := m.MapRange()
		for iter.Next() {
			v.SetMapIndex(iter.Key(), deepCloneValue(iter.Value()))
		}
	}
	p := a.parallel
	if p.imported == nil {
		p.imported = map[*ast.FuncDecl]bool{}
	}
	for _, fn := range s.funcs[u] {
		p.imported[fn] = true
		if shadow := p.readShadows[fn]; shadow != nil {
			delete(p.readShadows, fn)
			delete(p.original, shadow)
		}
	}
	return waited
}

// holdsLazyCache reports a map value that points at memoizing state (a struct with
// maps): its owner keeps filling it, so sharing it would race. Importers rebuild.
func holdsLazyCache(t reflect.Type) bool {
	return t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct && typeHoldsMapInline(t.Elem())
}

// reportMeasured prints the critical path under measured unit times.
func (s *bodySchedule) reportMeasured() {
	if os.Getenv("ELISA_PARALLEL_GRAPH_REPORT") == "" {
		return
	}
	finish := make([]time.Duration, len(s.units))
	via := make([]int, len(s.units))
	var total, crit time.Duration
	end := 0
	for u := range s.units {
		var start time.Duration
		via[u] = -1
		for _, d := range s.deps[u] {
			if finish[d] > start {
				start, via[u] = finish[d], d
			}
		}
		finish[u] = start + s.took[u]
		total += s.took[u]
		if finish[u] > crit {
			crit, end = finish[u], u
		}
	}
	fmt.Fprintf(os.Stderr, "[parallel-measured] work %v, critical path %v -> max speedup %.1fx\n", total.Round(time.Millisecond), crit.Round(time.Millisecond), float64(total)/float64(crit))
	for u := end; u >= 0; u = via[u] {
		if s.took[u] > 100*time.Millisecond {
			fmt.Fprintf(os.Stderr, "[parallel-measured]   unit %d (%d fns, first %s): %v\n", u, len(s.funcs[u]), firstName(s.funcs[u]), s.took[u].Round(time.Millisecond))
		}
	}
}

func firstName(fns []*ast.FuncDecl) string {
	if len(fns) == 0 {
		return "-"
	}
	return fns[0].Name
}
