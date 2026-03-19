package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

type ConstValueKind int

const (
	ConstUnknown ConstValueKind = iota
	ConstInt
	ConstBool
	ConstString
)

type ConstValue struct {
	Kind   ConstValueKind
	Int    int64
	Bool   bool
	String string
}

type ShapeTransformSpec struct {
	FreshReturnShapeParams []string
}

var shapeTransformTable = map[string]ShapeTransformSpec{
	"resize":                              {FreshReturnShapeParams: []string{"shape_out"}},
	"push":                                {FreshReturnShapeParams: []string{"shape_out"}},
	"append_many":                         {FreshReturnShapeParams: []string{"shape_out"}},
	"truncate":                            {FreshReturnShapeParams: []string{"shape_out"}},
	"clear":                               {FreshReturnShapeParams: []string{"shape_out"}},
	"concat":                              {FreshReturnShapeParams: []string{"shape_result"}},
	"strcat":                              {FreshReturnShapeParams: []string{"shape_result"}},
	"arena_da_append":                     {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_append_many":                {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_concat2":                {FreshReturnShapeParams: []string{"shape_result"}},
	"ctx_stage1rt_concat2_scratch":        {FreshReturnShapeParams: []string{"shape_result"}},
	"ctx_stage1rt_string_builder_finish":  {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_int_to_string":          {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_int_to_string_scratch":  {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_bool_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_bool_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_char_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_char_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_string_slice":           {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_string_from_view":       {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_from_view":                  {FreshReturnShapeParams: []string{"shape_out"}},
}

type freshReturnStatus int

const (
	freshReturnUnknown freshReturnStatus = iota
	freshReturnAlways
	freshReturnNotFresh
)

type Analyzer struct {
	file                              *ast.File
	diagnostics                       []Diagnostic
	namedTypes                        map[string]Type
	permissions                       map[string]*PermissionSet
	globalScope                       *Scope
	functionTypes                     map[string]*FuncType
	constValues                       map[string]ConstValue
	exprTypes                         map[ast.Expr]Type
	typeParamScopes                   []map[string]Type
	shapeParamScopes                  []map[string]Shape
	regionParamScopes                 []map[string]bool
	freshShapeCounter                 int
	returnFreshShapeStatus            map[string]freshReturnStatus
	annotatedFuncs                    []*AnnotatedFunc
	exportedTypes                     []*ExportedType
	exportedFuncs                     []*ExportedFunc
	exportedGlobals                   []*ExportedGlobal
	currentScope                      *Scope
	currentReturn                     Type
	currentRegions                    map[*Symbol]regionState
	currentRegionMarks                map[*Symbol]regionMarkState
	currentRegionRefs                 map[*Symbol]regionRefState
	currentPackedStores               map[string]*PackedEnumStoreType
	currentFunctionUsedPermissions    map[string]bool
	currentFunctionUsedPermissionRefs []ast.PermissionRef
}

type regionState struct {
	Destroyed  bool
	Generation int
}

type regionMarkState struct {
	Region        *Symbol
	Generation    int
	Valid         bool
	InvalidatedBy string
}

type regionRefState struct {
	Region        *Symbol
	Generation    int
	Valid         bool
	InvalidatedBy string
}

func Analyze(file *ast.File) *Result {
	a := &Analyzer{
		file:          file,
		namedTypes:    map[string]Type{},
		permissions:   map[string]*PermissionSet{},
		globalScope:   NewScope(nil),
		functionTypes: map[string]*FuncType{},
		constValues:   map[string]ConstValue{},
		exprTypes:     map[ast.Expr]Type{},
	}
	a.registerBuiltins()
	a.collectConstValues(file.Decls)
	activeDecls := a.expandActiveDecls(file.Decls)
	a.collectPermissionDecls(activeDecls)
	a.collectNamedTypes(activeDecls)
	a.populateStructFields(activeDecls)
	a.populateEnumVariants(activeDecls)
	a.collectExportTypeAliases(activeDecls)
	a.collectValueSymbols(activeDecls)
	a.analyzeDecls(activeDecls)
	a.inferFunctionPermissionEffects(activeDecls)
	a.warnOnImplicitFunctionPermissions(activeDecls)
	a.validatePermissionUsage(activeDecls)
	a.analyzeExports(activeDecls)
	return &Result{
		File:            file,
		GlobalScope:     a.globalScope,
		NamedTypes:      a.namedTypes,
		ConstValues:     a.constValues,
		ExprTypes:       a.exprTypes,
		AnnotatedFuncs:  a.annotatedFuncs,
		ExportedTypes:   a.exportedTypes,
		ExportedFuncs:   a.exportedFuncs,
		ExportedGlobals: a.exportedGlobals,
		Diagnostics:     a.diagnostics,
	}
}

func (a *Analyzer) registerBuiltins() {
	for _, name := range []string{"void", "bool", "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr"} {
		a.namedTypes[name] = &BuiltinType{Name: name}
	}
	a.registerBuiltinPermissions()
	a.registerBuiltinRuntimeStructs()
}

func (a *Analyzer) registerBuiltinPermissions() {
	a.registerBuiltinPermission("Memory", []string{"Allocate", "Release"})
	a.registerBuiltinPermission("Console", []string{"Format", "Write"})
	a.registerBuiltinPermission("Abort", []string{"Exit", "Panic"})
}

func (a *Analyzer) registerBuiltinPermission(name string, members []string) {
	memberSet := make(map[string]bool, len(members))
	canonicalMembers := make([]string, 0, len(members))
	for _, member := range members {
		if memberSet[member] {
			continue
		}
		memberSet[member] = true
		canonicalMembers = append(canonicalMembers, member)
	}
	a.permissions[name] = &PermissionSet{
		Name:      name,
		Members:   canonicalMembers,
		MemberSet: memberSet,
		Builtin:   true,
	}
}

func (a *Analyzer) registerBuiltinRuntimeStructs() {
	a.registerBuiltinStructType("Region", nil, []builtinFieldSpec{
		{name: "next", typ: refTypeExpr("Region", true), mutable: true},
		{name: "count", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "capacity", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "owner_tag", typ: namedTypeExpr("uintptr", false), mutable: true},
		{name: "owner_next", typ: refTypeExpr("Region", true), mutable: true},
		{name: "global_index", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "data", typ: namedTypeExpr("uintptr", false), isTail: true},
	})
	a.registerBuiltinStructType("Arena", nil, []builtinFieldSpec{
		{name: "begin", typ: refTypeExpr("Region", true), mutable: true},
		{name: "end", typ: refTypeExpr("Region", true), mutable: true},
		{name: "end_index", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("ArenaMark", nil, []builtinFieldSpec{
		{name: "region", typ: refTypeExpr("Region", true), mutable: true},
		{name: "count", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("PackedStoreAllocResult", nil, []builtinFieldSpec{
		{name: "row", typ: refTypeExpr("void", true), mutable: true},
		{name: "handle", typ: namedTypeExpr("uintptr", false), mutable: true},
	})
	a.registerBuiltinStructType("StringView", nil, []builtinFieldSpec{
		{name: "data", typ: refTypeExpr("u8", false), mutable: true},
		{name: "len", typ: namedTypeExpr("i64", false), mutable: true},
	})
	a.registerBuiltinStructType("DynArray", []string{"T"}, []builtinFieldSpec{
		{name: "items", typ: refTypeParamExpr("T", true), mutable: true},
		{name: "count", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "capacity", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("DynArrayView", nil, []builtinFieldSpec{
		{name: "data", typ: refTypeExpr("void", true), mutable: true},
		{name: "len", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "elem_size", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("DictBucket", []string{"T"}, []builtinFieldSpec{
		{name: "state", typ: namedTypeExpr("u8", false), mutable: true},
		{name: "hash", typ: namedTypeExpr("u64", false), mutable: true},
		{name: "key_data", typ: refTypeExpr("u8", true), mutable: true},
		{name: "key_len", typ: namedTypeExpr("i64", false), mutable: true},
		{name: "value", typ: namedTypeExpr("T", false), mutable: true},
	})
	a.registerBuiltinStructType("DynDict", []string{"T"}, []builtinFieldSpec{
		{name: "items", typ: refToTypeExpr(genericTypeExpr("DictBucket", namedTypeExpr("T", false)), true), mutable: true},
		{name: "count", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "used", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "capacity", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "arena", typ: refTypeExpr("Arena", true), mutable: true},
	})
}

type builtinFieldSpec struct {
	name    string
	typ     ast.TypeExpr
	mutable bool
	isTail  bool
}

func (a *Analyzer) registerBuiltinStructType(name string, typeParams []string, fields []builtinFieldSpec) {
	declFields := make([]ast.FieldDecl, 0, len(fields))
	semanticFields := make(map[string]Field, len(fields))
	decl := &ast.StructDecl{Position: lexer.Pos{}, Name: name, TypeParams: append([]string(nil), typeParams...), ReprC: true}
	st := &StructType{
		Name:       name,
		TypeParams: append([]string(nil), typeParams...),
		Fields:     semanticFields,
		ReprC:      true,
		Decl:       decl,
		Builtin:    true,
	}
	a.namedTypes[name] = st
	a.withTypeParams(typeParams, nil, func() {
		for _, field := range fields {
			declField := ast.FieldDecl{Position: lexer.Pos{}, Name: field.name, Mutable: field.mutable, IsTail: field.isTail, Type: field.typ}
			declFields = append(declFields, declField)
			fieldType := a.resolveType(field.typ)
			if field.isTail {
				fieldType = &RefType{Elem: fieldType, State: RefStateNonNull, Storage: RefStorageAny}
			}
			semanticFields[field.name] = Field{Name: field.name, Type: fieldType, Mutable: field.mutable, IsTail: field.isTail}
		}
	})
	decl.Fields = declFields
}

func namedTypeExpr(name string, mutable bool) ast.TypeExpr {
	base := ast.TypeExpr(&ast.NamedType{Position: lexer.Pos{}, Name: name})
	if mutable {
		return &ast.MutableType{Position: lexer.Pos{}, Elem: base}
	}
	return base
}

func genericTypeExpr(name string, args ...ast.TypeExpr) ast.TypeExpr {
	return &ast.GenericType{Position: lexer.Pos{}, Name: name, Args: args}
}

func refToTypeExpr(elem ast.TypeExpr, nullable bool) ast.TypeExpr {
	state := ast.RefStateNonNull
	if nullable {
		state = ast.RefStateNullable
	}
	return &ast.RefType{Position: lexer.Pos{}, Elem: elem, State: state, Storage: ast.RefStorageAny}
}

func refTypeExpr(name string, nullable bool) ast.TypeExpr {
	return refToTypeExpr(&ast.NamedType{Position: lexer.Pos{}, Name: name}, nullable)
}

func refTypeParamExpr(name string, nullable bool) ast.TypeExpr {
	return refToTypeExpr(&ast.NamedType{Position: lexer.Pos{}, Name: name}, nullable)
}

func nestedRefTypeExpr(name string, innerNonNull bool, outerNullable bool) ast.TypeExpr {
	innerState := ast.RefStateNullable
	if innerNonNull {
		innerState = ast.RefStateNonNull
	}
	outerState := ast.RefStateNonNull
	if outerNullable {
		outerState = ast.RefStateNullable
	}
	inner := &ast.RefType{Position: lexer.Pos{}, Elem: &ast.NamedType{Position: lexer.Pos{}, Name: name}, State: innerState, Storage: ast.RefStorageAny}
	return &ast.RefType{Position: lexer.Pos{}, Elem: inner, State: outerState, Storage: ast.RefStorageAny}
}

func isBuiltinRuntimeStructName(name string) bool {
	switch name {
	case "Region", "Arena", "ArenaMark", "StringView", "DynArray", "DynArrayView", "DictBucket", "DynDict":
		return true
	case "PackedStoreAllocResult":
		return true
	default:
		return false
	}
}

func (a *Analyzer) collectConstValues(decls []ast.Decl) {
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.ConstDecl:
			if value, ok := a.evalConstExpr(n.Value); ok {
				a.constValues[n.Name] = value
			}
		case *ast.StaticIfDecl:
			a.collectConstValues(a.activeDeclBranch(n))
		}
	}
}

func (a *Analyzer) expandActiveDecls(decls []ast.Decl) []ast.Decl {
	out := make([]ast.Decl, 0, len(decls))
	for _, decl := range decls {
		if n, ok := decl.(*ast.StaticIfDecl); ok {
			out = append(out, a.expandActiveDecls(a.activeDeclBranch(n))...)
			continue
		}
		out = append(out, decl)
	}
	return out
}

func (a *Analyzer) activeDeclBranch(n *ast.StaticIfDecl) []ast.Decl {
	if selected, ok := a.evalConstBoolExpr(n.Cond); ok {
		if selected {
			return n.Then
		}
	} else {
		a.errorf(n.Pos(), "static if condition must be a compile-time bool")
		return n.Then
	}
	for _, elif := range n.Elifs {
		selected, ok := a.evalConstBoolExpr(elif.Cond)
		if !ok {
			a.errorf(elif.Position, "static elif condition must be a compile-time bool")
			continue
		}
		if selected {
			return elif.Body
		}
	}
	return n.Else
}

func (a *Analyzer) activeStmtBranch(n *ast.StaticIfStmt) []ast.Stmt {
	if selected, ok := a.evalConstBoolExpr(n.Cond); ok {
		if selected {
			return n.Then
		}
	} else {
		a.errorf(n.Pos(), "static if condition must be a compile-time bool")
		return n.Then
	}
	for _, elif := range n.Elifs {
		selected, ok := a.evalConstBoolExpr(elif.Cond)
		if !ok {
			a.errorf(elif.Position, "static elif condition must be a compile-time bool")
			continue
		}
		if selected {
			return elif.Body
		}
	}
	return n.Else
}

func (a *Analyzer) collectNamedTypes(decls []ast.Decl) {
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.StructDecl:
			if existing, exists := a.namedTypes[n.Name]; exists {
				if st, ok := existing.(*StructType); ok && st.Builtin && isBuiltinRuntimeStructName(n.Name) {
					continue
				}
				a.errorf(n.Pos(), "duplicate type %q", n.Name)
				continue
			}
			st := &StructType{
				Name:       n.Name,
				TypeParams: append([]string(nil), n.TypeParams...),
				Fields:     map[string]Field{},
				ReprC:      n.ReprC,
				Decl:       n,
			}
			a.namedTypes[n.Name] = st
		case *ast.EnumDecl:
			if _, exists := a.namedTypes[n.Name]; exists {
				a.errorf(n.Pos(), "duplicate type %q", n.Name)
				continue
			}
			enumType := &EnumType{Name: n.Name, Packed: n.Packed, Common: map[string]Field{}, VariantMap: map[string]*EnumVariant{}, Decl: n}
			a.namedTypes[n.Name] = enumType
			if n.Packed {
				storeName := packedEnumStoreTypeName(n.Name)
				if _, exists := a.namedTypes[storeName]; exists {
					a.errorf(n.Pos(), "duplicate type %q", storeName)
					continue
				}
				storeType := &PackedEnumStoreType{Name: storeName, Enum: enumType}
				enumType.StoreType = storeType
				a.namedTypes[storeName] = storeType
			}
		case *ast.ExternTypeDecl:
			if _, exists := a.namedTypes[n.Name]; exists {
				a.errorf(n.Pos(), "duplicate type %q", n.Name)
				continue
			}
			a.namedTypes[n.Name] = &OpaqueType{Name: n.Name}
		case *ast.ErrorDecl:
			if _, exists := a.namedTypes[n.Name]; exists {
				a.errorf(n.Pos(), "duplicate type %q", n.Name)
				continue
			}
			seenTags := map[string]bool{}
			resolvedTags := make([]string, 0, len(n.Tags))
			for _, tag := range n.Tags {
				if seenTags[tag] {
					a.errorf(n.Pos(), "duplicate error tag %q in error set %q", tag, n.Name)
					continue
				}
				seenTags[tag] = true
				resolvedTags = append(resolvedTags, QualifyErrorTag(n.Name, tag))
			}
			a.namedTypes[n.Name] = &ErrorSetType{Name: n.Name, Tags: resolvedTags}
		case *ast.PermissionDecl:
			continue
		case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			continue
		}
	}
}

func packedEnumStoreTypeName(enumName string) string {
	return enumName + ".Store"
}

func (a *Analyzer) populateEnumVariants(decls []ast.Decl) {
	for _, decl := range decls {
		enumDecl, ok := decl.(*ast.EnumDecl)
		if !ok {
			continue
		}
		enumType, _ := a.namedTypes[enumDecl.Name].(*EnumType)
		if enumType == nil {
			continue
		}
		if len(enumDecl.Common) > 0 && !enumDecl.Packed {
			a.errorf(enumDecl.Pos(), "enum %q only supports common: fields for packed enums", enumDecl.Name)
		}
		for _, commonDecl := range enumDecl.Common {
			if commonDecl.Mutable {
				a.errorf(commonDecl.Position, "packed enum %q common field %q cannot be mutable in v1", enumDecl.Name, commonDecl.Name)
			}
			if commonDecl.IsTail {
				a.errorf(commonDecl.Position, "packed enum %q common field %q cannot be tail-allocated", enumDecl.Name, commonDecl.Name)
			}
			if _, exists := enumType.Common[commonDecl.Name]; exists {
				a.errorf(commonDecl.Position, "duplicate common field %q in enum %q", commonDecl.Name, enumDecl.Name)
				continue
			}
			enumType.Common[commonDecl.Name] = Field{Name: commonDecl.Name, Type: a.resolveType(commonDecl.Type), Mutable: false}
		}
		variants := make([]*EnumVariant, 0, len(enumDecl.Variants))
		for i := range enumDecl.Variants {
			variantDecl := &enumDecl.Variants[i]
			if _, exists := enumType.VariantMap[variantDecl.Name]; exists {
				a.errorf(variantDecl.Position, "duplicate variant %q in enum %q", variantDecl.Name, enumDecl.Name)
				continue
			}
			payload := make([]Type, 0, len(variantDecl.Payload))
			payloadNames := make([]string, 0, len(variantDecl.Payload))
			seenPayloadNames := map[string]bool{}
			hasNamedPayloads := false
			hasUnnamedPayloads := false
			for _, payloadDecl := range variantDecl.Payload {
				if payloadDecl.Name != "" {
					hasNamedPayloads = true
					if seenPayloadNames[payloadDecl.Name] {
						a.errorf(payloadDecl.Position, "duplicate payload field %q in enum variant %q.%q", payloadDecl.Name, enumDecl.Name, variantDecl.Name)
					}
					seenPayloadNames[payloadDecl.Name] = true
				} else {
					hasUnnamedPayloads = true
				}
				payloadType := a.resolveType(payloadDecl.Type)
				if !enumDecl.Packed && SameType(payloadType, enumType) {
					a.errorf(payloadDecl.Type.Pos(), "enum %q variant %q cannot contain %q by value; use a reference type instead", enumDecl.Name, variantDecl.Name, enumDecl.Name)
				}
				payload = append(payload, payloadType)
				payloadNames = append(payloadNames, payloadDecl.Name)
			}
			if hasNamedPayloads && hasUnnamedPayloads {
				a.errorf(variantDecl.Position, "enum variant %q.%q must name either all payload fields or none", enumDecl.Name, variantDecl.Name)
			}
			variant := &EnumVariant{Name: variantDecl.Name, Tag: uint32(i), Payload: payload, PayloadNames: payloadNames, Decl: variantDecl}
			enumType.VariantMap[variant.Name] = variant
			variants = append(variants, variant)
		}
		enumType.Variants = variants
	}
}

func (a *Analyzer) populateStructFields(decls []ast.Decl) {
	for _, decl := range decls {
		stDecl, ok := decl.(*ast.StructDecl)
		if !ok {
			continue
		}
		st, _ := a.namedTypes[stDecl.Name].(*StructType)
		if st == nil {
			continue
		}
		if st.Builtin && isBuiltinRuntimeStructName(stDecl.Name) {
			continue
		}
		a.withTypeParams(stDecl.TypeParams, nil, func() {
			for _, field := range stDecl.Fields {
				if _, exists := st.Fields[field.Name]; exists {
					a.errorf(field.Position, "duplicate field %q in struct %q", field.Name, stDecl.Name)
					continue
				}
				fieldType := a.resolveType(field.Type)
				if field.IsTail {
					fieldType = &RefType{Elem: fieldType, State: RefStateNonNull, Storage: RefStorageAny}
				}
				st.Fields[field.Name] = Field{
					Name:    field.Name,
					Type:    fieldType,
					Mutable: field.Mutable,
					IsTail:  field.IsTail,
				}
			}
		})
	}
}

func (a *Analyzer) collectValueSymbols(decls []ast.Decl) {
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.ConstDecl:
			var declType Type = invalidType
			if n.Type != nil {
				declType = a.resolveType(n.Type)
			} else {
				declType = a.inferLiteralType(n.Value)
			}
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolConst, Type: declType, Node: n, Mutable: false}, n.Pos())
		case *ast.GlobalDecl:
			declType := a.resolveType(n.Type)
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolGlobal, Type: declType, Node: n, Mutable: n.Mutable}, n.Pos())
		case *ast.FuncDecl:
			fnType := a.funcTypeFromDecl(n.Name, n.TypeParams, n.RegionParams, n.Permissions, n.Params, n.ReturnType, false)
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternFuncDecl:
			fnType := a.funcTypeFromDecl(n.Name, nil, n.RegionParams, n.Permissions, n.Params, n.ReturnType, n.Variadic)
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolExternFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternVarDecl:
			declType := a.resolveType(n.Type)
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolExternVar, Type: declType, Node: n, Mutable: true}, n.Pos())
		case *ast.EnumDecl:
			continue
		case *ast.ErrorDecl:
		case *ast.PermissionDecl:
			continue
		case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			continue
		}
	}
}

func (a *Analyzer) analyzeDecls(decls []ast.Decl) {
	for _, decl := range decls {
		switch n := decl.(type) {
		case *ast.ConstDecl:
			if sym, ok := a.globalScope.Lookup(n.Name); ok {
				valueType := a.analyzeExprInScope(n.Value, a.globalScope)
				if !AssignableTo(sym.Type, valueType) {
					a.errorf(n.Pos(), "const %q expects %s, got %s", n.Name, sym.Type.String(), valueType.String())
				}
			}
		case *ast.GlobalDecl:
			if n.Value != nil {
				if sym, ok := a.globalScope.Lookup(n.Name); ok {
					valueType := a.analyzeExprInScope(n.Value, a.globalScope)
					if !AssignableTo(sym.Type, valueType) {
						a.errorf(n.Pos(), "global %q expects %s, got %s", n.Name, sym.Type.String(), valueType.String())
					}
				}
			}
		case *ast.FuncDecl:
			a.analyzeFunctionAnnotations(n)
			a.analyzeFunc(n)
		case *ast.EnumDecl:
			continue
		case *ast.ErrorDecl:
		case *ast.PermissionDecl:
			continue
		case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			continue
		}
	}
}

func (a *Analyzer) analyzeFunctionAnnotations(fn *ast.FuncDecl) {
	if len(fn.Annotations) == 0 {
		return
	}

	valid := make([]ast.Annotation, 0, len(fn.Annotations))
	seen := make(map[string]lexer.Pos, len(fn.Annotations))
	for _, annotation := range fn.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on function %q (first seen at %s:%d:%d)", annotation.Name, fn.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedFunctionAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown function annotation @%s on %q", annotation.Name, fn.Name)
			continue
		}
		valid = append(valid, annotation)
	}

	if len(valid) == 0 {
		return
	}

	var signature *FuncType
	if sym, ok := a.globalScope.Lookup(fn.Name); ok {
		signature, _ = sym.Type.(*FuncType)
	}
	accepted := make([]ast.Annotation, 0, len(valid))
	for _, annotation := range valid {
		if a.validateFunctionAnnotation(annotation, fn, signature) {
			accepted = append(accepted, annotation)
		}
	}
	if len(accepted) == 0 {
		return
	}
	a.annotatedFuncs = append(a.annotatedFuncs, &AnnotatedFunc{
		Name:        fn.Name,
		Annotations: accepted,
		Signature:   signature,
		Decl:        fn,
	})
}

func (a *Analyzer) validateFunctionAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if signature == nil {
		a.errorf(annotation.Position, "cannot resolve signature for @%s function %q", annotation.Name, fn.Name)
		return false
	}
	if len(signature.TypeParams) > 0 || len(signature.RegionParams) > 0 || len(signature.ShapeParams) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not have type or shape parameters; got %s", annotation.Name, fn.Name, signature.String())
		return false
	}
	if len(signature.Permissions) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not require permissions; got %s", annotation.Name, fn.Name, signature.String())
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@%s function %q must not be variadic", annotation.Name, fn.Name)
		return false
	}
	if len(signature.Params) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not take parameters; got %s", annotation.Name, fn.Name, signature.String())
		return false
	}
	switch annotation.Name {
	case "test", "bench":
		if !isVoidType(signature.Return) {
			a.errorf(annotation.Position, "@%s function %q must return void, got %s", annotation.Name, fn.Name, signature.Return.String())
			return false
		}
	}
	return true
}

