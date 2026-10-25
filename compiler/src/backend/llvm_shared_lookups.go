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
	program        *sharedCodegenLookups // canonical immutable declaration/type indexes
	importWeights  sync.Map              // current FuncDecl -> bounded AST weight
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
	// typeKeys memoizes noteTypeKeyFor by type pointer. Semantic types are frozen
	// while one result is lowered, and the canonical key is a pure function of the
	// type, so every partition of this result shares one encoding per type.
	typeKeys sync.Map // semantic.Type -> typeMemoKey
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
		proofCount: result.IndexProofSpanCount(),
	}
}

func (shared *sharedCodegenLookups) indexProofSpans() map[lexer.Pos]bool {
	shared.proofOnce.Do(func() {
		shared.proofSpans = make(map[lexer.Pos]bool, shared.proofCount)
		shared.result.RangeIndexProofSpanAuthority(func(expr *ast.IndexExpr, proven bool) bool {
			if expr != nil && proven {
				shared.proofSpans[expr.Position] = true
			}
			return true
		})
	})
	return shared.proofSpans
}

func (shared *sharedCodegenLookups) programLookups() *sharedCodegenLookups {
	for shared != nil && shared.program != nil {
		shared = shared.program
	}
	return shared
}
