package semantic

import (
	"fmt"
	"runtime"
	"testing"

	"elisacore/src/ast"
)

// Row scopes are deliberately not made sparse: both alternatives perform every
// live FuncType lookup and row comparison/save/restore. Setup is excluded. The
// reused inventory's one-per-update construction has a separate benchmark.
func BenchmarkPermissionSummaryCallerInventory(b *testing.B) {
	for _, size := range []int{128, 2048} {
		declarations := make([]scopedDecl, size)
		a := &Analyzer{funcDeclSymbols: make(map[*ast.FuncDecl]*Symbol, size)}
		p := &retainedPermissions{initial: map[*FuncType]permissionRowSnapshot{}, declared: map[*FuncType]permissionRowSnapshot{}, bodyEdited: map[*ast.FuncDecl]bool{}, summaryReanalyzed: map[*ast.FuncDecl]bool{}}
		for i := range declarations {
			fn := &ast.FuncDecl{Name: fmt.Sprintf("bench%d", i)}
			ft := &FuncType{Name: fn.Name}
			declarations[i] = scopedDecl{Decl: fn}
			a.funcDeclSymbols[fn] = &Symbol{Type: ft}
			inventoryTestRow("live").restore(ft)
			p.initial[ft] = inventoryTestRow("initial")
			p.declared[ft] = inventoryTestRow("declared")
		}
		inventory := newRetainedPermissionInventory(declarations)
		for _, cached := range []bool{false, true} {
			label := "old"
			if cached {
				label = "reused"
			}
			b.Run(fmt.Sprintf("%d/%s", size, label), func(b *testing.B) {
				p.summaryReanalyzed = map[*ast.FuncDecl]bool{}
				for _, symbol := range a.funcDeclSymbols {
					inventoryTestRow("live").restore(symbol.Type.(*FuncType))
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					fn := declarations[(i*17)%size].Decl.(*ast.FuncDecl)
					p.summaryReanalyzed[fn] = true
					if cached {
						p.withInitialRowsForSummaryCallerUsingInventory(a, declarations, inventory, fn, func() {})
					} else {
						oldPermissionSummaryCaller(p, a, declarations, fn, func() {})
					}
				}
				b.StopTimer()
				runtime.KeepAlive(a)
				runtime.KeepAlive(p)
				runtime.KeepAlive(inventory)
			})
		}
		b.Run(fmt.Sprintf("%d/build_once_per_update", size), func(b *testing.B) {
			b.ReportAllocs()
			var sink *retainedPermissionInventory
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sink = newRetainedPermissionInventory(declarations)
			}
			b.StopTimer()
			runtime.KeepAlive(sink)
		})
	}
}
