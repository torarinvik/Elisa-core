package semantic

import (
	"sort"
	"strings"

	"elisacore/src/ast"
)

// recordStorageContainerAlias retains the backing provenance of a shallow darray
// descriptor copy or aggregate carrying darray fields. Growth keeps the container
// value usable; it invalidates only views into storage that may have moved.
func (a *Analyzer) recordStorageContainerAlias(sym *Symbol, value ast.Expr, valueType Type) {
	if sym == nil || !storageViewTypeCarriesDArray(valueType, make(map[*StructType]bool)) || a.currentStorageViewDeps == nil {
		return
	}
	sources := a.storageViewContainerAliasSourcesForValue(value, valueType)
	if len(sources) == 0 {
		return
	}
	dependency := a.currentStorageViewDeps[sym]
	for _, source := range sources {
		dependency.ContainerAliases = appendStorageViewSource(dependency.ContainerAliases, source)
	}
	if _, exists := a.currentStorageViewDeps[sym]; !exists {
		dependency.Valid = true
	}
	a.currentStorageViewDeps[sym] = dependency
}

func appendStorageViewSource(sources []string, source string) []string {
	if source == "" {
		return sources
	}
	for _, existing := range sources {
		if existing == source {
			return sources
		}
	}
	return append(sources, source)
}

func (a *Analyzer) recordStorageContainerAliasTarget(target ast.Expr, value ast.Expr, targetType Type, valueType Type) {
	ident, ok := stripOptimizationParens(target).(*ast.Ident)
	if ok && ident != nil && a.currentScope != nil {
		if sym, found := a.currentScope.Lookup(ident.Name); found {
			a.recordStorageContainerAlias(sym, value, valueType)
		}
		return
	}
	field, ok := stripOptimizationParens(target).(*ast.FieldExpr)
	if !ok || field == nil || !storageViewTypeCarriesDArray(targetType, make(map[*StructType]bool)) || a.currentScope == nil {
		return
	}
	root, fieldObjectIsAggregatePath := storageViewAggregateRoot(field.Object)
	if !fieldObjectIsAggregatePath {
		return
	}
	if sym, found := a.currentScope.Lookup(root); found {
		a.recordStorageContainerAlias(sym, value, valueType)
	}
}

func storageViewAggregateRoot(expr ast.Expr) (string, bool) {
	switch node := stripOptimizationParens(expr).(type) {
	case *ast.Ident:
		if node != nil && node.Name != "" {
			return node.Name, true
		}
	case *ast.FieldExpr:
		return storageViewAggregateRoot(node.Object)
	}
	return "", false
}

