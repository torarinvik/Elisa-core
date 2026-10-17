package semantic

import (
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

const (
	smtBoolInitializerMaxDepth  = 256
	smtBoolInitializerMaxNodes  = 8192
	smtBoolHypothesisMaxSymbols = 8192
)

type smtBoolLocalInitCacheEntry struct {
	bindings map[string]*Symbol
}

// smtBoolMacroContext exposes the bodies of only the Bool macros reachable from context. The SMT
// preamble suppresses macro definitions while computing demand, but immutable numeric-local equations
// still need to be sliced through those bodies just as they were through Bool equality hypotheses.
func smtBoolMacroContext(tr *smtTranslator, context string) string {
	if tr == nil || len(tr.boolDefs) == 0 || context == "" {
		return context
	}
	var b strings.Builder
	b.WriteString(context)
	pending := smtTokens(context)
	seen := make(map[string]bool, len(pending))
	for i := 0; i < len(pending); i++ {
		symbol := pending[i]
		if seen[symbol] {
			continue
		}
		seen[symbol] = true
		value, ok := tr.boolDefs[symbol]
		if !ok {
			continue
		}
		b.WriteString(value)
		b.WriteByte('\n')
		pending = append(pending, smtTokens(value)...)
	}
	return b.String()
}

// smtImmutableBoolLocalHypotheses adds defining equalities only for immutable
// Boolean locals that the query actually uses. Translation can discover another
// local through an initializer, so walk to a fixed point instead of depending on
// lexical or map iteration order.
func (a *Analyzer) smtImmutableBoolLocalHypotheses(tr *smtTranslator, context string) string {
	if a == nil || a.currentScope == nil || tr == nil || a.inClosedWorldProof || len(tr.boolLocalRefs) == 0 {
		return ""
	}
	reached := make(map[string]bool)
	for _, symbol := range smtTokens(context) {
		reached[symbol] = true
	}
	emitted := make(map[string]bool)
	type localBoolCandidate struct {
		name  string
		decl  *ast.VarDeclStmt
		sym   *Symbol
		scope *Scope
	}
	seen := make(map[string]bool)
	candidates := make(map[string]localBoolCandidate)
	definitions := make(map[string]string)
	dependencies := make(map[string]map[string]bool)
	currentParent := ""
	pending := make([]string, 0, len(tr.boolLocalRefs))
	candidateForSymbol := func(symbol string, known *Symbol) (localBoolCandidate, bool) {
		if !strings.HasPrefix(symbol, "b_") {
			return localBoolCandidate{}, false
		}
		name := strings.TrimPrefix(symbol, "b_")
		if name == "" || strings.ContainsAny(name, ".@") {
			return localBoolCandidate{}, false
		}
		var sym *Symbol
		var declScope *Scope
		for scope := a.currentScope; scope != nil; scope = scope.Parent {
			if visible, found := scope.Symbols[name]; found {
				if known != nil && visible != known {
					return localBoolCandidate{}, false
				}
				sym = visible
				declScope = scope
				break
			}
		}
		if sym == nil || sym.Kind != SymbolLocal || sym.Mutable || isDirectRefType(sym.Type) || !IsBoolType(sym.Type) {
			return localBoolCandidate{}, false
		}
		decl, ok := sym.Node.(*ast.VarDeclStmt)
		if !ok || decl == nil || decl.Value == nil {
			return localBoolCandidate{}, false
		}
		if declScope == nil || a.smtStaleLocalDefs[sym] {
			return localBoolCandidate{}, false
		}
		return localBoolCandidate{name: name, decl: decl, sym: sym, scope: declScope}, true
	}
	enqueue := func(symbol string) {
		if seen[symbol] || len(candidates) >= smtBoolHypothesisMaxSymbols {
			return
		}
		seen[symbol] = true
		if _, alreadyDefined := tr.boolDefs[symbol]; alreadyDefined {
			return
		}
		candidate, ok := candidateForSymbol(symbol, tr.boolLocalRefs[symbol])
		if !ok {
			return
		}
		candidates[symbol] = candidate
		pending = append(pending, symbol)
	}
	initial := make([]string, 0, len(tr.boolLocalRefs))
	for symbol, sym := range tr.boolLocalRefs {
		if !reached[symbol] || !strings.HasPrefix(symbol, "b_") {
			continue
		}
		seen[symbol] = true
		if _, alreadyDefined := tr.boolDefs[symbol]; alreadyDefined {
			continue
		}
		if candidate, ok := candidateForSymbol(symbol, sym); ok {
			initial = append(initial, symbol)
			candidates[symbol] = candidate
		}
	}
	sort.Strings(initial)
	if len(initial) > smtBoolHypothesisMaxSymbols {
		for _, symbol := range initial[smtBoolHypothesisMaxSymbols:] {
			delete(candidates, symbol)
		}
		initial = initial[:smtBoolHypothesisMaxSymbols]
	}
	pending = append(pending, initial...)
	previousObserver := tr.onBoolIdentifier
	tr.onBoolIdentifier = func(symbol string) {
		if currentParent != "" && currentParent != symbol {
			if dependencies[currentParent] == nil {
				dependencies[currentParent] = make(map[string]bool)
			}
			dependencies[currentParent][symbol] = true
		}
		enqueue(symbol)
		if previousObserver != nil {
			previousObserver(symbol)
		}
	}
	defer func() { tr.onBoolIdentifier = previousObserver }()
	walkedNodes := 0
	for len(pending) > 0 {
		current := pending
		pending = nil
		for _, symbol := range current {
			candidate := candidates[symbol]
			name := candidate.name
			// Source locals are bare identifiers. Dotted projections and @-prefixed
			// generated symbols belong to separate namespaces.
			if name == "" || emitted[name] {
				continue
			}
			cached, hasCached := a.smtBoolLocalInitCache[candidate.sym]
			if hasCached {
				if !a.smtBoolLocalDefinitionBindingsCurrent(candidate.sym, cached.bindings) {
					emitted[name] = true
					continue
				}
			} else {
				identifiers := make(map[string]bool)
				if !a.smtBoolInitializerStable(candidate.decl.Value, &walkedNodes, identifiers, 0) {
					emitted[name] = true
					continue
				}
				bindings, current := a.smtBoolLocalDefinitionBindings(candidate.scope, candidate.sym, identifiers)
				if !current {
					emitted[name] = true
					continue
				}
				if a.smtBoolLocalInitCache == nil {
					a.smtBoolLocalInitCache = make(map[*Symbol]smtBoolLocalInitCacheEntry)
				}
				a.smtBoolLocalInitCache[candidate.sym] = smtBoolLocalInitCacheEntry{bindings: bindings}
			}
			currentParent = symbol
			value, ok := tr.boolTerm(candidate.decl.Value, nil)
			currentParent = ""
			if !ok {
				emitted[name] = true
				continue
			}
			emitted[name] = true
			tr.boolDecls[symbol] = true
			definitions[symbol] = value
		}
		sort.Strings(pending)
	}
	if len(definitions) == 0 {
		return ""
	}
	keys := make([]string, 0, len(definitions))
	for symbol := range definitions {
		keys = append(keys, symbol)
	}
	sort.Strings(keys)
	state := make(map[string]uint8, len(definitions))
	cyclic := make(map[string]bool)
	stack := make([]string, 0, len(definitions))
	order := make([]string, 0, len(definitions))
	var visit func(string)
	visit = func(symbol string) {
		switch state[symbol] {
		case 2:
			return
		case 1:
			for i := len(stack) - 1; i >= 0; i-- {
				cyclic[stack[i]] = true
				if stack[i] == symbol {
					break
				}
			}
			return
		}
		state[symbol] = 1
		stack = append(stack, symbol)
		deps := make([]string, 0, len(dependencies[symbol]))
		for dep := range dependencies[symbol] {
			if _, localDefinition := definitions[dep]; localDefinition {
				deps = append(deps, dep)
			}
		}
		sort.Strings(deps)
		for _, dep := range deps {
			visit(dep)
		}
		stack = stack[:len(stack)-1]
		state[symbol] = 2
		order = append(order, symbol)
	}
	for _, symbol := range keys {
		visit(symbol)
	}
	var fallback strings.Builder
	for _, symbol := range order {
		if cyclic[symbol] {
			fallback.WriteString("(assert (= " + symbol + " " + definitions[symbol] + "))\n")
			continue
		}
		if tr.boolDefs == nil {
			tr.boolDefs = make(map[string]string)
		}
		tr.boolDefs[symbol] = definitions[symbol]
		tr.boolDefOrder = append(tr.boolDefOrder, symbol)
	}
	return fallback.String()
}

// smtBoolInitializerStable is intentionally narrower than boolTerm. It allows
// pure scalar expressions and immutable by-value record projections, while
// rejecting calls, indexing, reference roots, and mutable or reference-bearing
// records before replaying an initializer at a later proof point.
func (a *Analyzer) smtBoolInitializerStable(expr ast.Expr, walkedNodes *int, identifiers map[string]bool, depth int) bool {
	if expr == nil || a == nil {
		return false
	}
	if !a.smtBoolInitializerVisit(walkedNodes, depth) {
		return false
	}
	if t := a.exprTypes[expr]; isDirectRefType(t) {
		return false
	}
	switch n := expr.(type) {
	case *ast.BoolLit:
		return true
	case *ast.ParenExpr:
		return a.smtBoolInitializerStable(n.Inner, walkedNodes, identifiers, depth+1)
	case *ast.Ident:
		identifiers[n.Name] = true
		if _, ok := a.evalConstBoolExpr(n); ok {
			return true
		}
		return a.smtImmutableScalarIdent(n)
	case *ast.FieldExpr:
		if root, ok := rootIdentName(n); ok {
			identifiers[root] = true
		}
		return a.smtImmutableScalarProjection(n)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && n.Op == lexer.TOKEN_NOT && a.smtBoolInitializerStable(n.Operand, walkedNodes, identifiers, depth+1)
	case *ast.TernaryExpr:
		return n != nil && a.smtBoolInitializerStable(n.Cond, walkedNodes, identifiers, depth+1) && a.smtBoolInitializerStable(n.Value, walkedNodes, identifiers, depth+1) && a.smtBoolInitializerStable(n.Alt, walkedNodes, identifiers, depth+1)
	case *ast.BinaryExpr:
		if n == nil || n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_AND, lexer.TOKEN_OR:
			return a.smtBoolInitializerStable(n.Left, walkedNodes, identifiers, depth+1) && a.smtBoolInitializerStable(n.Right, walkedNodes, identifiers, depth+1)
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			if IsBoolType(a.exprTypes[n.Left]) && IsBoolType(a.exprTypes[n.Right]) {
				return a.smtBoolInitializerStable(n.Left, walkedNodes, identifiers, depth+1) && a.smtBoolInitializerStable(n.Right, walkedNodes, identifiers, depth+1)
			}
			return a.smtScalarInitializerStable(n.Left, walkedNodes, identifiers, depth+1) && a.smtScalarInitializerStable(n.Right, walkedNodes, identifiers, depth+1)
		case lexer.TOKEN_LT, lexer.TOKEN_LTEQ, lexer.TOKEN_GT, lexer.TOKEN_GTEQ:
			return a.smtScalarInitializerStable(n.Left, walkedNodes, identifiers, depth+1) && a.smtScalarInitializerStable(n.Right, walkedNodes, identifiers, depth+1)
		default:
			return false
		}
	default:
		return false
	}
}

