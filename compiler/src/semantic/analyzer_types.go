package semantic

import (
	"fmt"
	"strings"
	"unicode"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func (a *Analyzer) defineGlobal(sym *Symbol, pos lexer.Pos) {
	if existing, ok := a.globalScope.Define(sym); !ok {
		a.errorf(pos, "duplicate declaration %q (already defined as %s)", existing.Name, existing.Kind)
	}
}

func (a *Analyzer) defineLocal(sym *Symbol, pos lexer.Pos) {
	if a.currentScope == nil {
		return
	}
	if existing, ok := a.currentScope.Define(sym); !ok {
		a.errorf(pos, "duplicate local %q (already defined as %s)", existing.Name, existing.Kind)
	}
}

func (a *Analyzer) funcTypeFromDecl(name string, typeParams []string, params []ast.ParamDecl, ret ast.TypeExpr, variadic bool) *FuncType {
	ptypes := make([]Type, 0, len(params))
	retType := a.namedTypes["void"]
	shapeParams := a.collectImplicitShapeParams(params, ret)
	a.withTypeParams(typeParams, nil, func() {
		a.withShapeParams(shapeParams, func() {
			for _, p := range params {
				ptypes = append(ptypes, a.resolveType(p.Type))
			}
			if ret != nil {
				retType = a.resolveType(ret)
			}
		})
	})
	return &FuncType{
		Name:                   name,
		TypeParams:             append([]string(nil), typeParams...),
		ShapeParams:            shapeParams,
		FreshReturnShapeParams: knownFreshReturnShapeParams(name, retType),
		Params:                 ptypes,
		Return:                 retType,
		Variadic:               variadic,
	}
}

func (a *Analyzer) resolveType(expr ast.TypeExpr) Type {
	switch n := expr.(type) {
	case *ast.NamedType:
		if t, ok := a.lookupTypeParam(n.Name); ok {
			return t
		}
		if t, ok := a.namedTypes[n.Name]; ok {
			return t
		}
		a.errorf(n.Pos(), "unknown type %q", n.Name)
		return invalidType
	case *ast.ErrorSetExpr:
		return a.resolveErrorSetExpr(n)
	case *ast.ErrorUnionTypeExpr:
		valueType := a.resolveType(n.Value)
		errorType := a.resolveType(n.Errors)
		errSet, ok := errorType.(*ErrorSetType)
		if !ok {
			a.errorf(n.Pos(), "error union expects an error set on the right-hand side, got %s", errorType.String())
			return invalidType
		}
		return &ErrorUnionType{Value: valueType, Errors: errSet}
	case *ast.RefType:
		return &RefType{Elem: a.resolveType(n.Elem), State: RefState(n.State), Storage: RefStorage(n.Storage), ExplicitStorage: n.Explicit}
	case *ast.ArrayType:
		return a.resolveArrayType(n)
	case *ast.BuiltinTypeExpr:
		return a.resolveBuiltinSurfaceType(n)
	case *ast.MutableType:
		return a.resolveType(n.Elem)
	case *ast.TailType:
		return &RefType{Elem: a.resolveType(n.Elem), State: RefStateNonNull, Storage: RefStorageAny}
	case *ast.GenericType:
		if shaped, ok := a.resolveDynamicShapeType(n); ok {
			return shaped
		}
		if arrayExpr, ok := a.genericTypeAsArrayType(n); ok {
			return a.resolveArrayType(arrayExpr)
		}
		args := make([]Type, 0, len(n.Args))
		for _, arg := range n.Args {
			args = append(args, a.resolveType(arg))
		}
		base, ok := a.namedTypes[n.Name]
		if !ok {
			a.errorf(n.Pos(), "unknown type %q", n.Name)
			return invalidType
		}
		switch base.(type) {
		case *StructType, *OpaqueType:
			return &GenericInstanceType{Name: n.Name, Base: base, Args: args}
		default:
			a.errorf(n.Pos(), "type %q cannot be used with generic arguments", n.Name)
			return invalidType
		}
	default:
		return invalidType
	}
}

func (a *Analyzer) resolveErrorSetExpr(expr *ast.ErrorSetExpr) Type {
	if expr == nil {
		return invalidType
	}
	if len(expr.Tags) == 0 {
		a.errorf(expr.Pos(), "error[...] requires at least one qualified error tag")
		return invalidType
	}
	if expr.HasEllipsis && containsWildcardErrorTag(expr.Tags) {
		a.errorf(expr.Pos(), "error[Set.*, ...] is no longer supported; use error[Set, ...] or error[Set] instead")
		return invalidType
	}
	if containsWildcardErrorTag(expr.Tags) {
		if len(expr.Tags) != 1 {
			a.errorf(expr.Pos(), "error[Set.*] cannot be mixed with explicit tags")
			return invalidType
		}
		a.errorf(expr.Pos(), "error[Set.*] is no longer supported; use error[Set] instead")
		return invalidType
	}
	if len(expr.Tags) == 1 && expr.Tags[0].Tag == "" {
		_, errSet := a.lookupDeclaredErrorSet(expr.Tags[0])
		if errSet == nil {
			return invalidType
		}
		return errSet
	}
	if expr.HasEllipsis {
		return a.resolveExpandedErrorFamilies(expr)
	}

	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	selectedTags := map[string]map[string]bool{}
	seenTags := map[string]ast.ErrorTagExpr{}
	for _, tag := range expr.Tags {
		_, errSet := a.lookupDeclaredErrorSet(tag)
		if errSet == nil {
			return invalidType
		}
		familySets[tag.SetName] = errSet
		if tag.Tag == "" {
			fullFamilies[tag.SetName] = true
			for _, qualifiedTag := range errSet.Tags {
				if prev, ok := seenTags[qualifiedTag]; ok {
					_, shortName := SplitErrorTagName(qualifiedTag)
					a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s", shortName, prev.SetName, prev.Tag, tag.SetName)
					return invalidType
				}
				seenTags[qualifiedTag] = ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: ErrorTagShortName(qualifiedTag)}
			}
			continue
		}
		qualifiedTag := QualifyErrorTag(tag.SetName, tag.Tag)
		if !errSet.HasTag(qualifiedTag) {
			a.errorf(tag.Position, "error set %q has no tag %q", tag.SetName, tag.Tag)
			return invalidType
		}
		if prev, ok := seenTags[qualifiedTag]; ok {
			a.errorf(tag.Position, "duplicate error tag %q in error set via %s.%s and %s.%s", tag.Tag, prev.SetName, prev.Tag, tag.SetName, tag.Tag)
			return invalidType
		}
		seenTags[qualifiedTag] = tag
		if selectedTags[tag.SetName] == nil {
			selectedTags[tag.SetName] = map[string]bool{}
		}
		selectedTags[tag.SetName][qualifiedTag] = true
	}
	return CanonicalizeErrorSetSelections(familySets, fullFamilies, selectedTags)
}

