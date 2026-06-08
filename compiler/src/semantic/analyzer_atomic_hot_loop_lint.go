package semantic

import "elisacore/src/ast"

var atomicHotLoopNames = map[string]bool{
	"compare_exchange":               true,
	"fetch_add":                      true,
	"fetch_sub":                      true,
	"fetch_or":                       true,
	"fetch_and":                      true,
	"fetch_xor":                      true,
	"atomic_compare_exchange_acqrel": true,
	"atomic_fetch_add_acqrel":        true,
	"atomic_fetch_sub_acqrel":        true,
}

// Atomic RMW/CAS in a loop is often the "lock-free" version of lock churn: every
// iteration can serialize on one cache line. It is sometimes correct and intentional, but
// under -Wperf shipped code should use reductions, sharding, batching, or per-worker locals
// unless the hot atomic is a deliberate protocol boundary.
func (a *Analyzer) checkAtomicHotLoops(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.forEachFirstLoopBody(fn.Body, a.flagAtomicHotLoop)
}

func (a *Analyzer) flagAtomicHotLoop(loopBody []ast.Stmt) {
	a.walkStaticStmts(loopBody, func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil || isRuntimeStdPermissionInternal(call.Pos().File) {
			return false
		}
		name := callIdentName(call)
		if name == "" {
			name = callSpecializedIdentName(call)
		}
		if !atomicHotLoopNames[name] {
			return false
		}
		a.perfLint(call.Pos(), "`%s` performs an atomic read-modify-write/compare-exchange on every iteration of this loop. Atomic hot loops often serialize on one cache line and scale poorly; prefer per-worker locals plus reduction, sharded counters, batching, or a coarser synchronization protocol. If this is an intentional low-level protocol boundary, isolate it in a named wrapper", name)
		return false
	})
}