func (a *Analyzer) smtScalarInitializerStable(expr ast.Expr, walkedNodes *int, identifiers map[string]bool, depth int) bool {
	if expr == nil || a == nil {
		return false
	}
	if !a.smtBoolInitializerVisit(walkedNodes, depth) {
		return false
	}
	if isDirectRefType(a.exprTypes[expr]) {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.CharLit:
		return true
	case *ast.ParenExpr:
		return a.smtScalarInitializerStable(n.Inner, walkedNodes, identifiers, depth+1)
	case *ast.Ident:
		identifiers[n.Name] = true
		return a.smtImmutableScalarIdent(n)
	case *ast.FieldExpr:
		if root, ok := rootIdentName(n); ok {
			identifiers[root] = true
		}
		return a.smtImmutableScalarProjection(n)
	case *ast.UnaryExpr:
		return n != nil && n.LoweredCall == nil && n.Op == lexer.TOKEN_MINUS && a.smtScalarInitializerStable(n.Operand, walkedNodes, identifiers, depth+1)
	case *ast.BinaryExpr:
		if n == nil || n.LoweredCall != nil {
			return false
		}
		switch n.Op {
		case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH, lexer.TOKEN_PERCENT,
			lexer.TOKEN_AMPERSAND, lexer.TOKEN_PIPE, lexer.TOKEN_CARET, lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			return a.smtScalarInitializerStable(n.Left, walkedNodes, identifiers, depth+1) && a.smtScalarInitializerStable(n.Right, walkedNodes, identifiers, depth+1)
		default:
			return false
		}
	default:
		return false
	}
}