func (a *Analyzer) lookupDeclaredErrorSet(tag ast.ErrorTagExpr) (string, *ErrorSetType) {
	t, ok := a.namedTypes[tag.SetName]
	if !ok {
		a.errorf(tag.Position, "unknown error set %q", tag.SetName)
		return "", nil
	}
	errSet, ok := t.(*ErrorSetType)
	if !ok {
		a.errorf(tag.Position, "%q is not an error set", tag.SetName)
		return "", nil
	}
	return tag.SetName, errSet
}

func containsWildcardErrorTag(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "*" {
			return true
		}
	}
	return false
}

func containsBareFamily(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag == "" {
			return true
		}
	}
	return false
}

func (a *Analyzer) resolveExpandedErrorFamilies(expr *ast.ErrorSetExpr) Type {
	familySets := map[string]*ErrorSetType{}
	fullFamilies := map[string]bool{}
	for _, tag := range expr.Tags {
		_, errSet := a.lookupDeclaredErrorSet(tag)
		if errSet == nil {
			return invalidType
		}
		if tag.Tag != "" && !errSet.HasQualifiedTag(tag.SetName, tag.Tag) {
			a.errorf(tag.Position, "error set %q has no tag %q", tag.SetName, tag.Tag)
			return invalidType
		}
		familySets[tag.SetName] = errSet
		fullFamilies[tag.SetName] = true
	}
	return CanonicalizeErrorSetSelections(familySets, fullFamilies, nil)
}

