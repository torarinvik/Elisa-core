package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"reflect"
	"sync"
	"time"
)

// exportUnit records the owner's final declaration-keyed facts for a finished
// unit and publishes its function types; closing done orders both before any
// importer's reads.
func (s *bodySchedule) exportUnit(a *Analyzer, u int) {
	// A unit with no dependents cannot be reached by an import. Its worker
	// still owns its facts for the final merge and publishes its function types.
	if len(s.rdeps[u]) != 0 {
		out := map[int]reflect.Value{}
		root := settable(reflect.ValueOf(a).Elem())
		for pi, path := range exportDeclMapPaths() {
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
		s.nodes[u] = exportNodeEntries(a, s.stmts[u])
	}
	var owned [][2]*FuncType
	if p := a.parallel; p != nil {
		for _, fn := range s.funcs[u] {
			if sym, ok := a.symbolForFuncDecl(fn); ok && sym != nil {
				if shared, _ := sym.Type.(*FuncType); shared != nil {
					if private := p.funcTypes[shared]; private != nil && p.funcTypeOwned[shared] {
						owned = append(owned, [2]*FuncType{shared, private})
					}
				}
			}
		}
	}
	// Sibling chunks of a cycle run side by side and read each other's shared
	// types, so a cycle publishes once, when its last chunk ends.
	s.mu.Lock()
	c := s.sccOf[u]
	s.sccPublish[c] = append(s.sccPublish[c], owned...)
	s.sccLeft[c]--
	if s.sccLeft[c] == 0 {
		for _, pair := range s.sccPublish[c] {
			publishChangedFields(pair[0], pair[1])
		}
		delete(s.sccPublish, c)
	}
	s.mu.Unlock()
	close(s.done[u])
}

// importUnit waits for a foreign unit and copies its exported facts in.
func (s *bodySchedule) importUnit(a *Analyzer, u int) time.Duration {
	tw := time.Now()
	<-s.done[u]
	waited := time.Since(tw)
	root := settable(reflect.ValueOf(a).Elem())
	paths := exportDeclMapPaths()
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
	importNodeEntries(a, s.nodes[u])
	p := a.parallel
	if p.imported == nil {
		p.imported = map[*ast.FuncDecl]bool{}
	}
	for _, fn := range s.funcs[u] {
		// Imports happen after the scheduler has waited for the dependency's
		// entire SCC, including its final FuncType publication. A private copy
		// obtained by an earlier quiet scan must not mask those finished facts.
		a.invalidateImportedFuncType(fn)
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

// nodeKeyedMap is an Analyzer map keyed by AST nodes other than declarations:
// expression types, name resolutions, call facts. A finished unit exports the
// entries of its own bodies' nodes, which callers read when they unfold a callee.
type nodeKeyedMap struct {
	path []int
	key  reflect.Type
}

var nodeKeyedMapPaths = sync.OnceValue(func() []nodeKeyedMap {
	var out []nodeKeyedMap
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			ft := t.Field(i).Type
			path := append(append([]int(nil), prefix...), i)
			switch ft.Kind() {
			case reflect.Map:
				k := ft.Key()
				if k == funcDeclPtrType || !k.Implements(astNodeType) || holdsLazyCache(ft.Elem()) {
					continue
				}
				out = append(out, nodeKeyedMap{path: path, key: k})
			case reflect.Struct:
				if typeHoldsMapInline(ft) {
					walk(ft, path)
				}
			}
		}
	}
	walk(reflect.TypeOf(Analyzer{}), nil)
	return out
})

// A node can be a key in several maps of the same key type. Group those
// columns so export boxes each interface key once per node.
type nodeKeyedMapGroup struct {
	key     reflect.Type
	columns []int
}

var nodeKeyedMapDispatch sync.Map // reflect.Type -> []nodeKeyedMapGroup (immutable)

func nodeKeyedMapGroups(t reflect.Type) []nodeKeyedMapGroup {
	if cached, ok := nodeKeyedMapDispatch.Load(t); ok {
		return cached.([]nodeKeyedMapGroup)
	}
	var groups []nodeKeyedMapGroup
	groupByKey := map[reflect.Type]int{}
	for i, path := range nodeKeyedMapPaths() {
		if t != path.key && !(path.key.Kind() == reflect.Interface && t.Implements(path.key)) {
			continue
		}
		g, ok := groupByKey[path.key]
		if !ok {
			g = len(groups)
			groupByKey[path.key] = g
			groups = append(groups, nodeKeyedMapGroup{key: path.key})
		}
		groups[g].columns = append(groups[g].columns, i)
	}
	actual, _ := nodeKeyedMapDispatch.LoadOrStore(t, groups)
	return actual.([]nodeKeyedMapGroup)
}

type nodeFact struct {
	key, value reflect.Value
}

// exportNodeEntries copies the node-keyed entries of a unit's bodies. Imports
// consume each column in order, so the published copy needs no hash table.
func exportNodeEntries(a *Analyzer, stmts []ast.Stmt) [][]nodeFact {
	paths := nodeKeyedMapPaths()
	maps := make([]reflect.Value, len(paths))
	out := make([][]nodeFact, len(paths))
	root := settable(reflect.ValueOf(a).Elem())
	for pi, m := range paths {
		v := root
		for _, i := range m.path {
			v = settable(v.Field(i))
		}
		if !v.IsNil() && v.Len() > 0 {
			maps[pi] = v
		}
	}
	walkStoreScanNodes(stmts, func(node any) {
		nv := reflect.ValueOf(node)
		for _, group := range nodeKeyedMapGroups(nv.Type()) {
			var key reflect.Value
			for _, pi := range group.columns {
				v := maps[pi]
				if !v.IsValid() {
					continue
				}
				if !key.IsValid() {
					key = nv
					if nv.Type() != group.key {
						key = nv.Convert(group.key)
					}
				}
				if val := v.MapIndex(key); val.IsValid() {
					out[pi] = append(out[pi], nodeFact{key, deepCloneValue(val)})
				}
			}
		}
	})
	return out
}

func importNodeEntries(a *Analyzer, entries [][]nodeFact) {
	paths := nodeKeyedMapPaths()
	root := settable(reflect.ValueOf(a).Elem())
	for pi, facts := range entries {
		if len(facts) == 0 {
			continue
		}
		v := root
		for _, i := range paths[pi].path {
			v = settable(v.Field(i))
		}
		if v.IsNil() {
			v.Set(reflect.MakeMapWithSize(v.Type(), len(facts)))
			// The exporter visits each syntax node once, so a newly allocated
			// column has no existing entries and its keys are distinct.
			for _, fact := range facts {
				v.SetMapIndex(fact.key, deepCloneValue(fact.value))
			}
			continue
		}
		for _, fact := range facts {
			if !v.MapIndex(fact.key).IsValid() {
				v.SetMapIndex(fact.key, deepCloneValue(fact.value))
			}
		}
	}
}

// exportDeclMapPaths lists every declaration-keyed map, including those whose
// values refer to declarations: exporting copies entries under the same key, so
// nothing is rekeyed.
var exportDeclMapPaths = sync.OnceValue(func() [][]int {
	var paths [][]int
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := 0; i < t.NumField(); i++ {
			ft := t.Field(i).Type
			path := append(append([]int(nil), prefix...), i)
			switch ft.Kind() {
			case reflect.Map:
				if ft.Key() == funcDeclPtrType && !holdsLazyCache(ft.Elem()) && !parallelBodyWorkerLocalOnlyMap(t.Field(i).Name) {
					paths = append(paths, path)
				}
			case reflect.Struct:
				if typeHoldsMapInline(ft) {
					walk(ft, path)
				}
			}
		}
	}
	walk(reflect.TypeOf(Analyzer{}), nil)
	return paths
})

// publishChangedFields writes only the fields a worker's analysis changed: other
// workers may read the rest of a shared function type at any time.
func publishChangedFields(shared, private *FuncType) {
	dst := settable(reflect.ValueOf(shared).Elem())
	src := reflect.ValueOf(private).Elem()
	var snapshot reflect.Value
	for i := 0; i < dst.NumField(); i++ {
		d, s := settable(dst.Field(i)), settable(src.Field(i))
		if !reflect.DeepEqual(d.Interface(), s.Interface()) {
			// Defer copying until a field needs publication. A single snapshot
			// preserves sharing across changed fields and redirects root cycles.
			if !snapshot.IsValid() {
				snapshot = snapshotFuncTypeForDestination(private, shared)
			}
			// Publication transfers a snapshot, not ownership of the worker's
			// mutable slice/map backing. Later local inference must not modify
			// memory another worker reads through the shared declaration.
			d.Set(settable(snapshot.Field(i)))
		}
	}
}