// smtBoolLocalDefinitionBindings resolves dependencies once after the stable-expression visitor
// has collected every identifier. Later proof points can validate those bindings with one lookup
// per name instead of rescanning the initializer and looking up each name twice.
func (a *Analyzer) smtBoolLocalDefinitionBindings(declScope *Scope, sym *Symbol, names map[string]bool) (map[string]*Symbol, bool) {
	if a == nil || a.currentScope == nil || declScope == nil || sym == nil || sym.Node == nil {
		return nil, false
	}
	if a.smtStaleLocalDefs[sym] {
		return nil, false
	}
	declPos := sym.Node.Pos()
	bindings := make(map[string]*Symbol, len(names))
	for name := range names {
		if name == sym.Name {
			return nil, false
		}
		then, _ := declScope.Lookup(name)
		now, _ := a.currentScope.Lookup(name)
		if then != now {
			return nil, false
		}
		bindings[name] = then
		if then != nil && then.Node != nil && then.Kind == SymbolLocal {
			if p := then.Node.Pos(); p.File == declPos.File && p.Offset > declPos.Offset {
				return nil, false
			}
		}
		if then != nil && then.Kind == SymbolGlobal && then.Mutable {
			return nil, false
		}
	}
	return bindings, true
}

func (a *Analyzer) smtBoolLocalDefinitionBindingsCurrent(sym *Symbol, bindings map[string]*Symbol) bool {
	if a == nil || a.currentScope == nil || sym == nil || a.smtStaleLocalDefs[sym] {
		return false
	}
	for name, binding := range bindings {
		now, _ := a.currentScope.Lookup(name)
		if now != binding {
			return false
		}
	}
	return true
}