func storageViewTypeCarriesDArray(typ Type, seen map[*StructType]bool) bool {
	switch value := StripAggregateStateType(typ).(type) {
	case *DArrayType:
		return true
	case *RefType:
		return storageViewTypeCarriesDArray(value.Elem, seen)
	case *ArrayType:
		return storageViewTypeCarriesDArray(value.Elem, seen)
	case *OptionalType:
		return storageViewTypeCarriesDArray(value.Value, seen)
	case *ErrorUnionType:
		return storageViewTypeCarriesDArray(value.Value, seen)
	case *TupleType:
		for _, field := range value.Fields {
			if storageViewTypeCarriesDArray(field.Type, seen) {
				return true
			}
		}
	case *GenericInstanceType:
		if storageViewTypeCarriesDArray(value.Base, seen) {
			return true
		}
		for _, argument := range value.Args {
			if storageViewTypeCarriesDArray(argument, seen) {
				return true
			}
		}
	case *StructType:
		if value == nil || seen[value] {
			return false
		}
		seen[value] = true
		for _, field := range value.Fields {
			if storageViewTypeCarriesDArray(field.Type, seen) {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) storageViewContainerAliasSourcesForValue(expr ast.Expr, typ Type) []string {
	seen := make(map[string]bool)
	var sources []string
	add := func(source string) {
		if source != "" && !seen[source] {
			seen[source] = true
			sources = append(sources, source)
		}
	}
	var visit func(ast.Expr, Type)
	visit = func(current ast.Expr, currentType Type) {
		if current == nil || !storageViewTypeCarriesDArray(currentType, make(map[*StructType]bool)) {
			return
		}
		switch n := current.(type) {
		case *ast.ParenExpr:
			visit(n.Inner, currentType)
		case *ast.MoveExpr:
			visit(n.Operand, currentType)
		case *ast.CastExpr:
			visit(n.Operand, currentType)
		case *ast.TernaryExpr:
			visit(n.Value, currentType)
			visit(n.Alt, currentType)
		case *ast.StructLitExpr:
			for _, argument := range n.LoweredArgs() {
				visit(argument, a.exprTypes[argument])
			}
			for _, spread := range n.Spreads {
				visit(spread, a.exprTypes[spread])
			}
		case *ast.RecordUpdateExpr:
			visit(n.Base, a.exprTypes[n.Base])
			for _, argument := range n.LoweredArgs() {
				visit(argument, a.exprTypes[argument])
			}
		case *ast.TupleExpr:
			tuple, _ := StripAggregateStateType(currentType).(*TupleType)
			for index, element := range n.Elems {
				if tuple != nil && index < len(tuple.Fields) {
					visit(element, tuple.Fields[index].Type)
				}
			}
		case *ast.Ident:
			if a.currentScope != nil && a.currentStorageViewDeps != nil {
				if sym, ok := a.currentScope.Lookup(n.Name); ok {
					if dependency := a.currentStorageViewDeps[sym]; len(dependency.ContainerAliases) > 0 {
						for _, source := range dependency.ContainerAliases {
							add(source)
						}
						return
					}
				}
			}
			add(optimizationExprString(current))
		case *ast.CallExpr:
			for _, source := range storageViewContainerAliasSources(current) {
				add(source)
			}
		default:
			if _, isDArray := StripAggregateStateType(currentType).(*DArrayType); isDArray {
				for _, source := range storageViewContainerAliasSources(current) {
					add(source)
				}
			}
		}
	}
	visit(expr, typ)
	return sources
}

func storageViewContainerAliasSources(expr ast.Expr) []string {
	seen := make(map[string]bool)
	var sources []string
	var visit func(ast.Expr)
	visit = func(current ast.Expr) {
		if current == nil {
			return
		}
		switch n := current.(type) {
		case *ast.ParenExpr:
			visit(n.Inner)
		case *ast.MoveExpr:
			visit(n.Operand)
		case *ast.CastExpr:
			visit(n.Operand)
		case *ast.TernaryExpr:
			visit(n.Value)
			visit(n.Alt)
		case *ast.ListLitExpr:
			// A list literal owns fresh backing storage. Its element expressions can
			// carry borrows, but the new darray descriptor is not a shallow alias of
			// any element or of another syntactically identical literal.
			return
		case *ast.CallExpr:
			// Opaque helpers may return an input descriptor. Until a verified
			// return-alias summary exists, include every argument conservatively.
			for _, argument := range n.Args {
				visit(argument)
			}
			if key := optimizationExprString(n.Func); key != "" && !seen[key] {
				seen[key] = true
				sources = append(sources, key)
			}
		default:
			if key := optimizationExprString(current); key != "" && !seen[key] {
				seen[key] = true
				sources = append(sources, key)
			}
		}
	}
	visit(expr)
	return sources
}

func (a *Analyzer) expandStorageViewMutationAliases(sources map[string]bool) {
	if len(sources) == 0 || len(a.currentStorageViewDeps) == 0 {
		return
	}
	changed := true
	for changed {
		changed = false
		for sym, alias := range a.currentStorageViewDeps {
			if sym == nil || len(alias.ContainerAliases) == 0 {
				continue
			}
			aliasName := sym.Name
			if aliasName == "" {
				continue
			}
			aliasMutated := false
			for mutated := range sources {
				if storageViewSourcesOverlap(aliasName, mutated) {
					aliasMutated = true
					break
				}
			}
			if aliasMutated {
				for _, source := range alias.ContainerAliases {
					if source != "" && !sources[source] {
						sources[source] = true
						changed = true
					}
				}
			}
			for _, source := range alias.ContainerAliases {
				for mutated := range sources {
					if storageViewSourcesOverlap(source, mutated) && !sources[aliasName] {
						sources[aliasName] = true
						changed = true
						break
					}
				}
			}
		}
	}
}

func storageViewDependsOnAny(dependency storageViewDependencyState, sources map[string]bool) bool {
	for _, source := range dependency.Sources {
		for candidate := range sources {
			if storageViewSourcesOverlap(source, candidate) {
				return true
			}
		}
	}
	return false
}

func storageViewMatchedMutationSource(dependency storageViewDependencyState, sources map[string]bool) string {
	candidates := make([]string, 0, len(sources))
	for source := range sources {
		candidates = append(candidates, source)
	}
	sort.Strings(candidates)
	for _, source := range dependency.Sources {
		for _, candidate := range candidates {
			if storageViewSourcesOverlap(source, candidate) {
				return candidate
			}
		}
	}
	return ""
}

// A write to an aggregate root can relocate any of its darray fields. Without a
// field-sensitive ownership proof, sibling paths under the same root may alias.
func storageViewSourcesOverlap(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	if left == right || storageViewSourceRoot(left) == storageViewSourceRoot(right) {
		return true
	}
	return strings.HasPrefix(left, right+".") || strings.HasPrefix(left, right+"[") ||
		strings.HasPrefix(right, left+".") || strings.HasPrefix(right, left+"[")
}

func storageViewSourceRoot(source string) string {
	if separator := strings.IndexAny(source, ".["); separator >= 0 {
		return source[:separator]
	}
	return source
}

func mergeStorageViewDependencyStates(dst map[*Symbol]storageViewDependencyState, src map[*Symbol]storageViewDependencyState) map[*Symbol]storageViewDependencyState {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[*Symbol]storageViewDependencyState, len(src))
		for sym, dep := range src {
			dst[sym] = cloneStorageViewDependency(dep)
		}
		return dst
	}
	for sym, srcDep := range src {
		dstDep, ok := dst[sym]
		if !ok {
			dst[sym] = cloneStorageViewDependency(srcDep)
			continue
		}
		merged, mergedDependencies := mergeStorageViewDependencies(dstDep, srcDep)
		if !mergedDependencies {
			merged = storageViewDependencyState{Valid: true}
		}
		if dstDep.Valid && !srcDep.Valid {
			merged.InvalidatedBy = srcDep.InvalidatedBy
		}
		if !dstDep.Valid && merged.InvalidatedBy == "" {
			merged.InvalidatedBy = dstDep.InvalidatedBy
		}
		merged.Valid = dstDep.Valid && srcDep.Valid
		for _, alias := range dstDep.ContainerAliases {
			merged.ContainerAliases = appendStorageViewSource(merged.ContainerAliases, alias)
		}
		for _, alias := range srcDep.ContainerAliases {
			merged.ContainerAliases = appendStorageViewSource(merged.ContainerAliases, alias)
		}
		dst[sym] = merged
	}
	return dst
}

func cloneStorageViewDependency(dependency storageViewDependencyState) storageViewDependencyState {
	dependency.Sources = append([]string(nil), dependency.Sources...)
	dependency.ContainerAliases = append([]string(nil), dependency.ContainerAliases...)
	return dependency
}
