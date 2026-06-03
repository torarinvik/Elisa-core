package semantic

import (
	"fmt"
	"strings"
	"unicode"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func exactTypeKey(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T:%s", t, t.String())
}

func castHookKey(source Type, target Type) castHookSignature {
	return castHookSignature{Source: exactTypeKey(source), Target: exactTypeKey(target)}
}

func initHookParamKey(fnType *FuncType) string {
	if fnType == nil {
		return "<invalid>"
	}
	explicitParamCount := funcTypeExplicitParamCount(fnType)
	parts := make([]string, 0, explicitParamCount)
	for i := 0; i < explicitParamCount; i++ {
		name := ""
		if i < len(fnType.ExplicitParamNames) {
			name = fnType.ExplicitParamNames[i]
		}
		paramType := "<nil>"
		if i < len(fnType.Params) && fnType.Params[i] != nil {
			paramType = exactTypeKey(fnType.Params[i])
		}
		hasDefault := i < len(fnType.ExplicitParamHasDefault) && fnType.ExplicitParamHasDefault[i]
		parts = append(parts, fmt.Sprintf("%s=%s=%t", name, paramType, hasDefault))
	}
	return strings.Join(parts, "|")
}

func sanitizeHookSymbolFragment(value string) string {
	if value == "" {
		return "anon"
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "anon"
	}
	return out
}

func castHookSymbolName(qualifiedName string, fnType *FuncType, pos lexer.Pos) string {
	source := "invalid_src"
	target := "invalid_dst"
	if fnType != nil && len(fnType.Params) == 1 && fnType.Params[0] != nil {
		source = sanitizeHookSymbolFragment(fnType.Params[0].String())
	}
	if fnType != nil && fnType.Return != nil {
		target = sanitizeHookSymbolFragment(fnType.Return.String())
	}
	base := sanitizeHookSymbolFragment(qualifiedName)
	return fmt.Sprintf("%s__%s__to__%s__L%d_C%d", base, source, target, pos.Line, pos.Col)
}

func initHookSymbolName(qualifiedName string, fnType *FuncType, pos lexer.Pos) string {
	target := "invalid_ctor"
	if fnType != nil && fnType.Return != nil {
		target = sanitizeHookSymbolFragment(fnType.Return.String())
	}
	base := sanitizeHookSymbolFragment(qualifiedName)
	return fmt.Sprintf("%s__for__%s__L%d_C%d", base, target, pos.Line, pos.Col)
}

func (a *Analyzer) constructorDeclInitHookName(namespace string, decl *ast.FuncDecl, fnType *FuncType) (string, bool) {
	if a == nil || decl == nil || fnType == nil || decl.Name == "" || decl.Name == "__cast__" || decl.Name == "__init__" {
		return "", false
	}
	if fnType.Return == nil || isVoidType(fnType.Return) {
		return "", false
	}
	targetType, _, ok := a.lookupVisibleType(decl.Name)
	if !ok {
		return "", false
	}
	targetBase, _, _, ok := structLiteralBaseAndBindings(targetType)
	if !ok || targetBase == nil {
		return "", false
	}
	returnBase, _, _, ok := structLiteralBaseAndBindings(fnType.Return)
	if !ok || returnBase == nil {
		return "", false
	}
	if targetBase.Name != returnBase.Name {
		return "", false
	}
	return "__init__", true
}

func (a *Analyzer) registerCastHook(namespace string, decl *ast.FuncDecl, fnType *FuncType, sym *Symbol) {
	if a == nil || decl == nil || fnType == nil || sym == nil {
		return
	}
	qualifiedName := joinQualifiedName(namespace, decl.Name)
	if len(fnType.ImplicitParamNames) != 0 {
		a.errorf(decl.Pos(), "__cast__ hook %q must not declare implicit parameters in v1", qualifiedName)
		return
	}
	if funcTypeExplicitParamCount(fnType) != 1 {
		a.errorf(decl.Pos(), "__cast__ hook %q must take exactly 1 parameter, got %d", qualifiedName, funcTypeExplicitParamCount(fnType))
		return
	}
	if len(fnType.TypeParams) != 0 || len(fnType.RefStorageParams) != 0 || len(fnType.RefStateParams) != 0 || len(fnType.RegionParams) != 0 {
		a.errorf(decl.Pos(), "__cast__ hook %q must not be generic in v1", qualifiedName)
		return
	}
	if fnType.Variadic {
		a.errorf(decl.Pos(), "__cast__ hook %q must not be variadic", qualifiedName)
		return
	}
	if fnType.Return == nil || isVoidType(fnType.Return) {
		a.errorf(decl.Pos(), "__cast__ hook %q must return a concrete non-void type", qualifiedName)
		return
	}
	key := castHookKey(fnType.Params[0], fnType.Return)
	hooks := a.castHooksByName[qualifiedName]
	if hooks == nil {
		hooks = map[castHookSignature]*Symbol{}
		a.castHooksByName[qualifiedName] = hooks
	}
	if existing, ok := hooks[key]; ok {
		a.errorf(decl.Pos(), "duplicate __cast__ hook for %s -> %s (already defined as %q)", fnType.Params[0], fnType.Return, existing.Name)
		return
	}
	hooks[key] = sym
}

func (a *Analyzer) registerInitHook(namespace string, decl *ast.FuncDecl, lookupName string, fnType *FuncType, sym *Symbol) {
	if a == nil || decl == nil || fnType == nil || sym == nil {
		return
	}
	declName := joinQualifiedName(namespace, decl.Name)
	qualifiedName := joinQualifiedName(namespace, lookupName)
	if len(fnType.ImplicitParamNames) != 0 {
		a.errorf(decl.Pos(), "__init__ hook %q must not declare implicit parameters in v1", declName)
		return
	}
	if len(fnType.TypeParams) != 0 || len(fnType.RefStorageParams) != 0 || len(fnType.RefStateParams) != 0 || len(fnType.RegionParams) != 0 || len(fnType.PermissionParams) != 0 || len(fnType.GenericParams) != 0 {
		a.errorf(decl.Pos(), "__init__ hook %q must not be generic in v1", declName)
		return
	}
	if fnType.Variadic {
		a.errorf(decl.Pos(), "__init__ hook %q must not be variadic", declName)
		return
	}
	if fnType.Return == nil || isVoidType(fnType.Return) {
		a.errorf(decl.Pos(), "__init__ hook %q must return a concrete struct type", declName)
		return
	}
	if base, _, _, ok := structLiteralBaseAndBindings(fnType.Return); !ok || base == nil {
		a.errorf(decl.Pos(), "__init__ hook %q must return a concrete struct type, got %s", declName, fnType.Return)
		return
	}
	key := initHookSignature{Target: exactTypeKey(fnType.Return), Params: initHookParamKey(fnType)}
	hooks := a.initHooksByName[qualifiedName]
	if hooks == nil {
		hooks = map[initHookSignature]*Symbol{}
		a.initHooksByName[qualifiedName] = hooks
	}
	if existing, ok := hooks[key]; ok {
		a.errorf(decl.Pos(), "duplicate __init__ hook for %s (already defined as %q)", fnType.Return, existing.Name)
		return
	}
	hooks[key] = sym
}

func (a *Analyzer) lookupVisibleCastHook(source Type, target Type) (*Symbol, bool) {
	if a == nil {
		return nil, false
	}
	key := castHookKey(source, target)
	for _, candidate := range a.visibleNameCandidates("__cast__") {
		hooks := a.castHooksByName[candidate]
		if hooks == nil {
			continue
		}
		if sym, ok := hooks[key]; ok {
			return sym, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupVisibleInitHooks(target Type) []*Symbol {
	if a == nil || target == nil {
		return nil
	}
	key := exactTypeKey(target)
	var hooks []*Symbol
	for _, candidate := range a.visibleNameCandidates("__init__") {
		registered := a.initHooksByName[candidate]
		if len(registered) == 0 {
			continue
		}
		for signature, sym := range registered {
			if signature.Target == key && sym != nil {
				hooks = append(hooks, sym)
			}
		}
	}
	return hooks
}

func (a *Analyzer) symbolForFuncDecl(fn *ast.FuncDecl) (*Symbol, bool) {
	if a == nil || fn == nil {
		return nil, false
	}
	if sym, ok := a.funcDeclSymbols[fn]; ok && sym != nil {
		return sym, true
	}
	if sym, _, ok := a.lookupVisibleGlobal(fn.Name); ok {
		return sym, true
	}
	return nil, false
}
