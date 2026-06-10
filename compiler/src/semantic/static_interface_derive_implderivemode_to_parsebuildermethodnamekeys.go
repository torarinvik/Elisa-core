package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

type implDeriveMode int

const (
	implDeriveNone implDeriveMode = iota
	implDeriveParseBuilder
	implDeriveNullBuilder
)

type implDeriveSpec struct {
	Mode       implDeriveMode
	TreeName   string
	Annotation ast.Annotation
}
type derivedMethodSignature struct {
	Params         []ast.ParamDecl
	ResolvedParams []Type
	ReturnType     ast.TypeExpr
	ResolvedReturn Type
	GenericParams  []ast.GenericParam
	Permissions    []ast.PermissionRef
	Ensures        []ast.EnsuresClause
}
type parseBuilderField struct {
	Name string
	Type Type
}
type parseBuilderConstructorCandidate struct {
	QualifiedName string
	Fields        []parseBuilderField
	Family        *TreeType
}

func (a *Analyzer) synthesizeDerivedImplMembers(decls []scopedDecl) {
	for _, scoped := range decls {
		decl, ok := scoped.Decl.(*ast.ImplDecl)
		if !ok || decl == nil {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			spec, hasSpec := a.implDeriveSpecFor(decl)
			if !hasSpec {
				a.validateUndeclaredOverrideMembers(decl)
				return
			}
			if decl.IsExtension() {
				a.errorf(spec.Annotation.Position, "@derive requires an interface impl, not an extension impl")
				return
			}
			iface, _, ok := a.lookupVisibleStaticInterface(decl.InterfaceName)
			if !ok || iface == nil {
				return
			}
			receiver := a.resolveType(decl.ForType)
			if receiver == nil || IsInvalidType(receiver) {
				return
			}
			assocExprs := implAssociatedTypeExprMap(decl)
			existing := implMethodNameSet(decl)
			var parseBuilderTree *TreeType
			if spec.Mode == implDeriveParseBuilder {
				resolvedTree, _, ok := a.lookupVisibleType(spec.TreeName)
				if !ok {
					a.errorf(spec.Annotation.Position, "@derive(parse_builder ...) on impl of %q references unknown tree %q", decl.InterfaceName, spec.TreeName)
					return
				}
				parseBuilderTree, ok = resolvedTree.(*TreeType)
				if !ok || parseBuilderTree == nil {
					a.errorf(spec.Annotation.Position, "@derive(parse_builder ...) on impl of %q requires a tree family name, got %s", decl.InterfaceName, resolvedTree.String())
					return
				}
			}
			synthesized := make([]ast.ImplMember, 0, len(iface.Methods))
			for _, member := range iface.Decl.Members {
				methodDecl, ok := member.(*ast.ExternFuncDecl)
				if !ok || methodDecl == nil {
					continue
				}
				if existing[methodDecl.Name] {
					continue
				}
				sig := a.instantiateDerivedMethodSignature(methodDecl, assocExprs)
				if sig == nil {
					continue
				}
				body, ok := a.deriveImplMethodBody(decl, spec, methodDecl, sig, parseBuilderTree)
				if !ok {
					continue
				}
				synthesized = append(synthesized, &ast.FuncDecl{
					Position:         methodDecl.Position,
					Name:             methodDecl.Name,
					TypeParams:       append([]string(nil), methodDecl.TypeParams...),
					RegionParams:     append([]string(nil), methodDecl.RegionParams...),
					PermissionParams: append([]string(nil), methodDecl.PermissionParams...),
					GenericParams:    append([]ast.GenericParam(nil), sig.GenericParams...),
					Permissions:      append([]ast.PermissionRef(nil), sig.Permissions...),
					Ensures:          append([]ast.EnsuresClause(nil), sig.Ensures...),
					Params:           append([]ast.ParamDecl(nil), sig.Params...),
					ReturnType:       sig.ReturnType,
					Body:             body,
				})
				existing[methodDecl.Name] = true
			}
			if len(synthesized) != 0 {
				decl.Members = append(decl.Members, synthesized...)
			}
		})
	}
}
func (a *Analyzer) validateUndeclaredOverrideMembers(decl *ast.ImplDecl) {
	if decl == nil {
		return
	}
	for _, member := range decl.Members {
		switch fn := member.(type) {
		case *ast.FuncDecl:
			if fn.Override {
				a.errorf(fn.Position, "override def %q requires a derived impl annotation on the surrounding impl", fn.Name)
			}
		case *ast.ExternFuncDecl:
			if fn.Override {
				a.errorf(fn.Position, "override def %q requires a derived impl annotation on the surrounding impl", fn.Name)
			}
		}
	}
}
func (a *Analyzer) implDeriveSpecFor(decl *ast.ImplDecl) (*implDeriveSpec, bool) {
	if decl == nil || len(decl.Annotations) == 0 {
		return nil, false
	}
	var spec *implDeriveSpec
	for _, annotation := range decl.Annotations {
		if annotation.Name != "derive" {
			a.errorf(annotation.Position, "unknown impl annotation @%s", annotation.Name)
			continue
		}
		if spec != nil {
			a.errorf(annotation.Position, "duplicate @derive annotation on impl of %q", decl.InterfaceName)
			continue
		}
		parsed, ok := a.parseImplDeriveAnnotation(decl, annotation)
		if !ok {
			continue
		}
		spec = parsed
	}
	if spec == nil {
		return nil, false
	}
	return spec, true
}
func (a *Analyzer) parseImplDeriveAnnotation(decl *ast.ImplDecl, annotation ast.Annotation) (*implDeriveSpec, bool) {
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@derive on impl of %q expects a derive mode", decl.InterfaceName)
		return nil, false
	}
	switch annotation.Args[0] {
	case "null_builder":
		if len(annotation.Args) != 1 {
			a.errorf(annotation.Position, "@derive(null_builder) on impl of %q does not take extra arguments", decl.InterfaceName)
			return nil, false
		}
		return &implDeriveSpec{Mode: implDeriveNullBuilder, Annotation: annotation}, true
	case "parse_builder":
		if len(annotation.Args) == 2 {
			return &implDeriveSpec{Mode: implDeriveParseBuilder, TreeName: annotation.Args[1], Annotation: annotation}, true
		}
		if len(annotation.Args) == 3 && annotation.Args[1] == "tree" {
			return &implDeriveSpec{Mode: implDeriveParseBuilder, TreeName: annotation.Args[2], Annotation: annotation}, true
		}
		a.errorf(annotation.Position, "@derive(parse_builder ...) on impl of %q expects a tree name, for example @derive(parse_builder tree Syntax)", decl.InterfaceName)
		return nil, false
	default:
		a.errorf(annotation.Position, "unknown @derive mode %q on impl of %q", annotation.Args[0], decl.InterfaceName)
		return nil, false
	}
}
func implAssociatedTypeExprMap(decl *ast.ImplDecl) map[string]ast.TypeExpr {
	if decl == nil {
		return nil
	}
	bindings := make(map[string]ast.TypeExpr, len(decl.Members))
	for _, member := range decl.Members {
		assoc, ok := member.(*ast.ImplAssociatedTypeDecl)
		if !ok || assoc == nil || assoc.Type == nil {
			continue
		}
		bindings[assoc.Name] = assoc.Type
	}
	return bindings
}
func implMethodNameSet(decl *ast.ImplDecl) map[string]bool {
	seen := map[string]bool{}
	if decl == nil {
		return seen
	}
	for _, member := range decl.Members {
		switch fn := member.(type) {
		case *ast.FuncDecl:
			seen[fn.Name] = true
		case *ast.ExternFuncDecl:
			seen[fn.Name] = true
		}
	}
	return seen
}
func (a *Analyzer) instantiateDerivedMethodSignature(method *ast.ExternFuncDecl, assocExprs map[string]ast.TypeExpr) *derivedMethodSignature {
	if method == nil {
		return nil
	}
	params := make([]ast.ParamDecl, 0, len(method.Params))
	resolvedParams := make([]Type, 0, len(method.Params))
	for _, param := range method.Params {
		cloned := ast.ParamDecl{Position: param.Position, Name: param.Name, Mutable: param.Mutable, Type: substituteAssocTypeExpr(param.Type, assocExprs), DefaultValue: param.DefaultValue}
		params = append(params, cloned)
	}
	returnType := substituteAssocTypeExpr(method.ReturnType, assocExprs)
	genericParams := append([]ast.GenericParam(nil), method.GenericParams...)
	var resolvedReturn Type
	a.withGenericParams(genericParams, nil, func() {
		for _, param := range params {
			resolvedParams = append(resolvedParams, a.resolveType(param.Type))
		}
		if returnType != nil {
			resolvedReturn = a.resolveType(returnType)
		}
	})
	return &derivedMethodSignature{
		Params:         params,
		ResolvedParams: resolvedParams,
		ReturnType:     returnType,
		ResolvedReturn: resolvedReturn,
		GenericParams:  genericParams,
		Permissions:    append([]ast.PermissionRef(nil), method.Permissions...),
		Ensures:        append([]ast.EnsuresClause(nil), method.Ensures...),
	}
}
func substituteAssocTypeExpr(expr ast.TypeExpr, assocExprs map[string]ast.TypeExpr) ast.TypeExpr {
	if expr == nil {
		return nil
	}
	switch n := expr.(type) {
	case *ast.NamedType:
		if replacement, ok := assocExprs[n.Name]; ok && replacement != nil {
			return substituteAssocTypeExpr(replacement, assocExprs)
		}
		return &ast.NamedType{Position: n.Position, Name: n.Name}
	case *ast.RefType:
		return &ast.RefType{Position: n.Position, Elem: substituteAssocTypeExpr(n.Elem, assocExprs), State: n.State, Storage: n.Storage, Region: n.Region, Explicit: n.Explicit}
	case *ast.RefStateLiteralTypeExpr:
		return &ast.RefStateLiteralTypeExpr{Position: n.Position, State: n.State}
	case *ast.RefStorageLiteralTypeExpr:
		return &ast.RefStorageLiteralTypeExpr{Position: n.Position, Storage: n.Storage}
	case *ast.GenericType:
		args := make([]ast.TypeExpr, 0, len(n.Args))
		for _, arg := range n.Args {
			args = append(args, substituteAssocTypeExpr(arg, assocExprs))
		}
		return &ast.GenericType{Position: n.Position, Name: n.Name, Args: args}
	case *ast.AggregateStateTypeExpr:
		return &ast.AggregateStateTypeExpr{Position: n.Position, Base: substituteAssocTypeExpr(n.Base, assocExprs), State: n.State, States: append([]ast.RefState(nil), n.States...)}
	case *ast.StateSetTypeExpr:
		return &ast.StateSetTypeExpr{Position: n.Position, Cases: append([]string(nil), n.Cases...)}
	case *ast.MutableType:
		return &ast.MutableType{Position: n.Position, Elem: substituteAssocTypeExpr(n.Elem, assocExprs)}
	case *ast.TailType:
		return &ast.TailType{Position: n.Position, Elem: substituteAssocTypeExpr(n.Elem, assocExprs)}
	case *ast.ArrayType:
		return &ast.ArrayType{Position: n.Position, Elem: substituteAssocTypeExpr(n.Elem, assocExprs), Size: n.Size}
	case *ast.BuiltinTypeExpr:
		typeArgs := make([]ast.TypeExpr, 0, len(n.TypeArgs))
		for _, arg := range n.TypeArgs {
			typeArgs = append(typeArgs, substituteAssocTypeExpr(arg, assocExprs))
		}
		return &ast.BuiltinTypeExpr{Position: n.Position, Name: n.Name, TypeArgs: typeArgs, ValueArgs: append([]ast.Expr(nil), n.ValueArgs...)}
	case *ast.FuncTypeExpr:
		params := make([]ast.TypeExpr, 0, len(n.Params))
		for _, param := range n.Params {
			params = append(params, substituteAssocTypeExpr(param, assocExprs))
		}
		return &ast.FuncTypeExpr{Position: n.Position, Params: params, Return: substituteAssocTypeExpr(n.Return, assocExprs), Permissions: append([]ast.PermissionRef(nil), n.Permissions...), Variadic: n.Variadic}
	case *ast.ErrorSetExpr:
		tags := make([]ast.ErrorTagExpr, 0, len(n.Tags))
		for _, tag := range n.Tags {
			tags = append(tags, ast.ErrorTagExpr{Position: tag.Position, SetName: tag.SetName, Tag: tag.Tag})
		}
		return &ast.ErrorSetExpr{Position: n.Position, Tags: tags, HasEllipsis: n.HasEllipsis}
	case *ast.ErrorUnionTypeExpr:
		return &ast.ErrorUnionTypeExpr{Position: n.Position, Value: substituteAssocTypeExpr(n.Value, assocExprs), Errors: substituteAssocTypeExpr(n.Errors, assocExprs)}
	case *ast.OptionalTypeExpr:
		return &ast.OptionalTypeExpr{Position: n.Position, Value: substituteAssocTypeExpr(n.Value, assocExprs)}
	case *ast.TupleTypeExpr:
		fields := make([]ast.TupleTypeField, 0, len(n.Fields))
		for _, field := range n.Fields {
			fields = append(fields, ast.TupleTypeField{Position: field.Position, Name: field.Name, Type: substituteAssocTypeExpr(field.Type, assocExprs)})
		}
		return &ast.TupleTypeExpr{Position: n.Position, Fields: fields}
	default:
		return expr
	}
}
func (a *Analyzer) deriveImplMethodBody(decl *ast.ImplDecl, spec *implDeriveSpec, method *ast.ExternFuncDecl, sig *derivedMethodSignature, treeType *TreeType) ([]ast.Stmt, bool) {
	if method == nil || sig == nil || spec == nil {
		return nil, false
	}
	if method.Variadic {
		a.errorf(method.Position, "cannot synthesize variadic impl method %q in impl of %q", method.Name, decl.InterfaceName)
		return nil, false
	}
	switch spec.Mode {
	case implDeriveNullBuilder:
		return deriveNullBuilderBody(method.Position, sig.ReturnType, sig.ResolvedReturn), true
	case implDeriveParseBuilder:
		body, ok := a.deriveParseBuilderBody(method, sig, treeType)
		if !ok {
			a.errorf(method.Position, "@derive(parse_builder ...) could not synthesize impl method %q in impl of interface %q; add an explicit override", method.Name, decl.InterfaceName)
			return nil, false
		}
		return body, true
	default:
		return nil, false
	}
}
func deriveNullBuilderBody(pos lexer.Pos, returnType ast.TypeExpr, resolvedReturn Type) []ast.Stmt {
	if resolvedReturn == nil || isVoidType(resolvedReturn) || returnType == nil {
		return []ast.Stmt{&ast.PassStmt{Position: pos}}
	}
	return typedZeroedReturnBody(pos, returnType)
}
func typedZeroedReturnBody(pos lexer.Pos, returnType ast.TypeExpr) []ast.Stmt {
	const tempName = "__derived_result"
	return []ast.Stmt{
		&ast.VarDeclStmt{Position: pos, Name: tempName, Type: returnType, Value: &ast.ZeroedLit{Position: pos}},
		&ast.ReturnStmt{Position: pos, Value: &ast.Ident{Position: pos, Name: tempName}},
	}
}
func (a *Analyzer) deriveParseBuilderBody(method *ast.ExternFuncDecl, sig *derivedMethodSignature, treeType *TreeType) ([]ast.Stmt, bool) {
	if method == nil || sig == nil || treeType == nil {
		return nil, false
	}
	if body, ok := deriveParseBuilderListInitBody(method, sig); ok {
		return body, true
	}
	if body, ok := deriveParseBuilderListAppendBody(method, sig); ok {
		return body, true
	}
	return a.deriveParseBuilderConstructorBody(method, sig, treeType)
}
func deriveParseBuilderListInitBody(method *ast.ExternFuncDecl, sig *derivedMethodSignature) ([]ast.Stmt, bool) {
	if method == nil || sig == nil || len(sig.Params) != 0 {
		return nil, false
	}
	if _, ok := StripAggregateStateType(sig.ResolvedReturn).(*DArrayType); !ok {
		return nil, false
	}
	return typedZeroedReturnBody(method.Position, sig.ReturnType), true
}
func deriveParseBuilderListAppendBody(method *ast.ExternFuncDecl, sig *derivedMethodSignature) ([]ast.Stmt, bool) {
	if method == nil || sig == nil || len(sig.Params) != 3 {
		return nil, false
	}
	if sig.ResolvedReturn != nil && !isVoidType(sig.ResolvedReturn) {
		return nil, false
	}
	itemsRef, ok := StripAggregateStateType(sig.ResolvedParams[1]).(*RefType)
	if !ok || itemsRef == nil {
		return nil, false
	}
	itemsType, ok := StripAggregateStateType(itemsRef.Elem).(*DArrayType)
	if !ok || itemsType == nil {
		return nil, false
	}
	if !SameType(itemsType.Elem, StripAggregateStateType(sig.ResolvedParams[2])) {
		return nil, false
	}
	call := &ast.CallExpr{Position: method.Position, Func: &ast.Ident{Position: method.Position, Name: "arena_da_append"}, Args: []ast.Expr{
		&ast.Ident{Position: method.Position, Name: sig.Params[0].Name},
		&ast.Ident{Position: method.Position, Name: sig.Params[1].Name},
		&ast.Ident{Position: method.Position, Name: sig.Params[2].Name},
	}}
	return []ast.Stmt{&ast.DiscardStmt{Position: method.Position, Value: call}}, true
}
func (a *Analyzer) deriveParseBuilderConstructorBody(method *ast.ExternFuncDecl, sig *derivedMethodSignature, treeType *TreeType) ([]ast.Stmt, bool) {
	if method == nil || sig == nil || len(sig.Params) == 0 {
		return nil, false
	}
	candidates := parseBuilderConstructorCandidates(StripAggregateStateType(sig.ResolvedReturn), treeType)
	if len(candidates) == 0 {
		return nil, false
	}
	params := sig.Params[1:]
	paramTypes := sig.ResolvedParams[1:]
	matched := make([]parseBuilderConstructorCandidate, 0, 1)
	for _, candidate := range candidates {
		if parseBuilderConstructorMatches(params, paramTypes, candidate.Fields) {
			matched = append(matched, candidate)
		}
	}
	if len(matched) == 0 {
		return nil, false
	}
	if len(matched) > 1 {
		filtered := filterParseBuilderCandidatesByMethodName(method.Name, matched)
		if len(filtered) == 1 {
			matched = filtered
		}
	}
	if len(matched) != 1 {
		return nil, false
	}
	owner := &ast.Ident{Position: method.Position, Name: sig.Params[0].Name}
	ctorArgs := make([]ast.Expr, 0, len(matched[0].Fields))
	ctorArgNames := make([]string, 0, len(matched[0].Fields))
	for _, field := range matched[0].Fields {
		ctorArgs = append(ctorArgs, &ast.Ident{Position: method.Position, Name: field.Name})
		ctorArgNames = append(ctorArgNames, field.Name)
	}
	ctor := &ast.CallExpr{Position: method.Position, Func: qualifiedExpr(method.Position, matched[0].QualifiedName), Args: ctorArgs, ArgNames: ctorArgNames}
	value := &ast.AllocExpr{Position: method.Position, Owner: owner, Value: ctor}
	return []ast.Stmt{&ast.ReturnStmt{Position: method.Position, Value: value}}, true
}
func parseBuilderConstructorCandidates(returnType Type, treeType *TreeType) []parseBuilderConstructorCandidate {
	if treeType == nil || returnType == nil {
		return nil
	}
	switch tt := returnType.(type) {
	case *TreeCategoryType:
		if tt == nil || tt.Family == nil || tt.Family.Name != treeType.Name {
			return nil
		}
		commonDecls := TreeCommonFieldDeclsForFamily(tt.Family)
		out := make([]parseBuilderConstructorCandidate, 0, len(tt.Variants))
		for _, variant := range tt.Variants {
			if variant == nil || !treeVariantHasNamedPayloads(variant) {
				continue
			}
			fields := make([]parseBuilderField, 0, len(commonDecls)+len(variant.Payload))
			for _, decl := range commonDecls {
				if field, ok := tt.Common[decl.Name]; ok {
					fields = append(fields, parseBuilderField{Name: decl.Name, Type: field.Type})
				}
			}
			for i := range variant.Payload {
				fields = append(fields, parseBuilderField{Name: variant.PayloadLabel(i), Type: variant.Payload[i]})
			}
			out = append(out, parseBuilderConstructorCandidate{QualifiedName: tt.Name + "." + variant.Name, Fields: fields, Family: tt.Family})
		}
		return out
	case *TreeBlockType:
		if tt == nil || tt.Family == nil || tt.Family.Name != treeType.Name {
			return nil
		}
		return []parseBuilderConstructorCandidate{{QualifiedName: tt.Name, Fields: treeFieldDeclsToConstructorFields(TreeBlockFieldDeclsWithCommon(tt), tt.Family.Common, tt.Fields), Family: tt.Family}}
	case *TreeStructType:
		if tt == nil || tt.Family == nil || tt.Family.Name != treeType.Name {
			return nil
		}
		return []parseBuilderConstructorCandidate{{QualifiedName: tt.Name, Fields: treeFieldDeclsToConstructorFields(TreeStructFieldDeclsWithCommon(tt), tt.Family.Common, tt.Fields), Family: tt.Family}}
	default:
		return nil
	}
}
func treeVariantHasNamedPayloads(variant *EnumVariant) bool {
	if variant == nil {
		return false
	}
	for i := range variant.Payload {
		if variant.PayloadLabel(i) == "" {
			return false
		}
	}
	return true
}
func treeFieldDeclsToConstructorFields(decls []ast.FieldDecl, common map[string]Field, exact map[string]Field) []parseBuilderField {
	fields := make([]parseBuilderField, 0, len(decls))
	for _, decl := range decls {
		if field, ok := exact[decl.Name]; ok {
			fields = append(fields, parseBuilderField{Name: decl.Name, Type: field.Type})
			continue
		}
		if field, ok := common[decl.Name]; ok {
			fields = append(fields, parseBuilderField{Name: decl.Name, Type: field.Type})
		}
	}
	return fields
}
func parseBuilderConstructorMatches(params []ast.ParamDecl, paramTypes []Type, fields []parseBuilderField) bool {
	if len(params) != len(fields) || len(paramTypes) != len(fields) {
		return false
	}
	paramByName := make(map[string]Type, len(params))
	for i, param := range params {
		if param.Name == "" {
			return false
		}
		if _, exists := paramByName[param.Name]; exists {
			return false
		}
		paramByName[param.Name] = StripAggregateStateType(paramTypes[i])
	}
	for _, field := range fields {
		paramType, ok := paramByName[field.Name]
		if !ok || field.Type == nil || !SameType(StripAggregateStateType(field.Type), paramType) {
			return false
		}
	}
	return true
}
func filterParseBuilderCandidatesByMethodName(methodName string, candidates []parseBuilderConstructorCandidate) []parseBuilderConstructorCandidate {
	if len(candidates) <= 1 {
		return candidates
	}
	suffix := strings.TrimPrefix(methodName, "make_")
	if suffix == methodName || suffix == "" {
		return nil
	}
	keys := parseBuilderMethodNameKeys(suffix)
	if len(keys) == 0 {
		return nil
	}
	filtered := make([]parseBuilderConstructorCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		targetKey := normalizeParseBuilderName(lastQualifiedNamePart(candidate.QualifiedName))
		if keys[targetKey] {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}
func parseBuilderMethodNameKeys(suffix string) map[string]bool {
	keys := map[string]bool{}
	add := func(value string) {
		normalized := normalizeParseBuilderName(value)
		if normalized != "" {
			keys[normalized] = true
		}
	}
	add(suffix)
	if strings.HasSuffix(suffix, "_stmt") {
		trimmed := strings.TrimSuffix(suffix, "_stmt")
		add(trimmed)
		add(trimmed + "_lit")
	}
	if !strings.HasSuffix(suffix, "_lit") {
		add(suffix + "_lit")
	}
	return keys
}