func isSupportedFunctionAnnotation(name string) bool {
	switch name {
	case "test", "bench", "fixture":
		return true
	default:
		return false
	}
}

func isVoidType(t Type) bool {
	builtin, ok := t.(*BuiltinType)
	return ok && builtin.Name == "void"
}

func (a *Analyzer) analyzeFunc(fn *ast.FuncDecl) {
	sym, _ := a.globalScope.Lookup(fn.Name)
	fnType, _ := sym.Type.(*FuncType)
	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedPackedStores := a.currentPackedStores
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	a.currentScope = NewScope(a.globalScope)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	if fnType != nil {
		a.currentReturn = fnType.Return
		a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	}
	a.withTypeParams(fn.TypeParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withShapeParams(fnType.ShapeParams, func() {
				for i, param := range fn.Params {
					var ptype Type = invalidType
					if fnType != nil && i < len(fnType.Params) {
						ptype = fnType.Params[i]
					}
					a.defineLocal(&Symbol{Name: param.Name, Kind: SymbolParam, Type: ptype, Node: fn, Mutable: a.paramIsMutable(param)}, param.Position)
				}
				for _, stmt := range fn.Body {
					a.analyzeStmt(stmt)
				}
			})
		})
	})
	if fnType != nil {
		fnType.FreshReturnShapeParams = mergeShapeParamNames(fnType.FreshReturnShapeParams, inferredFreshReturnShapeParams(a.returnFreshShapeStatus))
		inferredRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
		inferredPermissions := permissionFamiliesFromRefs(inferredRefs)
		fnType.PermissionRefs = mergePermissionRefs(fnType.DeclaredPermissionRefs, inferredRefs)
		fnType.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, inferredPermissions)
	}
	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentPackedStores = savedPackedStores
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
}
