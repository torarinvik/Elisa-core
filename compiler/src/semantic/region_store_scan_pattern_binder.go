package semantic

import (
	"reflect"
	"strings"

	"elisacore/src/ast"
)

// storeScanPatternBinderType types a match-arm binder in a callee body that may not be analyzed
// yet: `match s: Stmt.Block(kind, clause, ...)` binds clause at the declared payload type of the
// Block variant of s's declared enum type. Only when the binder's name is written exactly once in
// the whole body as anything but a use (so no other pattern, loop, declaration or parameter can
// rebind it at another type), the arm is a direct variant pattern of the match subject, and the
// subject's own type is declared (a parameter, a typed local or a field chain of one).
func (a *Analyzer) storeScanPatternBinderType(decl *ast.FuncDecl, name string) (Type, bool) {
	decl = a.readDecl(decl)
	if decl == nil || name == "" || name == "_" {
		return nil, false
	}
	if a.storeScanBinderTypes == nil {
		a.storeScanBinderTypes = map[*ast.FuncDecl]map[string]storeScanBinderVerdict{}
	}
	cache := a.storeScanBinderTypes[decl]
	if cache == nil {
		cache = map[string]storeScanBinderVerdict{}
		a.storeScanBinderTypes[decl] = cache
	}
	if v, ok := cache[name]; ok {
		return v.t, v.ok
	}
	// Decline while computing: a binder whose subject is (transitively) itself is untyped.
	cache[name] = storeScanBinderVerdict{}
	t, ok := a.storeScanPatternBinderTypeUncached(decl, name)
	cache[name] = storeScanBinderVerdict{t, ok}
	return t, ok
}

type storeScanBinderVerdict struct {
	t  Type
	ok bool
}

func (a *Analyzer) storeScanPatternBinderTypeUncached(decl *ast.FuncDecl, name string) (Type, bool) {
	decl = a.readDecl(decl)
	for _, p := range decl.Params {
		if p.Name == name {
			return nil, false
		}
	}
	idx := a.storeScanIndex(decl)
	if idx.bindingWriteCount(decl.Body, name) != 1 {
		return nil, false
	}
	if len(idx.decls[name]) != 0 {
		return nil, false
	}
	for _, node := range idx.flat {
		match, ok := node.(*ast.MatchStmt)
		if !ok || match == nil {
			continue
		}
		for _, arm := range match.Arms {
			if t, found, ok := a.storeScanArmBinderType(decl, match, arm, name); found {
				return t, ok
			}
		}
	}
	return nil, false
}

// storeScanArmBinderType types name where arm binds it directly in a variant pattern of match's
// declared-type subject. found reports that arm binds name that way at all; ok that the type is
// known.
func (a *Analyzer) storeScanArmBinderType(decl *ast.FuncDecl, match *ast.MatchStmt, arm ast.MatchArm, name string) (Type, bool, bool) {
	enumName, variantName, args, resolved, ok := storeScanArmVariant(arm.Pattern)
	if !ok {
		return nil, false, false
	}
	for i, arg := range args {
		bind, isBind := arg.Pattern.(*ast.MatchBindPattern)
		if !isBind || bind == nil || bind.Name != name || bind.Binder != "" {
			continue
		}
		subject, ok := a.storeScanSubjectDeclType(decl, match.Value)
		if !ok {
			return nil, true, false
		}
		enum, ok := stripRefForBounds(subject).(*EnumType)
		if !ok || enum == nil {
			return nil, true, false
		}
		if enumName != "" && lastNameSegment(enumName) != lastNameSegment(enum.Name) {
			return nil, true, false
		}
		variant := enum.VariantMap[variantName]
		if variant == nil {
			return nil, true, false
		}
		slot := -1
		if len(resolved) > 0 {
			// The parser's payload-ordered view of the args (named args resolved).
			for j, r := range resolved {
				if r != nil && r.Pattern == arg.Pattern {
					slot = j
				}
			}
		} else if arg.Name == "" {
			slot = i
		} else {
			for j, payloadName := range variant.PayloadNames {
				if payloadName == arg.Name {
					slot = j
				}
			}
		}
		if slot < 0 || slot >= len(variant.Payload) || variant.Payload[slot] == nil || containsTypeParam(variant.Payload[slot]) {
			return nil, true, false
		}
		return variant.Payload[slot], true, true
	}
	return nil, false, false
}

