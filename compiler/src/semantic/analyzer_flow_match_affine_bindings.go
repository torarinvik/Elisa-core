package semantic

import (
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// recordAffineMatchPatternBindings records the ownership source for binders introduced by an
// enum match arm.  Region/value provenance deliberately resolves a payload through the original
// constructor expression, but affine ownership is different: the arm binder owns the field in the
// matched value.  Keeping the source as a projection of the scrutinee lets `move binder` clear the
// same affine path that the aggregate tracker registered (for example, `job.thread`).
func (a *Analyzer) recordAffineMatchPatternBindings(pattern ast.MatchPattern, expected Type, valueExpr ast.Expr, scope *Scope) {
	if a == nil || pattern == nil || expected == nil || scope == nil {
		return
	}
	savedScope := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = savedScope }()
	a.recordAffineMatchPatternBindingsInScope(pattern, expected, valueExpr, scope)
}

// refineAffineEnumVariant drops the affine leaf obligations for sibling variants in
// the current arm's cloned flow state. An enum owns only the payload of its active
// tag; registering every variant's fields at the enum root is necessary before a
// match, but treating all of them as simultaneously live inside one refined arm
// falsely reports leaks for payloads that cannot exist on that path.
func (a *Analyzer) refineAffineEnumVariant(valueExpr ast.Expr, enumType *EnumType, pattern *ast.MatchVariantPattern) {
	if a == nil || valueExpr == nil || enumType == nil || pattern == nil || pattern.EnumName != enumType.Name {
		return
	}
	variant, ok := enumType.Variant(pattern.Variant)
	if !ok || variant == nil {
		return
	}
	scrutineeKey, ok := a.lookupAffineValueKey(valueExpr)
	if !ok || scrutineeKey.Root == nil {
		return
	}
	allPaths := a.protocolLiveLeafPaths(enumType, "", map[string]bool{})
	activePaths := a.affineEnumVariantLivePaths(enumType, variant)
	for path := range allPaths {
		if _, active := activePaths[path]; active {
			continue
		}
		key := affineValueKey{Root: scrutineeKey.Root, Path: joinAffinePath(scrutineeKey.Path, path)}
		state, tracked := a.currentAffineValues[key]
		if !tracked {
			continue
		}
		state.LiveProtocolType = nil
		state.LiveProtocolDescription = ""
		a.currentAffineValues[key] = state
	}
}

func (a *Analyzer) affineEnumVariantLivePaths(enumType *EnumType, variant *EnumVariant) map[string]Type {
	paths := map[string]Type{}
	if a == nil || enumType == nil || variant == nil {
		return paths
	}
	for _, field := range enumType.Common {
		if !a.containsTrackedProtocolCarrierValues(field.Type, map[string]bool{}) {
			continue
		}
		for childPath, liveType := range a.protocolLiveLeafPaths(field.Type, field.Name, map[string]bool{}) {
			paths[joinAffinePath("", childPath)] = liveType
		}
	}
	for i, payloadType := range variant.Payload {
		label := variant.PayloadLabel(i)
		if label == "" || !a.containsTrackedProtocolCarrierValues(payloadType, map[string]bool{}) {
			continue
		}
		for childPath, liveType := range a.protocolLiveLeafPaths(payloadType, label, map[string]bool{}) {
			paths[joinAffinePath("", childPath)] = liveType
		}
	}
	return paths
}

