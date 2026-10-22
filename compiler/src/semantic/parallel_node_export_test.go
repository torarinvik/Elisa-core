package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"sync"
	"testing"
)

func legacyExportNodeEntries(a *Analyzer, stmts []ast.Stmt) []reflect.Value {
	paths := nodeKeyedMapPaths()
	maps := make([]reflect.Value, len(paths))
	out := make([]reflect.Value, len(paths))
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
		for pi, v := range maps {
			if !v.IsValid() {
				continue
			}
			kt := v.Type().Key()
			if kt.Kind() == reflect.Interface {
				if !nv.Type().Implements(kt) {
					continue
				}
			} else if nv.Type() != kt {
				continue
			}
			key := nv.Convert(kt)
			if val := v.MapIndex(key); val.IsValid() {
				if !out[pi].IsValid() {
					out[pi] = reflect.MakeMap(v.Type())
				}
				out[pi].SetMapIndex(key, deepCloneValue(val))
			}
		}
	})
	return out
}

func nodeExportFixture(count int) (*Analyzer, []ast.Stmt) {
	a := &Analyzer{}
	var stmts []ast.Stmt
	for i := 0; i < count; i++ {
		call := &ast.CallExpr{Func: &ast.Ident{Name: "callee"}, Args: []ast.Expr{&ast.Ident{Name: "value"}}}
		stmts = append(stmts, &ast.AssignStmt{Target: &ast.Ident{Name: "out"}, Value: call})
	}
	root := settable(reflect.ValueOf(a).Elem())
	for _, path := range nodeKeyedMapPaths() {
		v := root
		for _, i := range path.path {
			v = settable(v.Field(i))
		}
		v.Set(reflect.MakeMap(v.Type()))
		walkStoreScanNodes(stmts, func(node any) {
			n := reflect.ValueOf(node)
			k := v.Type().Key()
			if (k.Kind() == reflect.Interface && n.Type().Implements(k)) || n.Type() == k {
				v.SetMapIndex(n.Convert(k), reflect.Zero(v.Type().Elem()))
			}
		})
	}
	return a, stmts
}

func TestParallelNodeExportMatchesAllMapDispatch(t *testing.T) {
	a, stmts := nodeExportFixture(4)
	want := legacyExportNodeEntries(a, stmts)
	check := func() {
		got := exportNodeEntries(a, stmts)
		for i := range want {
			if got[i].IsValid() != want[i].IsValid() {
				t.Errorf("map %d validity differs", i)
				continue
			}
			if got[i].IsValid() && !reflect.DeepEqual(got[i].Interface(), want[i].Interface()) {
				t.Errorf("map %d entries differ", i)
			}
		}
	}
	check()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); check() }()
	}
	wg.Wait()
	// The dispatch table must not retain any worker's map or entries.
	for i, v := range exportNodeEntries(&Analyzer{}, stmts) {
		if v.IsValid() {
			t.Errorf("empty worker inherited map %d", i)
		}
	}
}

func BenchmarkParallelNodeExport(b *testing.B) {
	a, stmts := nodeExportFixture(64)
	for _, tc := range []struct {
		name string
		run  func(*Analyzer, []ast.Stmt) []reflect.Value
	}{{"all-maps", legacyExportNodeEntries}, {"compatible-maps", exportNodeEntries}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = tc.run(a, stmts)
			}
		})
	}
}
