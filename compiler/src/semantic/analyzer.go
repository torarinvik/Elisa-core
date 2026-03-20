package semantic

import (
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"strconv"
	"strings"
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
	"resize":                    {FreshReturnShapeParams: []string{"shape_out"}},
	"push":                      {FreshReturnShapeParams: []string{"shape_out"}},
	"append_many":               {FreshReturnShapeParams: []string{"shape_out"}},
	"truncate":                  {FreshReturnShapeParams: []string{"shape_out"}},
	"clear":                     {FreshReturnShapeParams: []string{"shape_out"}},
	"concat":                    {FreshReturnShapeParams: []string{"shape_result"}},
	"strcat":                    {FreshReturnShapeParams: []string{"shape_result"}},
	"arena_da_append":           {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_append_many":      {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_concat2":                {FreshReturnShapeParams: []string{"shape_result"}},
	"rt_concat2_scratch":        {FreshReturnShapeParams: []string{"shape_result"}},
	"rt_string_builder_finish":  {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_int_to_string":          {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_int_to_string_scratch":  {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_bool_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_bool_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_char_to_string":         {FreshReturnShapeParams: []string{"shape_out"}},
	"rt_char_to_string_scratch": {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_string_slice":          {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_string_from_view":      {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_from_view":        {FreshReturnShapeParams: []string{"shape_out"}},
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
	exprFacts                         map[ast.Expr]OptimizationFacts
	symbolFacts                       map[*Symbol]OptimizationFacts
	typeParamScopes                   []map[string]Type
	shapeParamScopes                  []map[string]Shape
	regionParamScopes                 []map[string]bool
	permissionParamScopes             []map[string]bool
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
	currentAffineValues               map[affineValueKey]affineValueState
	currentPackedStores               map[string]*PackedEnumStoreType
	currentFunctionUsedPermissions    map[string]bool
	currentFunctionUsedPermissionRefs []ast.PermissionRef
	currentReturnProvenance           regionRefState
	suppressDiagnostics               bool
	returnProvenanceInProgress        map[string]bool
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

type regionDependencyState struct {
	Generation    int
	Valid         bool
	InvalidatedBy string
}

type packedStoreDependencyState struct {
	Type *PackedEnumStoreType
}

type regionRefState struct {
	Deps      map[*Symbol]regionDependencyState
	StoreDeps map[*Symbol]packedStoreDependencyState
	ParamDeps map[int]bool
	Fields    map[string]regionRefState
}

type affineValueState struct {
	ConsumedBy       string
	LiveProtocolType Type
}

type affineValueKey struct {
	Root *Symbol
	Path string
}

func Analyze(file *ast.File) *Result {
	a := &Analyzer{
		file:                       file,
		namedTypes:                 map[string]Type{},
		permissions:                map[string]*PermissionSet{},
		globalScope:                NewScope(nil),
		functionTypes:              map[string]*FuncType{},
		constValues:                map[string]ConstValue{},
		exprTypes:                  map[ast.Expr]Type{},
		exprFacts:                  map[ast.Expr]OptimizationFacts{},
		symbolFacts:                map[*Symbol]OptimizationFacts{},
		returnProvenanceInProgress: map[string]bool{},
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
		ExprFacts:       a.exprFacts,
		AnnotatedFuncs:  a.annotatedFuncs,
		ExportedTypes:   a.exportedTypes,
		ExportedFuncs:   a.exportedFuncs,
		ExportedGlobals: a.exportedGlobals,
		Diagnostics:     a.diagnostics,
	}
}

func (a *Analyzer) registerBuiltins() {
	for _, name := range []string{"void", "bool", "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr", "Local", "Frozen", "Joinable", "Pending", "Held"} {
		a.namedTypes[name] = &BuiltinType{Name: name}
	}
	a.registerBuiltinPermissions()
	a.registerBuiltinRuntimeStructs()
}

func (a *Analyzer) registerBuiltinPermissions() {
	a.registerBuiltinPermission("Memory", []string{"Allocate", "Release"})
	a.registerBuiltinPermission("Console", []string{"Format", "Write"})
	a.registerBuiltinPermission("Abort", []string{"Exit", "Panic"})
	a.registerBuiltinPermission("Thread", []string{"Spawn", "Join", "Detach"})
	a.registerBuiltinPermission("Pool", []string{"Create", "Submit", "Await", "WaitAll", "Shutdown"})
	a.registerBuiltinPermission("Sync", []string{"Lock", "Unlock", "Wait", "Notify"})
	a.registerBuiltinPermission("Atomics", []string{"Load", "Store", "Exchange", "CompareExchange", "Rmw", "Fence"})
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
	a.registerBuiltinStructType("Thread", []string{"T", "S"}, true, []builtinFieldSpec{
		{name: "handle", typ: namedTypeExpr("uintptr", false), mutable: true},
		{name: "state", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("Task", []string{"T", "S"}, true, []builtinFieldSpec{
		{name: "handle", typ: namedTypeExpr("uintptr", false), mutable: true},
		{name: "state", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("ThreadPool", nil, false, []builtinFieldSpec{
		{name: "handle", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("TaskGroup", nil, false, []builtinFieldSpec{
		{name: "handle", typ: refTypeExpr("void", true), mutable: true},
		{name: "cleanup", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("Mutex", nil, false, []builtinFieldSpec{
		{name: "handle", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("MutexGuard", []string{"S"}, true, []builtinFieldSpec{
		{name: "handle", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("CondVar", nil, false, []builtinFieldSpec{
		{name: "handle", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("atomic", []string{"T"}, false, []builtinFieldSpec{
		{name: "value", typ: namedTypeExpr("T", false), mutable: true},
	})
	a.registerBuiltinStructType("Region", nil, false, []builtinFieldSpec{
		{name: "next", typ: heapRefTypeExpr("Region", true), mutable: true},
		{name: "count", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "capacity", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "owner_tag", typ: namedTypeExpr("uintptr", false), mutable: true},
		{name: "owner_next", typ: heapRefTypeExpr("Region", true), mutable: true},
		{name: "global_index", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "data", typ: namedTypeExpr("uintptr", false), isTail: true},
	})
	a.registerBuiltinStructType("Arena", nil, false, []builtinFieldSpec{
		{name: "begin", typ: heapRefTypeExpr("Region", true), mutable: true},
		{name: "end", typ: heapRefTypeExpr("Region", true), mutable: true},
		{name: "end_index", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("ArenaMark", nil, false, []builtinFieldSpec{
		{name: "region", typ: heapRefTypeExpr("Region", true), mutable: true},
		{name: "count", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("PackedStoreAllocResult", nil, false, []builtinFieldSpec{
		{name: "row", typ: heapRefTypeExpr("void", true), mutable: true},
		{name: "handle", typ: namedTypeExpr("uintptr", false), mutable: true},
	})
	a.registerBuiltinStructType("StringView", nil, false, []builtinFieldSpec{
		{name: "data", typ: refTypeExpr("u8", false), mutable: true},
		{name: "len", typ: namedTypeExpr("i64", false), mutable: true},
	})
	a.registerBuiltinStructType("DynArray", []string{"T"}, false, []builtinFieldSpec{
		{name: "items", typ: refTypeParamExpr("T", true), mutable: true},
		{name: "count", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "capacity", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("DynArrayView", nil, false, []builtinFieldSpec{
		{name: "data", typ: refTypeExpr("void", true), mutable: true},
		{name: "len", typ: namedTypeExpr("usize", false), mutable: true},
		{name: "elem_size", typ: namedTypeExpr("usize", false), mutable: true},
	})
	a.registerBuiltinStructType("DictBucket", []string{"T"}, false, []builtinFieldSpec{
		{name: "state", typ: namedTypeExpr("u8", false), mutable: true},
		{name: "hash", typ: namedTypeExpr("u64", false), mutable: true},
		{name: "key_data", typ: refTypeExpr("u8", true), mutable: true},
		{name: "key_len", typ: namedTypeExpr("i64", false), mutable: true},
		{name: "value", typ: namedTypeExpr("T", false), mutable: true},
	})
	a.registerBuiltinStructType("DynDict", []string{"T"}, false, []builtinFieldSpec{
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

func (a *Analyzer) registerBuiltinStructType(name string, typeParams []string, affine bool, fields []builtinFieldSpec) {
	declFields := make([]ast.FieldDecl, 0, len(fields))
	semanticFields := make(map[string]Field, len(fields))
	decl := &ast.StructDecl{Position: lexer.Pos{}, Name: name, TypeParams: append([]string(nil), typeParams...), ReprC: true, Affine: affine}
	st := &StructType{
		Name:       name,
		TypeParams: append([]string(nil), typeParams...),
		Fields:     semanticFields,
		Affine:     affine,
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

func refToTypeExprWithStorage(elem ast.TypeExpr, nullable bool, storage ast.RefStorage) ast.TypeExpr {
	state := ast.RefStateNonNull
	if nullable {
		state = ast.RefStateNullable
	}
	return &ast.RefType{Position: lexer.Pos{}, Elem: elem, State: state, Storage: storage}
}

func refToTypeExpr(elem ast.TypeExpr, nullable bool) ast.TypeExpr {
	return refToTypeExprWithStorage(elem, nullable, ast.RefStorageAny)
}

func refTypeExpr(name string, nullable bool) ast.TypeExpr {
	return refToTypeExpr(&ast.NamedType{Position: lexer.Pos{}, Name: name}, nullable)
}

func heapRefTypeExpr(name string, nullable bool) ast.TypeExpr {
	return refToTypeExprWithStorage(&ast.NamedType{Position: lexer.Pos{}, Name: name}, nullable, ast.RefStorageHeap)
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
	case "Thread", "Task", "ThreadPool", "TaskGroup", "Mutex", "MutexGuard", "CondVar", "atomic":
		return true
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
				Affine:     n.Affine,
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
			commonType := a.resolveType(commonDecl.Type)
			if enumDecl.Packed && a.containsAffineHandleValues(commonType, map[string]bool{}) {
				a.errorf(commonDecl.Position, "packed enum %q common field %q cannot contain affine payload type %s", enumDecl.Name, commonDecl.Name, commonType.String())
			}
			enumType.Common[commonDecl.Name] = Field{Name: commonDecl.Name, Type: commonType, Mutable: false}
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
				if enumDecl.Packed && a.containsAffineHandleValues(payloadType, map[string]bool{}) {
					a.errorf(payloadDecl.Type.Pos(), "packed enum %q variant %q cannot contain affine payload type %s", enumDecl.Name, variantDecl.Name, payloadType.String())
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
			if a.containsAffineHandleValues(declType, map[string]bool{}) {
				a.errorf(n.Pos(), "global %q cannot store affine handle values of type %s", n.Name, declType.String())
			}
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolGlobal, Type: declType, Node: n, Mutable: n.Mutable}, n.Pos())
		case *ast.FuncDecl:
			fnType := a.funcTypeFromDecl(n.Name, n.TypeParams, n.RegionParams, n.PermissionParams, n.Permissions, n.Params, n.ReturnType, false)
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternFuncDecl:
			fnType := a.funcTypeFromDecl(n.Name, nil, n.RegionParams, nil, n.Permissions, n.Params, n.ReturnType, n.Variadic)
			a.applyExternFuncAnnotations(n, fnType)
			if !fnType.ReturnProvenanceKnown {
				fnType.ReturnProvenanceKnown = true
			}
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolExternFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternVarDecl:
			declType := a.resolveType(n.Type)
			if a.containsAffineHandleValues(declType, map[string]bool{}) {
				a.errorf(n.Pos(), "extern var %q cannot store affine handle values of type %s", n.Name, declType.String())
			}
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

func isSupportedExternFunctionAnnotation(name string) bool {
	switch name {
	case "borrows_return", "borrows_return_field", "borrows_return_rebased", "borrows_return_field_rebased":
		return true
	default:
		return false
	}
}

func (a *Analyzer) applyExternFuncAnnotations(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || len(fn.Annotations) == 0 {
		return
	}
	seen := make(map[string]lexer.Pos, len(fn.Annotations))
	for _, annotation := range fn.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on extern function %q (first seen at %s:%d:%d)", annotation.Name, fn.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedExternFunctionAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown extern function annotation @%s on %q", annotation.Name, fn.Name)
			continue
		}
		switch annotation.Name {
		case "borrows_return":
			a.applyExternBorrowsReturnAnnotation(fn, fnType, annotation)
		case "borrows_return_field":
			a.applyExternBorrowsReturnFieldAnnotation(fn, fnType, annotation)
		case "borrows_return_rebased":
			a.applyExternBorrowsReturnRebasedAnnotation(fn, fnType, annotation)
		case "borrows_return_field_rebased":
			a.applyExternBorrowsReturnFieldRebasedAnnotation(fn, fnType, annotation)
		}
	}
}

func (a *Analyzer) applyExternBorrowsReturnAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@borrows_return on extern function %q expects at least one parameter name", fn.Name)
		return
	}
	var states []regionRefState
	for _, pathText := range annotation.Args {
		state, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, false, "")
		if !ok {
			continue
		}
		states = append(states, state)
	}
	if merged, ok := mergeRegionRefStates(states...); ok {
		fnType.ReturnProvenance = merged
	}
	fnType.ReturnProvenanceKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnFieldAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 || len(annotation.Args)%2 != 0 {
		a.errorf(annotation.Position, "@borrows_return_field on extern function %q expects field/path pairs", fn.Name)
		return
	}
	if _, ok := a.resolvedStructFields(fnType.Return); !ok {
		a.errorf(annotation.Position, "@borrows_return_field on extern function %q requires a concrete struct return type, got %s", fn.Name, fnType.Return.String())
		return
	}
	for i := 0; i < len(annotation.Args); i += 2 {
		returnFieldPath := annotation.Args[i]
		pathText := annotation.Args[i+1]
		returnSteps, ok := a.resolveExternReturnTargetPath(fn, fnType, annotation, returnFieldPath)
		if !ok {
			continue
		}
		state, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, false, returnFieldPath)
		if !ok {
			continue
		}
		fnType.ReturnProvenance = assignRegionRefStateAtPath(fnType.ReturnProvenance, returnSteps, state)
	}
	if !hasRegionProvenance(fnType.ReturnProvenance) {
		fnType.ReturnProvenance = regionRefState{}
	}
	fnType.ReturnProvenanceKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnRebasedAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@borrows_return_rebased on extern function %q expects at least one parameter path", fn.Name)
		return
	}
	var states []regionRefState
	for _, pathText := range annotation.Args {
		state, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, true, "")
		if !ok {
			continue
		}
		states = append(states, state)
	}
	if merged, ok := mergeRegionRefStates(states...); ok {
		fnType.ReturnProvenance = merged
	}
	fnType.ReturnProvenanceKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnFieldRebasedAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 || len(annotation.Args)%2 != 0 {
		a.errorf(annotation.Position, "@borrows_return_field_rebased on extern function %q expects field/path pairs", fn.Name)
		return
	}
	if _, ok := a.resolvedStructFields(fnType.Return); !ok {
		a.errorf(annotation.Position, "@borrows_return_field_rebased on extern function %q requires a concrete struct return type, got %s", fn.Name, fnType.Return.String())
		return
	}
	for i := 0; i < len(annotation.Args); i += 2 {
		returnFieldPath := annotation.Args[i]
		pathText := annotation.Args[i+1]
		returnSteps, ok := a.resolveExternReturnTargetPath(fn, fnType, annotation, returnFieldPath)
		if !ok {
			continue
		}
		state, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, true, returnFieldPath)
		if !ok {
			continue
		}
		fnType.ReturnProvenance = assignRegionRefStateAtPath(fnType.ReturnProvenance, returnSteps, state)
	}
	if !hasRegionProvenance(fnType.ReturnProvenance) {
		fnType.ReturnProvenance = regionRefState{}
	}
	fnType.ReturnProvenanceKnown = true
}

type borrowReturnAnnotationStep struct {
	Field    string
	Index    *int64
	Wildcard bool
}

func parseBorrowReturnAnnotationPath(text string) (string, []borrowReturnAnnotationStep, bool) {
	if text == "" {
		return "", nil, false
	}
	rootEnd := strings.IndexAny(text, ".[")
	if rootEnd < 0 {
		return text, nil, true
	}
	root := text[:rootEnd]
	if root == "" {
		return "", nil, false
	}
	rest := text[rootEnd:]
	steps := make([]borrowReturnAnnotationStep, 0, 2)
	for len(rest) != 0 {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			next := strings.IndexAny(rest, ".[")
			field := rest
			if next >= 0 {
				field = rest[:next]
				rest = rest[next:]
			} else {
				rest = ""
			}
			if field == "" {
				return "", nil, false
			}
			steps = append(steps, borrowReturnAnnotationStep{Field: field})
		case '[':
			end := strings.IndexByte(rest, ']')
			if end <= 1 {
				return "", nil, false
			}
			token := rest[1:end]
			rest = rest[end+1:]
			if token == "*" {
				steps = append(steps, borrowReturnAnnotationStep{Wildcard: true})
				continue
			}
			value, err := strconv.ParseInt(token, 10, 64)
			if err != nil {
				return "", nil, false
			}
			valueCopy := value
			steps = append(steps, borrowReturnAnnotationStep{Index: &valueCopy})
		default:
			return "", nil, false
		}
	}
	return root, steps, true
}

func projectBorrowReturnAnnotationState(state regionRefState, steps []borrowReturnAnnotationStep) (regionRefState, bool) {
	current := state
	ok := true
	for _, step := range steps {
		switch {
		case step.Field != "":
			current, ok = projectRegionFieldState(current, step.Field)
		case step.Wildcard:
			current, ok = projectRegionIndexKeyState(current, regionAnyIndexFieldKey())
		case step.Index != nil:
			current, ok = projectRegionIndexKeyState(current, regionIndexFieldKey(*step.Index))
		default:
			return regionRefState{}, false
		}
		if !ok {
			return regionRefState{}, false
		}
	}
	return current, true
}

func parseExternReturnTargetPath(text string) ([]borrowReturnAnnotationStep, bool) {
	if text == "" {
		return nil, false
	}
	_, steps, ok := parseBorrowReturnAnnotationPath("ret." + text)
	if !ok || len(steps) == 0 {
		return nil, false
	}
	return steps, true
}

func assignRegionRefStateAtPath(dst regionRefState, steps []borrowReturnAnnotationStep, value regionRefState) regionRefState {
	if len(steps) == 0 {
		if merged, ok := mergeRegionRefStates(dst, value); ok {
			return merged
		}
		return value
	}
	if dst.Fields == nil {
		dst.Fields = map[string]regionRefState{}
	}
	key := regionFieldKeyForBorrowStep(steps[0])
	child := dst.Fields[key]
	dst.Fields[key] = assignRegionRefStateAtPath(child, steps[1:], value)
	return dst
}

func regionFieldKeyForBorrowStep(step borrowReturnAnnotationStep) string {
	switch {
	case step.Field != "":
		return step.Field
	case step.Wildcard:
		return regionAnyIndexFieldKey()
	case step.Index != nil:
		return regionIndexFieldKey(*step.Index)
	default:
		return ""
	}
}

func (a *Analyzer) resolveExternReturnTargetPath(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation, pathText string) ([]borrowReturnAnnotationStep, bool) {
	steps, ok := parseExternReturnTargetPath(pathText)
	if !ok {
		a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, pathText, "has invalid return field path %q", pathText)
		return nil, false
	}
	current := fnType.Return
	for _, step := range steps {
		next, ok := a.projectExternReturnTargetType(current, step)
		if !ok {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, pathText, "references unknown return field path %q in %s", pathText, fnType.Return.String())
			return nil, false
		}
		current = next
	}
	return steps, true
}

func (a *Analyzer) projectExternReturnTargetType(current Type, step borrowReturnAnnotationStep) (Type, bool) {
	switch {
	case step.Field != "":
		fields, ok := a.resolvedStructFields(current)
		if !ok {
			return nil, false
		}
		for _, field := range fields {
			if field.Name == step.Field {
				return field.Type, true
			}
		}
		return nil, false
	case step.Wildcard || step.Index != nil:
		switch tt := current.(type) {
		case *ArrayType:
			return tt.Elem, true
		case *DArrayType:
			return tt.Elem, true
		case *ViewType:
			return tt.Elem, true
		case *DArrayViewType:
			return tt.Elem, true
		default:
			return nil, false
		}
	default:
		return nil, false
	}
}

func (a *Analyzer) resolveExternBorrowAnnotationPath(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation, pathText string, rebased bool, returnField string) (regionRefState, bool) {
	name, steps, ok := parseBorrowReturnAnnotationPath(pathText)
	if !ok {
		a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "has invalid path %q", pathText)
		return regionRefState{}, false
	}
	index := -1
	for i, param := range fn.Params {
		if param.Name == name {
			index = i
			break
		}
	}
	if index < 0 {
		a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "references unknown parameter %q", name)
		return regionRefState{}, false
	}
	if index >= len(fnType.Params) {
		return regionRefState{}, false
	}
	state, ok := a.abstractParamRegionRefState(fnType.Params[index], index, map[string]bool{})
	if !ok {
		if returnField == "" {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot borrow from parameter %q of type %s", name, fnType.Params[index].String())
		} else {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot borrow field %q from parameter %q of type %s", returnField, name, fnType.Params[index].String())
		}
		return regionRefState{}, false
	}
	if len(steps) != 0 {
		state, ok = projectBorrowReturnAnnotationState(state, steps)
		if !ok {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot project path %q from parameter %q of type %s", pathText, name, fnType.Params[index].String())
			return regionRefState{}, false
		}
	}
	if rebased {
		state, ok = summarizeRegionIndexStates(state)
		if !ok {
			return regionRefState{}, false
		}
	}
	return state, true
}

func (a *Analyzer) errorExternBorrowAnnotationPathError(fn *ast.ExternFuncDecl, annotation ast.Annotation, pathText string, returnField string, format string, args ...interface{}) {
	switch annotation.Name {
	case "borrows_return":
		a.errorf(annotation.Position, "@borrows_return on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	case "borrows_return_field":
		a.errorf(annotation.Position, "@borrows_return_field on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	case "borrows_return_rebased":
		a.errorf(annotation.Position, "@borrows_return_rebased on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	case "borrows_return_field_rebased":
		a.errorf(annotation.Position, "@borrows_return_field_rebased on extern function %q "+format, append([]interface{}{fn.Name}, args...)...)
	default:
		a.errorf(annotation.Position, "@%s on extern function %q "+format, append([]interface{}{annotation.Name, fn.Name}, args...)...)
	}
}

func (a *Analyzer) containsAffineHandleValues(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if isAffineHandleType(t) {
		return true
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.containsAffineHandleValues(tt.Elem, seen)
	case *DArrayType:
		return a.containsAffineHandleValues(tt.Elem, seen)
	case *DictType:
		return a.containsAffineHandleValues(tt.Key, seen) || a.containsAffineHandleValues(tt.Value, seen)
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.containsAffineHandleValues(fieldType, seen) {
					return true
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.containsAffineHandleValues(arg, seen) {
				return true
			}
		}
		return a.containsAffineHandleValues(tt.Base, seen)
	case *StructType:
		for _, field := range tt.Fields {
			if a.containsAffineHandleValues(field.Type, seen) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) typeStructurallyAtomicSafe(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if IsNumericType(t) || IsBoolType(t) {
		return true
	}
	if _, ok := t.(*TypeParamType); ok {
		return true
	}
	if isPointerLikeCastType(t) {
		return true
	}
	key := t.String()
	if seen[key] {
		return true
	}
	seen[key] = true
	switch tt := t.(type) {
	case *GenericInstanceType:
		for _, arg := range tt.Args {
			if !a.typeStructurallyAtomicSafe(arg, seen) {
				return false
			}
		}
		return false
	default:
		return false
	}
}

func (a *Analyzer) typeCanContainRegionRefs(t Type, seen map[string]bool) bool {
	if t == nil {
		return false
	}
	if _, ok := t.(*RefType); ok {
		return true
	}
	if _, ok := t.(*PackedEnumStoreType); ok {
		return true
	}
	key := t.String()
	if seen[key] {
		return false
	}
	seen[key] = true
	switch tt := t.(type) {
	case *ArrayType:
		return a.typeCanContainRegionRefs(tt.Elem, seen)
	case *DArrayType:
		return a.typeCanContainRegionRefs(tt.Elem, seen)
	case *ViewType:
		return a.typeCanContainRegionRefs(tt.Elem, seen)
	case *DArrayViewType:
		return a.typeCanContainRegionRefs(tt.Elem, seen)
	case *DictType:
		return a.typeCanContainRegionRefs(tt.Key, seen) || a.typeCanContainRegionRefs(tt.Value, seen)
	case *StructType:
		for _, field := range tt.Fields {
			if a.typeCanContainRegionRefs(field.Type, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		if tt.Packed {
			return true
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if a.typeCanContainRegionRefs(payload, seen) {
					return true
				}
			}
		}
		return false
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				if a.typeCanContainRegionRefs(fieldType, seen) {
					return true
				}
			}
			return false
		}
		if base, ok := tt.Base.(*EnumType); ok {
			for _, variant := range base.Variants {
				for _, payload := range variant.Payload {
					payloadType := a.substituteType(payload, nil, nil, nil, nil)
					if a.typeCanContainRegionRefs(payloadType, seen) {
						return true
					}
				}
			}
			return false
		}
		for _, arg := range tt.Args {
			if a.typeCanContainRegionRefs(arg, seen) {
				return true
			}
		}
		return a.typeCanContainRegionRefs(tt.Base, seen)
	default:
		return false
	}
}

func (a *Analyzer) abstractParamRegionRefState(t Type, paramIndex int, seen map[string]bool) (regionRefState, bool) {
	if t == nil || !a.typeCanContainRegionRefs(t, map[string]bool{}) {
		return regionRefState{}, false
	}
	key := t.String()
	if seen[key] {
		return regionRefStateFromParamDependency(paramIndex), true
	}
	seen[key] = true
	state := regionRefStateFromParamDependency(paramIndex)
	switch tt := t.(type) {
	case *RefType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			if len(elemState.Fields) != 0 {
				state.Fields = cloneRegionRefState(elemState).Fields
			}
		}
		return state, true
	case *StructType:
		for _, field := range tt.Fields {
			fieldState, ok := a.abstractParamRegionRefState(field.Type, paramIndex, seen)
			if !ok {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]regionRefState{}
			}
			state.Fields[field.Name] = fieldState
		}
	case *GenericInstanceType:
		if base, ok := tt.Base.(*StructType); ok {
			bindings := map[string]Type{}
			for i, name := range base.TypeParams {
				if i < len(tt.Args) {
					bindings[name] = tt.Args[i]
				}
			}
			for _, field := range base.Fields {
				fieldType := field.Type
				if len(bindings) != 0 {
					fieldType = a.substituteType(fieldType, bindings, nil, nil, nil)
				}
				fieldState, ok := a.abstractParamRegionRefState(fieldType, paramIndex, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = map[string]regionRefState{}
				}
				state.Fields[field.Name] = fieldState
			}
			return state, true
		}
		if base, ok := tt.Base.(*EnumType); ok {
			for _, variant := range base.Variants {
				for i, payload := range variant.Payload {
					fieldType := a.substituteType(payload, map[string]Type{}, nil, nil, nil)
					fieldState, ok := a.abstractParamRegionRefState(fieldType, paramIndex, seen)
					if !ok {
						continue
					}
					if state.Fields == nil {
						state.Fields = map[string]regionRefState{}
					}
					state.Fields[moveBindVariantFieldKey(variant, i)] = fieldState
				}
			}
			return state, true
		}
	case *EnumType:
		if tt.Packed {
			return state, true
		}
		for _, variant := range tt.Variants {
			for i, payload := range variant.Payload {
				fieldState, ok := a.abstractParamRegionRefState(payload, paramIndex, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = map[string]regionRefState{}
				}
				state.Fields[moveBindVariantFieldKey(variant, i)] = fieldState
			}
		}
	case *ArrayType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	case *DArrayType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	case *ViewType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	case *DArrayViewType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			state.Fields = map[string]regionRefState{
				regionAnyIndexFieldKey(): elemState,
			}
		}
	}
	return state, true
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
	savedAffineValues := a.currentAffineValues
	savedPackedStores := a.currentPackedStores
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedReturnProvenance := a.currentReturnProvenance
	a.currentScope = NewScope(a.globalScope)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentReturnProvenance = regionRefState{}
	if fnType != nil {
		a.currentReturn = fnType.Return
		a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	}
	a.withTypeParams(fn.TypeParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
					for i, param := range fn.Params {
						var ptype Type = invalidType
						if fnType != nil && i < len(fnType.Params) {
							ptype = fnType.Params[i]
						}
						sym := &Symbol{Name: param.Name, Kind: SymbolParam, Type: ptype, Node: fn, Mutable: a.paramIsMutable(param)}
						a.defineLocal(sym, param.Position)
						if state, ok := a.abstractParamRegionRefState(ptype, i, map[string]bool{}); ok {
							a.recordResolvedRegionRefBinding(sym, state)
						}
					}
					for _, stmt := range fn.Body {
						a.analyzeStmt(stmt)
					}
				})
			})
		})
	})
	if fnType != nil {
		if summary, ok := abstractParamOnlyRegionRefState(a.currentReturnProvenance); ok {
			fnType.ReturnProvenance = summary
		} else {
			fnType.ReturnProvenance = regionRefState{}
		}
		fnType.ReturnProvenanceKnown = true
		fnType.FreshReturnShapeParams = mergeShapeParamNames(fnType.FreshReturnShapeParams, inferredFreshReturnShapeParams(a.returnFreshShapeStatus))
		inferredRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
		inferredPermissions := permissionFamiliesFromRefs(inferredRefs)
		fnType.PermissionRefs = mergePermissionRefs(fnType.DeclaredPermissionRefs, inferredRefs)
		fnType.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, inferredPermissions)
	}
	a.reportUnconsumedProtocolValues()
	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
	a.currentPackedStores = savedPackedStores
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentReturnProvenance = savedReturnProvenance
}

func (a *Analyzer) inferFuncReturnProvenance(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || fnType.ReturnProvenanceKnown {
		return
	}
	if a.returnProvenanceInProgress[fn.Name] {
		return
	}
	a.returnProvenanceInProgress[fn.Name] = true
	defer delete(a.returnProvenanceInProgress, fn.Name)

	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedAffineValues := a.currentAffineValues
	savedPackedStores := a.currentPackedStores
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedReturnProvenance := a.currentReturnProvenance
	savedSuppressDiagnostics := a.suppressDiagnostics

	a.currentScope = NewScope(a.globalScope)
	a.currentReturn = fnType.Return
	a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentReturnProvenance = regionRefState{}
	a.suppressDiagnostics = true

	a.withTypeParams(fn.TypeParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
					for i, param := range fn.Params {
						var ptype Type = invalidType
						if i < len(fnType.Params) {
							ptype = fnType.Params[i]
						}
						sym := &Symbol{Name: param.Name, Kind: SymbolParam, Type: ptype, Node: fn, Mutable: a.paramIsMutable(param)}
						a.defineLocal(sym, param.Position)
						if state, ok := a.abstractParamRegionRefState(ptype, i, map[string]bool{}); ok {
							a.recordResolvedRegionRefBinding(sym, state)
						}
					}
					for _, stmt := range fn.Body {
						a.analyzeStmt(stmt)
					}
				})
			})
		})
	})

	if summary, ok := abstractParamOnlyRegionRefState(a.currentReturnProvenance); ok {
		fnType.ReturnProvenance = summary
	} else {
		fnType.ReturnProvenance = regionRefState{}
	}
	fnType.ReturnProvenanceKnown = true

	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
	a.currentPackedStores = savedPackedStores
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentReturnProvenance = savedReturnProvenance
	a.suppressDiagnostics = savedSuppressDiagnostics
}