func (a *Analyzer) smtBoolInitializerVisit(walkedNodes *int, depth int) bool {
	if walkedNodes == nil || *walkedNodes >= smtBoolInitializerMaxNodes || depth >= smtBoolInitializerMaxDepth {
		return false
	}
	*walkedNodes++
	return true
}

func (a *Analyzer) smtImmutableScalarIdent(id *ast.Ident) bool {
	if a == nil || id == nil || a.currentScope == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(id.Name)
	if !found || sym == nil || sym.Mutable || isDirectRefType(sym.Type) || symbolRootMutable(sym) {
		return false
	}
	if IsBoolType(sym.Type) {
		return true
	}
	return IsNumericType(sym.Type) && !IsFloatType(sym.Type)
}

func (a *Analyzer) smtImmutableScalarProjection(expr *ast.FieldExpr) bool {
	if a == nil || expr == nil || isDirectRefType(a.exprTypes[expr]) {
		return false
	}
	fieldType := a.exprTypes[expr]
	if fieldType == nil {
		var ok bool
		fieldType, ok = a.fieldReadResolvedType(expr)
		if !ok {
			return false
		}
	}
	if isDirectRefType(fieldType) || (!IsBoolType(fieldType) && (!IsNumericType(fieldType) || IsFloatType(fieldType))) {
		return false
	}
	rootName, ok := rootIdentName(expr)
	if !ok || a.currentScope == nil {
		return false
	}
	sym, found := a.currentScope.Lookup(rootName)
	if !found || sym == nil || sym.Mutable || symbolRootMutable(sym) || !immutableProjectionRecord(sym.Type, 0) {
		return false
	}
	// Every intermediate projection must also be a by-value part of the record.
	depth := 0
	for cur := ast.Expr(expr); ; {
		field, ok := cur.(*ast.FieldExpr)
		if !ok || field == nil {
			return true
		}
		if depth >= smtBoolInitializerMaxDepth {
			return false
		}
		depth++
		t := a.exprTypes[field]
		if t == nil {
			t, _ = a.fieldReadResolvedType(field)
		}
		if isDirectRefType(t) {
			return false
		}
		cur = field.Object
	}
}

func isDirectRefType(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}