func (a *Analyzer) recordAffineMatchPatternBindingsInScope(pattern ast.MatchPattern, expected Type, valueExpr ast.Expr, scope *Scope) {
	if pattern == nil || expected == nil || scope == nil {
		return
	}
	switch p := pattern.(type) {
	case *ast.MatchWildcardPattern, *ast.MatchStringLiteralPattern, *ast.MatchLiteralPattern, *ast.MatchRangePattern:
		return
	case *ast.MatchBindPattern:
		if p.Name == "" || p.Name == "_" {
			return
		}
		sym, ok := scope.Lookup(p.Name)
		if !ok || sym == nil || !a.containsAffineHandleValues(expected, map[string]bool{}) || valueExpr == nil {
			return
		}
		if _, ok := a.lookupAffineValueKey(valueExpr); !ok {
			return
		}
		if a.currentValueBindings == nil {
			a.currentValueBindings = map[*Symbol]ast.Expr{}
		}
		a.currentValueBindings[sym] = valueExpr
		return
	case *ast.MatchOrPattern:
		// The analyzer uses the first alternative as the shared binding shape.
		if len(p.Options) != 0 {
			a.recordAffineMatchPatternBindingsInScope(p.Options[0], expected, valueExpr, scope)
		}
		return
	case *ast.MatchStructPattern:
		fields, orderedArgs, ok := a.resolveMatchStructPattern(p, expected)
		if !ok {
			return
		}
		for i, arg := range orderedArgs {
			if arg == nil || i >= len(fields) {
				continue
			}
			fieldExpr := affineMatchFieldExpr(valueExpr, fields[i].Name, arg.Position)
			a.recordAffineMatchPatternBindingsInScope(arg.Pattern, fields[i].Type, fieldExpr, scope)
		}
		return
	case *ast.MatchListPattern:
		elemType, ok := SequenceMatchElementType(expected)
		if !ok {
			return
		}
		for i, elem := range p.Elems {
			if _, isRest := elem.(*ast.MatchRestPattern); isRest {
				continue
			}
			indexExpr := affineMatchIndexExpr(valueExpr, i, elem.Pos())
			a.recordAffineMatchPatternBindingsInScope(elem, elemType, indexExpr, scope)
		}
		return
	case *ast.MatchVariantPattern:
		variantType, _, ok := resolveMatchableEnumType(expected)
		if !ok || variantType == nil {
			return
		}
		variant, ok := variantType.Variant(p.Variant)
		if !ok || variant == nil {
			return
		}
		orderedArgs := a.resolveMatchPatternArgs(p, variant, variantType.Name+"."+variant.Name, true)
		for i, arg := range orderedArgs {
			if arg == nil || i >= len(variant.Payload) {
				continue
			}
			label := variant.PayloadLabel(i)
			if label == "" {
				// Unlabelled payloads are not represented by a field projection in
				// the affine path table; leave them to the existing conservative
				// aggregate check rather than inventing a path.
				continue
			}
			fieldExpr := affineMatchFieldExpr(valueExpr, label, arg.Position)
			a.recordAffineMatchPatternBindingsInScope(arg.Pattern, variant.Payload[i], fieldExpr, scope)
		}
	}
}

func affineMatchFieldExpr(object ast.Expr, field string, pos lexer.Pos) ast.Expr {
	if object == nil || field == "" {
		return nil
	}
	return &ast.FieldExpr{Position: pos, Object: object, Field: field}
}

func affineMatchIndexExpr(object ast.Expr, index int, pos lexer.Pos) ast.Expr {
	if object == nil {
		return nil
	}
	return &ast.IndexExpr{Position: pos, Object: object, Index: &ast.IntLit{Position: pos, Value: strconv.Itoa(index)}}
}

func matchPatternHasNamedBinding(pattern ast.MatchPattern) bool {
	switch p := pattern.(type) {
	case *ast.MatchBindPattern:
		return p.Name != "" && p.Name != "_"
	case *ast.MatchTuplePattern:
		for _, elem := range p.Elems {
			if matchPatternHasNamedBinding(elem) {
				return true
			}
		}
	case *ast.MatchListPattern:
		for _, elem := range p.Elems {
			if matchPatternHasNamedBinding(elem) {
				return true
			}
		}
	case *ast.MatchOrPattern:
		for _, option := range p.Options {
			if matchPatternHasNamedBinding(option) {
				return true
			}
		}
	case *ast.MatchStructPattern:
		if p.As != "" {
			return true
		}
		for _, arg := range p.Args {
			if matchPatternHasNamedBinding(arg.Pattern) {
				return true
			}
		}
	case *ast.MatchVariantPattern:
		if p.As != "" {
			return true
		}
		for _, arg := range p.Args {
			if matchPatternHasNamedBinding(arg.Pattern) {
				return true
			}
		}
	case *ast.MatchRestPattern:
		return p.Name != "" && p.Name != "_"
	}
	return false
}

func (a *Analyzer) matchVariantPatternBindsAffinePayload(pattern *ast.MatchVariantPattern, variant *EnumVariant) bool {
	if a == nil || pattern == nil || variant == nil {
		return false
	}
	orderedArgs := pattern.ResolvedArgs
	if len(orderedArgs) != len(variant.Payload) {
		orderedArgs = a.resolveMatchPatternArgs(pattern, variant, pattern.EnumName+"."+variant.Name, true)
	}
	for i, arg := range orderedArgs {
		if arg == nil || i >= len(variant.Payload) || !a.containsAffineHandleValues(variant.Payload[i], map[string]bool{}) {
			continue
		}
		if matchPatternHasNamedBinding(arg.Pattern) {
			return true
		}
	}
	return false
}
