//go:build cgo

package backend

import (
	"maps"
	"slices"
	"sync"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/semantic"
)

// One build's generators read the same idle semantic result. These indexes
// contain no LLVM handles and are immutable after construction. They never
// survive the build or authorize reuse of IR/native products.
type sharedCodegenLookups struct {
	result         *semantic.Result
	symbolsByNode  map[ast.Node]*semantic.Symbol
	namedCount     int
	symbolCount    int
	proofCount     int
	namedOnce      sync.Once
	globalOnce     sync.Once
	proofOnce      sync.Once
	namedSuffixes  map[string][]string
	globalSuffixes map[string][]string
	proofSpans     map[lexer.Pos]bool
}

func codegenSymbolsByNode(result *semantic.Result) map[ast.Node]*semantic.Symbol {
	byNode := make(map[ast.Node]*semantic.Symbol, len(result.GlobalScope.Symbols))
	// Preserve the original tie-break when several names refer to one node.
	for _, name := range slices.Sorted(maps.Keys(result.GlobalScope.Symbols)) {
		sym := result.GlobalScope.Symbols[name]
		if sym != nil && sym.Node != nil {
			byNode[sym.Node] = sym
		}
	}
	return byNode
}

func newSharedCodegenLookups(result *semantic.Result) *sharedCodegenLookups {
	return &sharedCodegenLookups{
		result: result, symbolsByNode: codegenSymbolsByNode(result),
		namedCount: len(result.NamedTypes), symbolCount: len(result.GlobalScope.Symbols),
		proofCount: len(result.IndexBoundsProven),
	}
}

func (shared *sharedCodegenLookups) indexProofSpans() map[lexer.Pos]bool {
	shared.proofOnce.Do(func() {
		shared.proofSpans = make(map[lexer.Pos]bool, shared.proofCount)
		for expr, proven := range shared.result.IndexBoundsProven {
			if expr != nil && proven {
				shared.proofSpans[expr.Position] = true
			}
		}
	})
	return shared.proofSpans
}