// storeScanPatternBindersShareNot reports a name bound only by direct variant patterns of
// declared-type match subjects, every one at a type that shares no indirect storage, and by
// declarations of plain view or scalar types. Unlike storeScanPatternBinderType, the name may be
// bound in several places.
func (a *Analyzer) storeScanPatternBindersShareNot(decl *ast.FuncDecl, name string) bool {
	decl = a.readDecl(decl)
	if decl == nil || name == "" || name == "_" {
		return false
	}
	for _, p := range decl.Params {
		if p.Name == name {
			return false
		}
	}
	idx := a.storeScanIndex(decl)
	typed := 0
	// A declaration of the name counts when it declares a plain view or scalar type.
	for _, v := range idx.decls[name] {
		if v == nil || v.Type == nil || !(storeFlowViewTypeExpr(v.Type) || storeScanTypeExprIsPlainScalar(v.Type)) {
			return false
		}
		typed++
	}
	for _, node := range idx.flat {
		match, ok := node.(*ast.MatchStmt)
		if !ok || match == nil {
			continue
		}
		for _, arm := range match.Arms {
			t, found, ok := a.storeScanArmBinderType(decl, match, arm, name)
			if !found {
				continue
			}
			if !ok || t == nil || a.storeScanSharesCached(t) {
				return false
			}
			typed++
		}
	}
	// Every binding of the name is one of those arms.
	return typed > 0 && idx.bindingWriteCount(decl.Body, name) == typed
}

// storeScanSubjectDeclType: the declared type of a match subject -- a parameter, an explicitly
// typed local, or a field chain of either.
func (a *Analyzer) storeScanSubjectDeclType(decl *ast.FuncDecl, subject ast.Expr) (Type, bool) {
	decl = a.readDecl(decl)
	switch e := storeScanUnwrap(subject).(type) {
	case *ast.Ident:
		if e == nil {
			return nil, false
		}
		if t, ok := a.storeScanLocalLiteralType(decl, e.Name); ok {
			return t, true
		}
		if t, ok := a.storeScanPatternBinderType(decl, e.Name); ok {
			return t, true
		}
		// A loop binder's source may itself be a loop binder: bound the chain.
		if a.storeScanLoopDepth < 16 {
			a.storeScanLoopDepth++
			t, ok := a.storeScanLoopBinderElemType(decl, e)
			a.storeScanLoopDepth--
			if ok && t != nil {
				return t, true
			}
		}
		if a.storeScanBoundNames(decl)[e.Name] {
			return nil, false
		}
		sym := a.funcDeclSymbols[decl]
		if sym == nil {
			return nil, false
		}
		fnType, ok := sym.Type.(*FuncType)
		if !ok || fnType == nil || fnType.Variadic {
			return nil, false
		}
		for i, p := range decl.Params {
			if p.Name == e.Name && i < len(fnType.Params) {
				return fnType.Params[i], true
			}
		}
		return nil, false
	case *ast.FieldExpr:
		return a.storeScanParamFieldChainType(decl, e)
	}
	return nil, false
}

// storeScanArmVariant splits a direct variant arm pattern (`E.V(a, b)` in either spelling).
func storeScanArmVariant(pattern ast.MatchPattern) (enumName, variant string, args []ast.MatchPatternArg, resolved []*ast.MatchPatternArg, ok bool) {
	switch p := pattern.(type) {
	case *ast.MatchVariantPattern:
		if p == nil {
			return "", "", nil, nil, false
		}
		return p.EnumName, p.Variant, p.Args, p.ResolvedArgs, true
	case *ast.MatchStructPattern:
		if p == nil {
			return "", "", nil, nil, false
		}
		dot := strings.LastIndex(p.TypeName, ".")
		if dot < 0 {
			return "", "", nil, nil, false
		}
		return p.TypeName[:dot], p.TypeName[dot+1:], p.Args, nil, true
	}
	return "", "", nil, nil, false
}

func lastNameSegment(name string) string {
	if i := strings.LastIndexAny(name, ".:"); i >= 0 {
		return name[i+1:]
	}
	return name
}

// storeScanBindingWriteCounts counts all places in body where a name is written as a string
// anywhere other than a plain use (an identifier or a field selector): binders, declarations,
// loop variables, capture lists. Over-counting only makes the binder typer decline. The index
// computes this map once per function so asking about multiple binders does not rescan the body.
func storeScanBindingWriteCounts(body []ast.Stmt) map[string]int {
	counts := map[string]int{}
	seen := map[uintptr]bool{}
	var rec func(v reflect.Value, depth int)
	rec = func(v reflect.Value, depth int) {
		if !v.IsValid() || depth > 200 {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch v.Interface().(type) {
			case *ast.Ident, *ast.NamedType:
				return
			case *ast.FieldExpr:
				rec(reflect.ValueOf(v.Interface().(*ast.FieldExpr).Object), depth+1)
				return
			}
			if v.Kind() == reflect.Pointer {
				// A node reachable twice (ResolvedArgs aliases Args) is one write.
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
			}
			rec(v.Elem(), depth+1)
		case reflect.Struct:
			if v.Type() == storeScanPosType {
				return
			}
			for i := 0; i < v.NumField(); i++ {
				if !v.Type().Field(i).IsExported() {
					continue
				}
				rec(v.Field(i), depth+1)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i), depth+1)
			}
		case reflect.String:
			counts[v.String()]++
		}
	}
	for _, stmt := range body {
		rec(reflect.ValueOf(stmt), 0)
	}
	return counts
}