func onlyBareFamilies(tags []ast.ErrorTagExpr) bool {
	for _, tag := range tags {
		if tag.Tag != "" {
			return false
		}
	}
	return len(tags) > 0
}

func singleExplicitErrorFamily(tags []ast.ErrorTagExpr) (string, bool) {
	family := ""
	for _, tag := range tags {
		if tag.Tag == "" {
			return "", false
		}
		if family == "" {
			family = tag.SetName
			continue
		}
		if tag.SetName != family {
			return "", false
		}
	}
	return family, family != ""
}

func (a *Analyzer) resolveDynamicShapeType(expr *ast.GenericType) (Type, bool) {
	switch expr.Name {
	case "view":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "view expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DArrayViewType{Elem: a.resolveType(expr.Args[0]), SurfaceName: "view"}, true
	case "DArray":
		if len(expr.Args) != 2 {
			a.errorf(expr.Pos(), "DArray expects 2 arguments, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DArrayType{Elem: a.resolveType(expr.Args[0]), Shape: a.resolveShapeArg(expr.Args[1])}, true
	case "DArrayView":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "DArrayView expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DArrayViewType{Elem: a.resolveType(expr.Args[0])}, true
	case "DList":
		if len(expr.Args) != 2 {
			a.errorf(expr.Pos(), "DList expects 2 arguments, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DListType{Elem: a.resolveType(expr.Args[0]), Shape: a.resolveShapeArg(expr.Args[1])}, true
	case "DListView":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "DListView expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DListViewType{Elem: a.resolveType(expr.Args[0])}, true
	case "DStr":
		if len(expr.Args) != 1 {
			a.errorf(expr.Pos(), "DStr expects 1 argument, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DStrType{Shape: a.resolveShapeArg(expr.Args[0])}, true
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveBuiltinSurfaceType(expr *ast.BuiltinTypeExpr) Type {
	switch expr.Name {
	case "array":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "array expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		resolved := a.resolveArrayType(&ast.ArrayType{Position: expr.Position, Elem: expr.TypeArgs[0], Size: expr.ValueArgs[0]})
		if arrayType, ok := resolved.(*ArrayType); ok {
			arrayType.SurfaceName = "array"
		}
		return resolved
	case "darray":
		if len(expr.TypeArgs) != 1 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "darray expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &DArrayType{Elem: a.resolveType(expr.TypeArgs[0]), Shape: a.resolveShapeExpr(expr.ValueArgs[0]), SurfaceName: "darray"}
	case "str", "string":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "str expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		resolved := a.resolveArrayType(&ast.ArrayType{
			Position: expr.Position,
			Elem:     &ast.NamedType{Position: expr.Position, Name: "u8"},
			Size:     expr.ValueArgs[0],
		})
		if arrayType, ok := resolved.(*ArrayType); ok {
			arrayType.SurfaceName = "str"
		}
		return resolved
	case "dstr", "dstring":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 1 {
			a.errorf(expr.Pos(), "dstr expects 1 argument, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &DStrType{Shape: a.resolveShapeExpr(expr.ValueArgs[0]), SurfaceName: "dstr"}
	case "view":
		if len(expr.TypeArgs) != 1 {
			a.errorf(expr.Pos(), "view expects 1 type argument, got %d", len(expr.TypeArgs))
			return invalidType
		}
		viewType := &DArrayViewType{Elem: a.resolveType(expr.TypeArgs[0]), SurfaceName: "view"}
		if len(expr.ValueArgs) == 2 {
			viewType.Begin = a.exprSummary(expr.ValueArgs[0])
			viewType.End = a.exprSummary(expr.ValueArgs[1])
		} else if len(expr.ValueArgs) != 0 {
			a.errorf(expr.Pos(), "view expects either 1 or 3 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return viewType
	case "sview":
		if len(expr.TypeArgs) != 0 || len(expr.ValueArgs) != 2 {
			a.errorf(expr.Pos(), "sview expects 2 arguments, got %d", len(expr.TypeArgs)+len(expr.ValueArgs))
			return invalidType
		}
		return &SViewType{Begin: a.exprSummary(expr.ValueArgs[0]), End: a.exprSummary(expr.ValueArgs[1])}
	default:
		a.errorf(expr.Pos(), "unknown built-in type %q", expr.Name)
		return invalidType
	}
}

func (a *Analyzer) resolveShapeArg(expr ast.TypeExpr) Shape {
	name, ok := shapeNameFromTypeExpr(expr)
	if !ok {
		a.errorf(expr.Pos(), "shape witness must be an identifier")
		return &NamedShape{Name: "?"}
	}
	if shape, ok := a.lookupShapeParam(name); ok {
		return shape
	}
	return &NamedShape{Name: name}
}

func (a *Analyzer) resolveShapeExpr(expr ast.Expr) Shape {
	name, ok := shapeNameFromValueExpr(expr)
	if !ok {
		return &NamedShape{Name: a.exprSummary(expr)}
	}
	if shape, ok := a.lookupShapeParam(name); ok {
		return shape
	}
	return &NamedShape{Name: name}
}

func (a *Analyzer) genericTypeAsArrayType(expr *ast.GenericType) (*ast.ArrayType, bool) {
	if len(expr.Args) != 1 {
		return nil, false
	}
	base, ok := a.namedTypes[expr.Name]
	if !ok {
		return nil, false
	}
	switch base.(type) {
	case *StructType, *OpaqueType:
		return nil, false
	}
	sizeTypeExpr, ok := expr.Args[0].(*ast.NamedType)
	if !ok {
		return nil, false
	}
	return &ast.ArrayType{
		Position: expr.Position,
		Elem:     &ast.NamedType{Position: expr.Position, Name: expr.Name},
		Size:     &ast.Ident{Position: sizeTypeExpr.Position, Name: sizeTypeExpr.Name},
	}, true
}

func (a *Analyzer) inferLiteralType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				return t
			}
			if n.Suffix == "u" {
				return a.namedTypes["usize"]
			}
		}
		return a.namedTypes["int"]
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
	case *ast.BoolLit:
		return a.namedTypes["bool"]
	case *ast.NullLit:
		return nullType
	default:
		return invalidType
	}
}

func (a *Analyzer) validCast(src, dst Type) bool {
	if SameType(src, dst) || IsInvalidType(src) || IsInvalidType(dst) {
		return true
	}
	if _, ok := src.(*TypeParamType); ok {
		return true
	}
	if _, ok := dst.(*TypeParamType); ok {
		return true
	}
	if IsNumericType(src) && IsNumericType(dst) {
		return true
	}
	if IsNullType(src) {
		if ref, ok := dst.(*RefType); ok {
			return ref.State != RefStateNonNull
		}
	}
	if isRefLike(src) && IsNumericType(dst) {
		return true
	}
	if IsNumericType(src) && isRefLike(dst) {
		return true
	}
	if srcRef, ok := src.(*RefType); ok {
		if dstRef, ok := dst.(*RefType); ok {
			return refStateAssignable(dstRef.State, srcRef.State)
		}
	}
	return false
}

func (a *Analyzer) exprSummary(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n.Value
	case *ast.Ident:
		return n.Name
	case *ast.BinaryExpr:
		return fmt.Sprintf("%s%s%s", a.exprSummary(n.Left), lexer.TokenName(n.Op), a.exprSummary(n.Right))
	default:
		return "?"
	}
}

func (a *Analyzer) withTypeParams(names []string, args []Type, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Type, len(names))
	for i, name := range names {
		if i < len(args) && args[i] != nil {
			bindings[name] = args[i]
		} else {
			bindings[name] = &TypeParamType{Name: name}
		}
	}
	a.typeParamScopes = append(a.typeParamScopes, bindings)
	fn()
	a.typeParamScopes = a.typeParamScopes[:len(a.typeParamScopes)-1]
}

func (a *Analyzer) lookupTypeParam(name string) (Type, bool) {
	for i := len(a.typeParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.typeParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) lookupShapeParam(name string) (Shape, bool) {
	for i := len(a.shapeParamScopes) - 1; i >= 0; i-- {
		if t, ok := a.shapeParamScopes[i][name]; ok {
			return t, true
		}
	}
	return nil, false
}

func (a *Analyzer) substituteType(t Type, bindings map[string]Type, shapeBindings map[string]Shape) Type {
	switch n := t.(type) {
	case *TypeParamType:
		if resolved, ok := bindings[n.Name]; ok {
			return resolved
		}
		return n
	case *ErrorUnionType:
		return &ErrorUnionType{Value: a.substituteType(n.Value, bindings, shapeBindings), Errors: n.Errors}
	case *RefType:
		return &RefType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), State: n.State, Storage: n.Storage, ExplicitStorage: n.ExplicitStorage}
	case *ArrayType:
		return &ArrayType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), Size: n.Size, HasConstSize: n.HasConstSize, ConstSize: n.ConstSize, SurfaceName: n.SurfaceName}
	case *DArrayType:
		return &DArrayType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName}
	case *DArrayViewType:
		return &DArrayViewType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), Begin: n.Begin, End: n.End, SurfaceName: n.SurfaceName}
	case *DListType:
		return &DListType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), Shape: a.substituteShape(n.Shape, shapeBindings)}
	case *DListViewType:
		return &DListViewType{Elem: a.substituteType(n.Elem, bindings, shapeBindings)}
	case *DStrType:
		return &DStrType{Shape: a.substituteShape(n.Shape, shapeBindings), SurfaceName: n.SurfaceName}
	case *SViewType:
		return &SViewType{Begin: n.Begin, End: n.End}
	case *GenericInstanceType:
		args := make([]Type, 0, len(n.Args))
		for _, arg := range n.Args {
			args = append(args, a.substituteType(arg, bindings, shapeBindings))
		}
		return &GenericInstanceType{Name: n.Name, Base: n.Base, Args: args}
	case *FuncType:
		params := make([]Type, 0, len(n.Params))
		for _, param := range n.Params {
			params = append(params, a.substituteType(param, bindings, shapeBindings))
		}
		return &FuncType{Name: n.Name, TypeParams: append([]string(nil), n.TypeParams...), ShapeParams: append([]string(nil), n.ShapeParams...), FreshReturnShapeParams: append([]string(nil), n.FreshReturnShapeParams...), Params: params, Return: a.substituteType(n.Return, bindings, shapeBindings), Variadic: n.Variadic}
	default:
		return t
	}
}

