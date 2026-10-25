package semantic

import (
	"fmt"
	"runtime"
	"testing"

	"elisacore/src/ast"
)

// Compare exact prior and buffered scopes; every live row is still scanned.
// The many-callers cases measure one invocation after its first scope, with
// first-frame allocation separately exposed by once_per_invocation.
func BenchmarkPermissionSnapshotFrames(b *testing.B) {
	for _, size := range []int{128, 2048} {
		declarations := make([]scopedDecl, size)
		a := &Analyzer{funcDeclSymbols: make(map[*ast.FuncDecl]*Symbol, size)}
		p := &retainedPermissions{initial: map[*FuncType]permissionRowSnapshot{}, declared: map[*FuncType]permissionRowSnapshot{}, bodyEdited: map[*ast.FuncDecl]bool{}, summaryReanalyzed: map[*ast.FuncDecl]bool{}}
		rows := map[*FuncType]permissionRowSnapshot{}
		for i := range declarations {
			fn := &ast.FuncDecl{Name: fmt.Sprintf("bench%d", i)}
			ft := &FuncType{Name: fn.Name}
			declarations[i] = scopedDecl{Decl: fn}
			a.funcDeclSymbols[fn] = &Symbol{Type: ft}
			rows[ft] = inventoryTestRow("live")
			p.initial[ft] = inventoryTestRow("initial")
			p.declared[ft] = inventoryTestRow("declared")
		}
		target := declarations[len(declarations)-1].Decl.(*ast.FuncDecl)
		for _, nested := range []bool{false, true} {
			depth := "single"
			if nested {
				depth = "nested"
			}
			for _, buffered := range []bool{false, true} {
				label := "prior"
				if buffered {
					label = "buffered"
				}
				b.Run(fmt.Sprintf("%d/%s/%s", size, depth, label), func(b *testing.B) {
					inventory := newRetainedPermissionInventory(declarations)
					reset := func() {
						for ft, row := range rows {
							row.restore(ft)
						}
						p.summaryReanalyzed = map[*ast.FuncDecl]bool{target: true}
					}
					call := func() {
						body := func() {
							if buffered {
								p.withInitialRowsForSummaryCallerUsingInventory(a, declarations, inventory, target, func() {})
							} else {
								priorPermissionSnapshotCaller(p, a, declarations, inventory, target, func() {})
							}
						}
						if !nested {
							body()
							return
						}
						if buffered {
							p.withInitialRowsUsingInventory(a, declarations, inventory, body)
						} else {
							priorPermissionSnapshotOuter(p, a, declarations, inventory, body)
						}
					}
					reset()
					call()
					reset() // warm depth capacity, not mutable permission state
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						call()
					}
					b.StopTimer()
					runtime.KeepAlive(inventory)
					runtime.KeepAlive(p)
					runtime.KeepAlive(a)
				})
			}
		}
		b.Run(fmt.Sprintf("%d/once_per_invocation", size), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				inventory := newRetainedPermissionInventory(declarations)
				p.withInitialRowsForSummaryCallerUsingInventory(a, declarations, inventory, target, func() {})
			}
			b.StopTimer()
			runtime.KeepAlive(a)
			runtime.KeepAlive(p)
		})
	}
}
