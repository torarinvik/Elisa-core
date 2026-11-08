package semantic

import "elisacore/src/ast"

// cloneStoreScanIndexForSyntax reuses syntax-derived scan facts for a private
// specialization AST. The string/name tables are immutable, while pointer-bearing
// slices and maps are rebound through the syntax copier's source-to-private map.
// A missing mapping declines reuse and lets storeScanIndex do its ordinary walk.
func cloneStoreScanIndexForSyntax(source *storeScanDeclIndex, nodes map[ast.Node]ast.Node) *storeScanDeclIndex {
	if source == nil {
		return nil
	}
	flat, ok := remapStoreScanNodeSlice(nodes, source.flat)
	if !ok {
		return nil
	}
	decls, ok := remapStoreScanNodeSlices(nodes, source.decls)
	if !ok {
		return nil
	}
	iterBinders, ok := remapStoreScanNodeSlices(nodes, source.iterBinders)
	if !ok {
		return nil
	}
	returns, ok := remapStoreScanNodeSlice(nodes, source.returns)
	if !ok {
		return nil
	}
	bound := make(map[ast.Stmt][]string, len(source.bound))
	for stmt, names := range source.bound {
		private, found := remapStoreScanNode(nodes, stmt)
		if !found {
			return nil
		}
		bound[private] = names
	}

	index := &storeScanDeclIndex{
		decls:          decls,
		otherBound:     source.otherBound,
		parameterNames: source.parameterNames,
		flat:           flat,
		returns:        returns,
		locals:         source.locals,
		iterBinders:    iterBinders,
		nonIterBinder:  source.nonIterBinder,
		bound:          bound,
	}
	if source.payloadNodesReady {
		index.payloadNodes, ok = remapStoreScanNodeSlice(nodes, source.payloadNodes)
		if !ok {
			return nil
		}
		index.payloadNodesReady = true
	}
	if source.bindingNodesReady {
		index.bindingNodes, ok = remapStoreScanNodeSlice(nodes, source.bindingNodes)
		if !ok {
			return nil
		}
		index.bindingNodesReady = true
	}
	if source.bindingWritesReady {
		index.bindingWrites = source.bindingWrites
		index.bindingWritesReady = true
	}
	if source.nameNodes != nil {
		index.nameNodes = source.nameNodes
		index.nameNodesOnce.Do(func() {})
	}
	if index.exprTokens, ok = remapStoreScanExprTokens(nodes, source.exprTokens); !ok {
		return nil
	}
	if index.headerTokens, ok = remapStoreScanHeaderTokens(nodes, source.headerTokens); !ok {
		return nil
	}
	if index.bundleCache, ok = remapStoreScanExprBools(nodes, source.bundleCache); !ok {
		return nil
	}
	if index.identCounts, ok = remapStoreScanIdentCounts(nodes, source.identCounts); !ok {
		return nil
	}
	index.bodyIdentCounts = source.bodyIdentCounts
	return index
}

func remapStoreScanNode[T any](nodes map[ast.Node]ast.Node, source T) (T, bool) {
	var zero T
	syntaxNode, ok := any(source).(ast.Node)
	if !ok {
		return zero, false
	}
	if nodes == nil {
		return source, true
	}
	private, ok := nodes[syntaxNode]
	if !ok {
		return zero, false
	}
	value, ok := private.(T)
	if !ok {
		return zero, false
	}
	return value, true
}

func remapStoreScanNodeSlice[T any](nodes map[ast.Node]ast.Node, source []T) ([]T, bool) {
	if source == nil {
		return nil, true
	}
	private := make([]T, len(source))
	for i, node := range source {
		value, ok := remapStoreScanNode(nodes, node)
		if !ok {
			return nil, false
		}
		private[i] = value
	}
	return private, true
}

func remapStoreScanNodeSlices[K comparable, T any](nodes map[ast.Node]ast.Node, source map[K][]T) (map[K][]T, bool) {
	if source == nil {
		return nil, true
	}
	private := make(map[K][]T, len(source))
	for key, list := range source {
		value, ok := remapStoreScanNodeSlice(nodes, list)
		if !ok {
			return nil, false
		}
		private[key] = value
	}
	return private, true
}

func remapStoreScanExprTokens(nodes map[ast.Node]ast.Node, source map[ast.Expr][]string) (map[ast.Expr][]string, bool) {
	if source == nil {
		return nil, true
	}
	private := make(map[ast.Expr][]string, len(source))
	for expr, tokens := range source {
		key, ok := remapStoreScanNode(nodes, expr)
		if !ok {
			return nil, false
		}
		private[key] = tokens
	}
	return private, true
}

func remapStoreScanHeaderTokens(nodes map[ast.Node]ast.Node, source map[ast.Stmt][]string) (map[ast.Stmt][]string, bool) {
	if source == nil {
		return nil, true
	}
	private := make(map[ast.Stmt][]string, len(source))
	for stmt, tokens := range source {
		key, ok := remapStoreScanNode(nodes, stmt)
		if !ok {
			return nil, false
		}
		private[key] = tokens
	}
	return private, true
}

func remapStoreScanExprBools(nodes map[ast.Node]ast.Node, source map[ast.Expr]bool) (map[ast.Expr]bool, bool) {
	if source == nil {
		return nil, true
	}
	private := make(map[ast.Expr]bool, len(source))
	for expr, value := range source {
		key, ok := remapStoreScanNode(nodes, expr)
		if !ok {
			return nil, false
		}
		private[key] = value
	}
	return private, true
}

func remapStoreScanIdentCounts(nodes map[ast.Node]ast.Node, source map[ast.Expr]map[string]int) (map[ast.Expr]map[string]int, bool) {
	if source == nil {
		return nil, true
	}
	private := make(map[ast.Expr]map[string]int, len(source))
	for expr, counts := range source {
		key, ok := remapStoreScanNode(nodes, expr)
		if !ok {
			return nil, false
		}
		private[key] = counts
	}
	return private, true
}