func (a *Analyzer) substituteShape(shape Shape, bindings map[string]Shape) Shape {
	param, ok := shape.(*ShapeParam)
	if !ok {
		return shape
	}
	if resolved, ok := bindings[param.Name]; ok {
		return resolved
	}
	return shape
}

func (a *Analyzer) paramIsMutable(param ast.ParamDecl) bool {
	if param.Mutable {
		return true
	}
	_, ok := param.Type.(*ast.MutableType)
	return ok
}

func (a *Analyzer) withShapeParams(names []string, fn func()) {
	if len(names) == 0 {
		fn()
		return
	}
	bindings := make(map[string]Shape, len(names))
	for _, name := range names {
		bindings[name] = &ShapeParam{Name: name}
	}
	a.shapeParamScopes = append(a.shapeParamScopes, bindings)
	fn()
	a.shapeParamScopes = a.shapeParamScopes[:len(a.shapeParamScopes)-1]
}

func (a *Analyzer) collectImplicitShapeParams(params []ast.ParamDecl, ret ast.TypeExpr) []string {
	seen := map[string]bool{}
	order := make([]string, 0)
	for _, param := range params {
		a.collectImplicitShapeParamsFromType(param.Type, seen, &order)
	}
	if ret != nil {
		a.collectImplicitShapeParamsFromType(ret, seen, &order)
	}
	return order
}

