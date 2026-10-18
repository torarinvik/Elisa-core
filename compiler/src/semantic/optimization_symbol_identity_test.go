package semantic

import (
	"runtime"
	"testing"
)

func TestOptimizationSymbolIdentitySurvivesCollection(t *testing.T) {
	analyzer := &Analyzer{}
	seen := map[string]bool{}
	// Returned strings outlive their lexical scopes. Even if no other code
	// retains an old Symbol, a new symbol must never acquire its alias identity.
	for i := 0; i < 64; i++ {
		identity := func() string {
			symbol := &Symbol{Name: "value"}
			first := analyzer.optimizationSymbolIdentity(symbol)
			if first != analyzer.optimizationSymbolIdentity(symbol) {
				t.Fatal("one symbol changed identity")
			}
			return first
		}()
		if seen[identity] {
			t.Fatal("distinct symbols merged after collection:", identity)
		}
		seen[identity] = true
		runtime.GC()
	}
	if analyzer.optimizationSymbolIdentity(nil) != "" {
		t.Fatal("nil symbol acquired an alias identity")
	}
	runtime.KeepAlive(analyzer)
}
