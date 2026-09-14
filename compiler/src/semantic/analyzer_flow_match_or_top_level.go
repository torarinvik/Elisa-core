package semantic

import "elisacore/src/ast"

// analyzeTopLevelOrMatchPattern checks each alternative in an isolated arm scope, then exposes the
// bindings common to every alternative in the real arm scope. This keeps branch-specific refinements
// local while preserving the language rule that every alternative binds the same names and types.
func (a *Analyzer) analyzeTopLevelOrMatchPattern(
	pattern *ast.MatchOrPattern,
	expected Type,
	scope *Scope,
	analyzeOption func(ast.MatchPattern, *Scope) bool,
) bool {
	if pattern == nil || scope == nil || analyzeOption == nil {
		return false
	}
	if len(pattern.Options) == 0 {
		a.errorf(pattern.Pos(), "or-pattern requires at least one alternative")
		return false
	}
	if _, ok := a.collectOrPatternBindingTypes(pattern, expected); !ok {
		return false
	}
	var baseline map[string]*Symbol
	wildcard := false
	for _, option := range pattern.Options {
		branchScope := NewScope(scope.Parent)
		branchWildcard := analyzeOption(option, branchScope)
		wildcard = wildcard || branchWildcard
		if baseline == nil {
			baseline = branchScope.Symbols
			continue
		}
		if !samePatternBindingTypeMap(symbolsToPatternBindingTypes(baseline), symbolsToPatternBindingTypes(branchScope.Symbols)) {
			a.errorf(option.Pos(), "or-pattern alternatives must bind the same names with compatible types")
		}
	}
	for _, symbol := range baseline {
		if symbol != nil {
			a.defineLocalInScope(scope, symbol, pattern.Pos())
		}
	}
	return wildcard
}

func symbolsToPatternBindingTypes(symbols map[string]*Symbol) map[string]Type {
	bindings := make(map[string]Type, len(symbols))
	for name, symbol := range symbols {
		if symbol != nil {
			bindings[name] = symbol.Type
		}
	}
	return bindings
}