func (a *Analyzer) collectImplicitShapeParamsFromType(expr ast.TypeExpr, seen map[string]bool, order *[]string) {
	if expr == nil {
		return
	}
	switch n := expr.(type) {
	case *ast.ErrorUnionTypeExpr:
		a.collectImplicitShapeParamsFromType(n.Value, seen, order)
		a.collectImplicitShapeParamsFromType(n.Errors, seen, order)
	case *ast.ErrorSetExpr:
		return
	case *ast.RefType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.MutableType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.TailType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.ArrayType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.BuiltinTypeExpr:
		switch n.Name {
		case "array", "view":
			for _, arg := range n.TypeArgs {
				a.collectImplicitShapeParamsFromType(arg, seen, order)
			}
		case "darray":
			if len(n.TypeArgs) > 0 {
				a.collectImplicitShapeParamsFromType(n.TypeArgs[0], seen, order)
			}
			if len(n.ValueArgs) > 0 {
				if name, ok := shapeNameFromValueExpr(n.ValueArgs[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		case "dstr", "dstring":
			if len(n.ValueArgs) > 0 {
				if name, ok := shapeNameFromValueExpr(n.ValueArgs[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		}
	case *ast.GenericType:
		switch n.Name {
		case "DArray":
			if len(n.Args) > 0 {
				a.collectImplicitShapeParamsFromType(n.Args[0], seen, order)
			}
			if len(n.Args) > 1 {
				if name, ok := shapeNameFromTypeExpr(n.Args[1]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		case "DArrayView":
			if len(n.Args) > 0 {
				a.collectImplicitShapeParamsFromType(n.Args[0], seen, order)
			}
		case "DList":
			if len(n.Args) > 0 {
				a.collectImplicitShapeParamsFromType(n.Args[0], seen, order)
			}
			if len(n.Args) > 1 {
				if name, ok := shapeNameFromTypeExpr(n.Args[1]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		case "DStr":
			if len(n.Args) > 0 {
				if name, ok := shapeNameFromTypeExpr(n.Args[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
			}
		case "DListView":
			if len(n.Args) > 0 {
				a.collectImplicitShapeParamsFromType(n.Args[0], seen, order)
			}
		default:
			for _, arg := range n.Args {
				a.collectImplicitShapeParamsFromType(arg, seen, order)
			}
		}
	}
}

func shapeNameFromTypeExpr(expr ast.TypeExpr) (string, bool) {
	name, ok := expr.(*ast.NamedType)
	if !ok {
		return "", false
	}
	return name.Name, true
}

func shapeNameFromValueExpr(expr ast.Expr) (string, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

func isImplicitShapeWitnessName(name string) bool {
	if strings.HasPrefix(name, "shape_") || strings.HasPrefix(name, "s_") {
		return true
	}
	runes := []rune(name)
	if len(runes) != 1 {
		return false
	}
	return unicode.In(runes[0], unicode.Greek)
}
