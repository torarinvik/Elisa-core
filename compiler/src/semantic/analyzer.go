package semantic

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
	"math/bits"
	"strconv"
	"strings"
	"unicode"
)

type ConstValueKind int

const (
	ConstUnknown ConstValueKind = iota
	ConstInt
	ConstFloat
	ConstBool
	ConstString
)

type ConstValue struct {
	Kind   ConstValueKind
	Int    int64
	Float  float64
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
	resolvedCastHooks                 map[ast.Expr]*Symbol
	exprDenseNodeKeys                 map[ast.Expr]DenseNodeKeyInfo
	exprNodeTables                    map[ast.Expr]NodeTableInfo
	deferInfo                         map[*ast.DeferStmt]*DeferInfo
	symbolFacts                       map[*Symbol]OptimizationFacts
	funcDeclSymbols                   map[*ast.FuncDecl]*Symbol
	castHooksByName                   map[string]map[castHookSignature]*Symbol
	typeParamScopes                   []map[string]Type
	refStorageParamScopes             []map[string]Type
	refStateParamScopes               []map[string]Type
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
	currentBorrowedOwnerRefs          map[*Symbol]borrowedOwnerRefState
	currentFunctionValues             map[*Symbol]*FuncType
	currentSpecializedValueTypes      map[*Symbol]Type
	currentValueBindings              map[*Symbol]ast.Expr
	currentPackedVariantViews         map[*Symbol]*PackedVariantViewType
	currentPackedStores               map[string]*PackedEnumStoreType
	currentPackedStoreResolutions     map[*Symbol]packedStoreResolution
	currentPoolScopes                 []poolScopeState
	currentFunctionUsedPermissions    map[string]bool
	currentFunctionUsedPermissionRefs []ast.PermissionRef
	currentReturnProvenance           regionRefState
	currentReturnBorrowedOwnerRefs    borrowedOwnerRefSummary
	suppressDiagnostics               bool
	returnProvenanceInProgress        map[*ast.FuncDecl]bool
	returnBorrowedOwnerRefInProgress  map[*ast.FuncDecl]bool
	sinkParamInferenceInProgress      map[*ast.FuncDecl]bool
	parallelForInfo                   map[*ast.ParallelForStmt]*ParallelForInfo
	functionAnalyses                  map[*ast.FuncDecl]*FunctionAnalysis
	currentNamespace                  string
	currentUsings                     []string
}

type castHookSignature struct {
	Source string
	Target string
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

type packedStoreResolution struct {
	Symbol *Symbol
	Type   *PackedEnumStoreType
}

type regionRefState struct {
	Deps                    map[*Symbol]regionDependencyState
	StoreDeps               map[*Symbol]packedStoreDependencyState
	DirectParamDep          int
	HasDirectParamDep       bool
	ParamDeps               IntBitSet
	Fields                  map[string]regionRefState
	PackedStoreSummary      PackedStoreProvenance
	PackedStoreSummaryKnown bool
}

type borrowedOwnerRefState struct {
	HasDirect bool
	Direct    affineValueKey
	Fields    map[string]borrowedOwnerRefState
}

type affineValueState struct {
	ConsumedBy              string
	LiveProtocolType        Type
	LiveProtocolDescription string
}

type affineValueKey struct {
	Root *Symbol
	Path string
}

type poolScopeState struct {
	Name string
}

func Analyze(file *ast.File) *Result {
	census := analyzeASTCensus(file)
	exprCapacity := census.exprs
	exprFactsCapacity := census.exprs / 8
	if exprFactsCapacity < 32 {
		exprFactsCapacity = 32
	}
	resolvedCastHookCapacity := census.exprs / 32
	if resolvedCastHookCapacity < 8 {
		resolvedCastHookCapacity = 8
	}
	denseNodeCapacity := census.exprs / 64
	if denseNodeCapacity < 8 {
		denseNodeCapacity = 8
	}
	funcDeclCapacity := census.funcDecls
	if funcDeclCapacity < 8 {
		funcDeclCapacity = 8
	}
	parallelForCapacity := census.parallelFors
	if parallelForCapacity < 4 {
		parallelForCapacity = 4
	}
	a := &Analyzer{
		file:                             file,
		namedTypes:                       map[string]Type{},
		permissions:                      map[string]*PermissionSet{},
		globalScope:                      NewScope(nil),
		functionTypes:                    map[string]*FuncType{},
		constValues:                      map[string]ConstValue{},
		exprTypes:                        make(map[ast.Expr]Type, exprCapacity),
		exprFacts:                        make(map[ast.Expr]OptimizationFacts, exprFactsCapacity),
		resolvedCastHooks:                make(map[ast.Expr]*Symbol, resolvedCastHookCapacity),
		exprDenseNodeKeys:                make(map[ast.Expr]DenseNodeKeyInfo, denseNodeCapacity),
		exprNodeTables:                   make(map[ast.Expr]NodeTableInfo, denseNodeCapacity),
		deferInfo:                        map[*ast.DeferStmt]*DeferInfo{},
		parallelForInfo:                  make(map[*ast.ParallelForStmt]*ParallelForInfo, parallelForCapacity),
		symbolFacts:                      map[*Symbol]OptimizationFacts{},
		funcDeclSymbols:                  make(map[*ast.FuncDecl]*Symbol, funcDeclCapacity),
		functionAnalyses:                 make(map[*ast.FuncDecl]*FunctionAnalysis, funcDeclCapacity),
		castHooksByName:                  map[string]map[castHookSignature]*Symbol{},
		returnProvenanceInProgress:       map[*ast.FuncDecl]bool{},
		returnBorrowedOwnerRefInProgress: map[*ast.FuncDecl]bool{},
		sinkParamInferenceInProgress:     map[*ast.FuncDecl]bool{},
	}
	a.registerBuiltins()
	activeDecls := a.flattenScopedDecls(file.Decls, "", nil)
	a.collectConstValues(activeDecls)
	a.collectPermissionDecls(activeDecls)
	a.collectNamedTypes(activeDecls)
	a.populateConstEnumMembers(activeDecls)
	a.populateStructFields(activeDecls)
	a.populateEnumVariants(activeDecls)
	a.warnOnAvoidableStructPadding(activeDecls)
	a.collectExportTypeAliases(activeDecls)
	a.collectValueSymbols(activeDecls)
	a.analyzeDecls(activeDecls)
	a.inferFunctionPermissionEffects(activeDecls)
	a.warnOnImplicitFunctionPermissions(activeDecls)
	a.validatePermissionUsage(activeDecls)
	a.analyzeExports(activeDecls)
	return &Result{
		File:             file,
		GlobalScope:      a.globalScope,
		NamedTypes:       a.namedTypes,
		ConstValues:      a.constValues,
		ExprTypes:        a.exprTypes,
		ExprFacts:        a.exprFacts,
		CastHooks:        a.resolvedCastHooks,
		DenseNodeKeys:    a.exprDenseNodeKeys,
		NodeTables:       a.exprNodeTables,
		ParallelFor:      a.parallelForInfo,
		Defer:            a.deferInfo,
		FunctionAnalyses: a.functionAnalyses,
		AnnotatedFuncs:   a.annotatedFuncs,
		ExportedTypes:    a.exportedTypes,
		ExportedFuncs:    a.exportedFuncs,
		ExportedGlobals:  a.exportedGlobals,
		Diagnostics:      a.diagnostics,
	}
}

func (a *Analyzer) registerBuiltins() {
	for _, name := range []string{"void", "bool", "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr", "f32", "f64", "Local", "Frozen", "Joinable", "Pending", "Held"} {
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
	a.registerBuiltinStructType("ThreadPool", nil, true, []builtinFieldSpec{
		{name: "handle", typ: refTypeExpr("void", true), mutable: true},
	})
	a.registerBuiltinStructType("TaskGroup", nil, true, []builtinFieldSpec{
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
	a.registerBuiltinStructType("PackedStoreIndexAllocResult", nil, false, []builtinFieldSpec{
		{name: "row", typ: heapRefTypeExpr("void", true), mutable: true},
		{name: "index", typ: namedTypeExpr("u32", false), mutable: true},
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
	a.registerBuiltinStructType("SplitView", []string{"T"}, false, []builtinFieldSpec{
		{name: "left", typ: genericTypeExpr("dview", namedTypeExpr("T", false)), mutable: false},
		{name: "right", typ: genericTypeExpr("dview", namedTypeExpr("T", false)), mutable: false},
	})
	a.registerBuiltinStructType("ChunksExactView", []string{"T"}, false, []builtinFieldSpec{
		{name: "source", typ: genericTypeExpr("dview", namedTypeExpr("T", false)), mutable: false},
		{name: "chunk_size", typ: namedTypeExpr("usize", false), mutable: false},
		{name: "len", typ: namedTypeExpr("usize", false), mutable: false},
	})
	a.registerBuiltinStructType("NodeKey", []string{"T"}, false, []builtinFieldSpec{
		{name: "index", typ: namedTypeExpr("u32", false), mutable: false},
	})
	a.registerBuiltinStructType("NodeTable", []string{"N", "T"}, false, []builtinFieldSpec{
		{name: "values", typ: genericTypeExpr("dview", namedTypeExpr("T", false)), mutable: false},
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
	decl := &ast.StructDecl{Position: lexer.Pos{}, Name: name, TypeParams: append([]string(nil), typeParams...), GenericParams: astTypeGenericParams(typeParams), ReprC: true, Affine: affine}
	st := &StructType{
		Name:          name,
		TypeParams:    append([]string(nil), typeParams...),
		GenericParams: astTypeGenericParams(typeParams),
		Fields:        semanticFields,
		Affine:        affine,
		ReprC:         true,
		Decl:          decl,
		Builtin:       true,
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

func astTypeGenericParams(typeParams []string) []ast.GenericParam {
	if len(typeParams) == 0 {
		return nil
	}
	params := make([]ast.GenericParam, 0, len(typeParams))
	for _, name := range typeParams {
		params = append(params, ast.GenericParam{Position: lexer.Pos{}, Kind: ast.GenericParamType, Name: name})
	}
	return params
}

func isBuiltinRuntimeStructName(name string) bool {
	switch name {
	case "Thread", "Task", "ThreadPool", "TaskGroup", "Mutex", "MutexGuard", "CondVar", "atomic":
		return true
	case "Region", "Arena", "ArenaMark", "StringView", "DynArray", "DynArrayView", "DictBucket", "DynDict":
		return true
	case "SplitView", "ChunksExactView":
		return true
	case "NodeKey", "NodeTable":
		return true
	case "PackedStoreAllocResult", "PackedStoreIndexAllocResult":
		return true
	default:
		return false
	}
}

func (a *Analyzer) collectConstValues(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.ConstDecl:
				if value, ok := a.evalConstExpr(n.Value); ok {
					a.constValues[joinQualifiedName(scoped.Namespace, n.Name)] = value
				}
			case *ast.ConstEnumDecl:
				nextValue := int64(0)
				hasValue := false
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				for i := range n.Members {
					member := &n.Members[i]
					value := nextValue
					if member.Value != nil {
						resolved, ok := a.evalConstExpr(member.Value)
						if !ok || resolved.Kind != ConstInt {
							continue
						}
						value = resolved.Int
					}
					a.constValues[qualifiedName+"."+member.Name] = ConstValue{Kind: ConstInt, Int: value}
					nextValue = value + 1
					hasValue = true
				}
				if !hasValue {
					nextValue = 0
				}
			}
		})
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

func (a *Analyzer) collectNamedTypes(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.StructDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if existing, exists := a.namedTypes[qualifiedName]; exists {
					if st, ok := existing.(*StructType); ok && st.Builtin && isBuiltinRuntimeStructName(n.Name) {
						return
					}
					a.errorf(n.Pos(), "duplicate type %q", qualifiedName)
					return
				}
				st := &StructType{
					Name:             qualifiedName,
					TypeParams:       append([]string(nil), n.TypeParams...),
					RefStorageParams: append([]string(nil), n.RefStorageParams...),
					RefStateParams:   append([]string(nil), n.RefStateParams...),
					GenericParams:    append([]ast.GenericParam(nil), n.GenericParams...),
					Fields:           map[string]Field{},
					Affine:           n.Affine,
					ReprC:            n.ReprC,
					Decl:             n,
				}
				a.namedTypes[qualifiedName] = st
			case *ast.ConstEnumDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "duplicate type %q", qualifiedName)
					return
				}
				a.namedTypes[qualifiedName] = &ConstEnumType{Name: qualifiedName, MemberMap: map[string]*ConstEnumMember{}, Decl: n}
			case *ast.EnumDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "duplicate type %q", qualifiedName)
					return
				}
				enumType := &EnumType{Name: qualifiedName, Packed: n.Packed, Common: map[string]Field{}, VariantMap: map[string]*EnumVariant{}, Decl: n}
				a.namedTypes[qualifiedName] = enumType
				if n.Packed {
					tagName := packedEnumTagTypeName(qualifiedName)
					if _, exists := a.namedTypes[tagName]; exists {
						a.errorf(n.Pos(), "duplicate type %q", tagName)
						return
					}
					tagType := &ConstEnumType{Name: tagName, Storage: a.namedTypes["u32"], MemberMap: map[string]*ConstEnumMember{}}
					enumType.TagType = tagType
					a.namedTypes[tagName] = tagType
					storeName := packedEnumStoreTypeName(qualifiedName)
					if _, exists := a.namedTypes[storeName]; exists {
						a.errorf(n.Pos(), "duplicate type %q", storeName)
						return
					}
					storeType := &PackedEnumStoreType{Name: storeName, Enum: enumType}
					enumType.StoreType = storeType
					a.namedTypes[storeName] = storeType
				}
			case *ast.ExternTypeDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "duplicate type %q", qualifiedName)
					return
				}
				a.namedTypes[qualifiedName] = &OpaqueType{Name: qualifiedName}
			case *ast.ErrorDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				if _, exists := a.namedTypes[qualifiedName]; exists {
					a.errorf(n.Pos(), "duplicate type %q", qualifiedName)
					return
				}
				seenTags := map[string]bool{}
				resolvedTags := make([]string, 0, len(n.Tags))
				for _, tag := range n.Tags {
					if seenTags[tag] {
						a.errorf(n.Pos(), "duplicate error tag %q in error set %q", tag, n.Name)
						continue
					}
					seenTags[tag] = true
					resolvedTags = append(resolvedTags, QualifyErrorTag(qualifiedName, tag))
				}
				a.namedTypes[qualifiedName] = &ErrorSetType{Name: qualifiedName, Tags: resolvedTags}
			case *ast.PermissionDecl:
			case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
	}
}

func (a *Analyzer) populateConstEnumMembers(decls []scopedDecl) {
	for _, scoped := range decls {
		constEnumDecl, ok := scoped.Decl.(*ast.ConstEnumDecl)
		if !ok {
			continue
		}
		constEnumType, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, constEnumDecl.Name)].(*ConstEnumType)
		if constEnumType == nil {
			continue
		}
		storageType := a.resolveType(constEnumDecl.Storage)
		constEnumType.Storage = storageType
		if !IsIntegralStorageType(storageType) {
			a.errorf(constEnumDecl.Storage.Pos(), "const enum %q storage type must be an explicit integer type, got %s", constEnumDecl.Name, storageType.String())
		}
		members := make([]*ConstEnumMember, 0, len(constEnumDecl.Members))
		nextValue := int64(0)
		for i := range constEnumDecl.Members {
			memberDecl := &constEnumDecl.Members[i]
			if _, exists := constEnumType.MemberMap[memberDecl.Name]; exists {
				a.errorf(memberDecl.Pos(), "duplicate const enum member %q in %q", memberDecl.Name, constEnumDecl.Name)
				continue
			}
			value := nextValue
			if memberDecl.Value != nil {
				resolved, ok := a.evalConstExpr(memberDecl.Value)
				if !ok || resolved.Kind != ConstInt {
					a.errorf(memberDecl.Value.Pos(), "const enum member %q.%q requires a compile-time integer value", constEnumDecl.Name, memberDecl.Name)
					continue
				}
				value = resolved.Int
			}
			member := &ConstEnumMember{Name: memberDecl.Name, Value: value, Decl: memberDecl}
			constEnumType.MemberMap[member.Name] = member
			members = append(members, member)
			nextValue = value + 1
		}
		constEnumType.Members = members
	}
}

func packedEnumStoreTypeName(enumName string) string {
	return enumName + ".Store"
}

func packedEnumTagTypeName(enumName string) string {
	return enumName + ".Tag"
}

func (a *Analyzer) populateEnumVariants(decls []scopedDecl) {
	for _, scoped := range decls {
		enumDecl, ok := scoped.Decl.(*ast.EnumDecl)
		if !ok {
			continue
		}
		enumType, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, enumDecl.Name)].(*EnumType)
		if enumType == nil {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.analyzeEnumAnnotations(enumDecl, enumType)
			if len(enumDecl.Common) > 0 && !enumDecl.Packed {
				a.errorf(enumDecl.Pos(), "enum %q only supports common: fields for packed enums", enumDecl.Name)
			}
			for _, commonDecl := range enumDecl.Common {
				storage := a.analyzePackedCommonFieldAnnotations(enumDecl, commonDecl)
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
				enumType.Common[commonDecl.Name] = Field{Name: commonDecl.Name, Type: commonType, Mutable: false, PackedStorage: storage}
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
				tailIndex := -1
				seenPayloadNames := map[string]bool{}
				hasNamedPayloads := false
				hasUnnamedPayloads := false
				for payloadIndex, payloadDecl := range variantDecl.Payload {
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
					if tailExpr, ok := payloadDecl.Type.(*ast.TailType); ok {
						if !enumDecl.Packed {
							a.errorf(payloadDecl.Type.Pos(), "enum %q variant %q tail payloads are only supported for packed enums", enumDecl.Name, variantDecl.Name)
						} else {
							if tailIndex >= 0 {
								a.errorf(payloadDecl.Type.Pos(), "packed enum %q variant %q can only declare one tail payload", enumDecl.Name, variantDecl.Name)
							}
							tailElemType := a.resolveType(tailExpr.Elem)
							payloadType = &DArrayViewType{Elem: tailElemType, SurfaceName: "dview"}
							tailIndex = payloadIndex
						}
					}
					if !enumDecl.Packed && SameType(payloadType, enumType) {
						a.errorf(payloadDecl.Type.Pos(), "enum %q variant %q cannot contain %q by value; use a reference type instead", enumDecl.Name, variantDecl.Name, enumDecl.Name)
					}
					payload = append(payload, payloadType)
					payloadNames = append(payloadNames, payloadDecl.Name)
				}
				if hasNamedPayloads && hasUnnamedPayloads {
					a.errorf(variantDecl.Position, "enum variant %q.%q must name either all payload fields or none", enumDecl.Name, variantDecl.Name)
				}
				variant := &EnumVariant{Name: variantDecl.Name, Tag: uint32(i), Payload: payload, PayloadNames: payloadNames, TailIndex: tailIndex, Decl: variantDecl}
				enumType.VariantMap[variant.Name] = variant
				variants = append(variants, variant)
				if enumType.Packed && enumType.TagType != nil {
					member := &ConstEnumMember{Name: variant.Name, Value: int64(variant.Tag)}
					enumType.TagType.Members = append(enumType.TagType.Members, member)
					enumType.TagType.MemberMap[member.Name] = member
					a.constValues[enumType.TagType.Name+"."+member.Name] = ConstValue{Kind: ConstInt, Int: member.Value}
				}
			}
			enumType.Variants = variants
		})
	}
}

func (a *Analyzer) populateStructFields(decls []scopedDecl) {
	for _, scoped := range decls {
		stDecl, ok := scoped.Decl.(*ast.StructDecl)
		if !ok {
			continue
		}
		st, _ := a.namedTypes[joinQualifiedName(scoped.Namespace, stDecl.Name)].(*StructType)
		if st == nil {
			continue
		}
		if st.Builtin && isBuiltinRuntimeStructName(stDecl.Name) {
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.analyzeStructAnnotations(stDecl, st)
			a.withGenericParams(stDecl.GenericParams, nil, func() {
				for _, field := range stDecl.Fields {
					if len(field.Annotations) != 0 {
						for _, annotation := range field.Annotations {
							a.errorf(annotation.Position, "field annotation @%s is only supported on packed enum common fields", annotation.Name)
						}
					}
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
		})
	}
}

func (a *Analyzer) collectValueSymbols(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.ConstDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				var declType Type = invalidType
				if n.Type != nil {
					declType = a.resolveType(n.Type)
				} else {
					declType = a.analyzeExprInScope(n.Value, a.globalScope)
					if IsInvalidType(declType) {
						if value, ok := a.evalConstExpr(n.Value); ok {
							switch value.Kind {
							case ConstInt:
								declType = a.namedTypes["int"]
							case ConstFloat:
								declType = a.namedTypes["f64"]
							case ConstBool:
								declType = a.namedTypes["bool"]
							case ConstString:
								declType = &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
							}
						}
					}
					if IsInvalidType(declType) {
						declType = a.inferLiteralType(n.Value)
					}
				}
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolConst, Type: declType, Node: n, Mutable: false}, n.Pos())
			case *ast.ConstEnumDecl:
			case *ast.GlobalDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				declType := a.resolveType(n.Type)
				if a.containsAffineHandleValues(declType, map[string]bool{}) {
					a.errorf(n.Pos(), "global %q cannot store affine handle values of type %s", n.Name, declType.String())
				}
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolGlobal, Type: declType, Node: n, Mutable: n.Mutable}, n.Pos())
			case *ast.FuncDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				fnType := a.funcTypeFromDecl(qualifiedName, n.TypeParams, n.RefStorageParams, n.RefStateParams, n.GenericParams, n.RegionParams, n.PermissionParams, n.Permissions, n.Params, n.ReturnType, false)
				symbolName := qualifiedName
				if n.Name == "__cast__" {
					symbolName = castHookSymbolName(qualifiedName, fnType, n.Pos())
				}
				sym := &Symbol{Name: symbolName, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false}
				a.functionTypes[symbolName] = fnType
				a.funcDeclSymbols[n] = sym
				a.defineGlobal(sym, n.Pos())
				if n.Name == "__cast__" {
					a.registerCastHook(scoped.Namespace, n, fnType, sym)
				}
			case *ast.ExternFuncDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				fnType := a.funcTypeFromDecl(qualifiedName, n.TypeParams, n.RefStorageParams, n.RefStateParams, n.GenericParams, n.RegionParams, n.PermissionParams, n.Permissions, n.Params, n.ReturnType, n.Variadic)
				a.applyExternFuncAnnotations(n, fnType)
				if !fnType.ReturnProvenanceKnown {
					fnType.ReturnProvenanceKnown = true
				}
				if !fnType.ReturnBorrowedOwnerRefsKnown {
					fnType.ReturnBorrowedOwnerRefsKnown = true
				}
				a.functionTypes[qualifiedName] = fnType
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolExternFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
			case *ast.ExternVarDecl:
				qualifiedName := joinQualifiedName(scoped.Namespace, n.Name)
				declType := a.resolveType(n.Type)
				if a.containsAffineHandleValues(declType, map[string]bool{}) {
					a.errorf(n.Pos(), "extern var %q cannot store affine handle values of type %s", n.Name, declType.String())
				}
				a.defineGlobal(&Symbol{Name: qualifiedName, Kind: SymbolExternVar, Type: declType, Node: n, Mutable: true}, n.Pos())
			case *ast.EnumDecl:
			case *ast.ErrorDecl:
			case *ast.PermissionDecl:
			case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
	}
}

func exactTypeKey(t Type) string {
	if t == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T:%s", t, t.String())
}

func castHookKey(source Type, target Type) castHookSignature {
	return castHookSignature{Source: exactTypeKey(source), Target: exactTypeKey(target)}
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

func (a *Analyzer) registerCastHook(namespace string, decl *ast.FuncDecl, fnType *FuncType, sym *Symbol) {
	if a == nil || decl == nil || fnType == nil || sym == nil {
		return
	}
	qualifiedName := joinQualifiedName(namespace, decl.Name)
	if len(fnType.Params) != 1 {
		a.errorf(decl.Pos(), "__cast__ hook %q must take exactly 1 parameter, got %d", qualifiedName, len(fnType.Params))
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
		a.errorf(decl.Pos(), "duplicate __cast__ hook for %s -> %s (already defined as %q)", fnType.Params[0].String(), fnType.Return.String(), existing.Name)
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

func isSupportedExternFunctionAnnotation(name string) bool {
	switch name {
	case "borrows_return", "borrows_return_field", "borrows_return_rebased", "borrows_return_field_rebased":
		return true
	default:
		return false
	}
}

func normalizePackedABIAnnotationArg(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "row-handle", "row_handle", "row", "rowhandle":
		return "row-handle", true
	case "word-handle", "word_handle", "word", "wordhandle":
		return "word-handle", true
	case "dense-fixed", "dense_fixed", "densefixed", "fixed-dense", "fixed_dense":
		return "dense-fixed", true
	case "index-soa", "index_soa", "index", "soa", "indexsoa":
		return "index-soa", true
	case "variant-sparse", "variant_sparse", "variant", "variantsparse", "sparse":
		return "variant-sparse", true
	default:
		return "", false
	}
}

func normalizePackedPrefixAnnotationArg(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all", "all-words", "all_words", "allwords", "full", "full-row", "full_row", "row":
		return "all-words", true
	case "common", "common-only", "common_only", "commononly", "common-fields", "common_fields":
		return "common-only", true
	default:
		return "", false
	}
}

func normalizePackedProfileAnnotationArg(value string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "canonical", "default", "canon", "variant_sparse", "variant-sparse":
		return "canonical", true
	case "retained_reads", "retained-reads", "retainedreads", "dense_reads", "dense-reads":
		return "retained-reads", true
	case "build_heavy", "build-heavy", "buildheavy", "dense_build_bias", "dense-build-bias", "balanced":
		return "build-heavy", true
	default:
		return "", false
	}
}

func normalizePackedFieldStorageAnnotationArg(value string) (PackedFieldStorageMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "inline", "in_place", "in-place", "row":
		return PackedFieldStorageInline, true
	case "side_table", "side-table", "sidetable", "cold":
		return PackedFieldStorageSideTable, true
	default:
		return PackedFieldStorageDefault, false
	}
}

func normalizeInlineAnnotationArg(value string) (FuncInlineMode, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "always", "force", "on":
		return FuncInlineModeAlways, true
	case "never", "off", "no":
		return FuncInlineModeNever, true
	default:
		return FuncInlineModeDefault, false
	}
}

func normalizeStructAlignmentAnnotationArg(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	parsed, err := strconv.ParseUint(trimmed, 0, 32)
	if err != nil || parsed == 0 {
		return 0, false
	}
	alignment := int(parsed)
	if bits.OnesCount32(uint32(alignment)) != 1 {
		return 0, false
	}
	return alignment, true
}

func temperatureModeForAnnotationName(name string) (FuncTemperatureMode, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hot":
		return FuncTemperatureModeHot, true
	case "cold":
		return FuncTemperatureModeCold, true
	default:
		return FuncTemperatureModeDefault, false
	}
}

func packedProfileDefaults(profile string) (string, string, bool) {
	switch profile {
	case "canonical":
		return "variant-sparse", "", true
	case "retained-reads":
		return "dense-fixed", "all-words", true
	case "build-heavy":
		return "dense-fixed", "common-only", true
	default:
		return "", "", false
	}
}

func isSupportedEnumAnnotation(name string) bool {
	switch name {
	case "packed_abi", "packed_prefix", "packed_profile":
		return true
	default:
		return false
	}
}

func isSupportedStructAnnotation(name string) bool {
	switch name {
	case "align", "cacheline_aligned":
		return true
	default:
		return false
	}
}

func isSupportedPackedCommonFieldAnnotation(name string) bool {
	switch name {
	case "storage":
		return true
	default:
		return false
	}
}

func (a *Analyzer) analyzePackedCommonFieldAnnotations(enumDecl *ast.EnumDecl, fieldDecl ast.FieldDecl) PackedFieldStorageMode {
	storage := PackedFieldStorageInline
	if enumDecl == nil || len(fieldDecl.Annotations) == 0 {
		return storage
	}
	seen := make(map[string]lexer.Pos, len(fieldDecl.Annotations))
	for _, annotation := range fieldDecl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on packed enum %q common field %q (first seen at %s:%d:%d)", annotation.Name, enumDecl.Name, fieldDecl.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedPackedCommonFieldAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown packed enum common-field annotation @%s on %q.%s", annotation.Name, enumDecl.Name, fieldDecl.Name)
			continue
		}
		switch annotation.Name {
		case "storage":
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@storage on packed enum %q common field %q expects exactly one argument", enumDecl.Name, fieldDecl.Name)
				continue
			}
			normalized, ok := normalizePackedFieldStorageAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@storage on packed enum %q common field %q uses unsupported mode %q (expected inline or side_table)", enumDecl.Name, fieldDecl.Name, annotation.Args[0])
				continue
			}
			storage = normalized
		}
	}
	return storage
}

func (a *Analyzer) analyzeEnumAnnotations(enumDecl *ast.EnumDecl, enumType *EnumType) {
	if enumDecl == nil || enumType == nil || len(enumDecl.Annotations) == 0 {
		return
	}
	seen := make(map[string]lexer.Pos, len(enumDecl.Annotations))
	profileOverride := ""
	hasProfileOverride := false
	abiOverride := ""
	hasABIOverride := false
	prefixOverride := ""
	hasPrefixOverride := false
	for _, annotation := range enumDecl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on enum %q (first seen at %s:%d:%d)", annotation.Name, enumDecl.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedEnumAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown enum annotation @%s on %q", annotation.Name, enumDecl.Name)
			continue
		}
		switch annotation.Name {
		case "packed_abi":
			if !enumDecl.Packed {
				a.errorf(annotation.Position, "@packed_abi on enum %q requires a packed enum", enumDecl.Name)
				continue
			}
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@packed_abi on enum %q expects exactly one ABI argument", enumDecl.Name)
				continue
			}
			normalized, ok := normalizePackedABIAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@packed_abi on enum %q uses unsupported ABI %q (expected row_handle, word_handle, dense_fixed, index_soa, or variant_sparse)", enumDecl.Name, annotation.Args[0])
				continue
			}
			abiOverride = normalized
			hasABIOverride = true
		case "packed_prefix":
			if !enumDecl.Packed {
				a.errorf(annotation.Position, "@packed_prefix on enum %q requires a packed enum", enumDecl.Name)
				continue
			}
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@packed_prefix on enum %q expects exactly one prefix argument", enumDecl.Name)
				continue
			}
			normalized, ok := normalizePackedPrefixAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@packed_prefix on enum %q uses unsupported prefix mode %q (expected all_words or common_only)", enumDecl.Name, annotation.Args[0])
				continue
			}
			prefixOverride = normalized
			hasPrefixOverride = true
		case "packed_profile":
			if !enumDecl.Packed {
				a.errorf(annotation.Position, "@packed_profile on enum %q requires a packed enum", enumDecl.Name)
				continue
			}
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@packed_profile on enum %q expects exactly one profile argument", enumDecl.Name)
				continue
			}
			normalized, ok := normalizePackedProfileAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@packed_profile on enum %q uses unsupported profile %q (expected canonical, retained_reads, or build_heavy)", enumDecl.Name, annotation.Args[0])
				continue
			}
			profileOverride = normalized
			hasProfileOverride = true
		}
	}
	if hasProfileOverride {
		enumType.PackedProfile = profileOverride
		enumType.HasPackedProfile = true
		if profileABI, profilePrefix, ok := packedProfileDefaults(profileOverride); ok {
			if profileABI != "" {
				enumType.PackedABIOverride = profileABI
				enumType.HasPackedABIOverride = true
			}
			if profilePrefix != "" {
				enumType.PackedPrefixOverride = profilePrefix
				enumType.HasPackedPrefixOverride = true
			}
		}
	}
	if hasABIOverride {
		enumType.PackedABIOverride = abiOverride
		enumType.HasPackedABIOverride = true
	}
	if hasPrefixOverride {
		enumType.PackedPrefixOverride = prefixOverride
		enumType.HasPackedPrefixOverride = true
	}
}

func (a *Analyzer) analyzeStructAnnotations(structDecl *ast.StructDecl, structType *StructType) {
	if structDecl == nil || structType == nil || len(structDecl.Annotations) == 0 {
		return
	}
	seen := make(map[string]lexer.Pos, len(structDecl.Annotations))
	alignment := 0
	hasAlignment := false
	alignmentSource := ""
	for _, annotation := range structDecl.Annotations {
		if prev, exists := seen[annotation.Name]; exists {
			a.errorf(annotation.Position, "duplicate @%s annotation on struct %q (first seen at %s:%d:%d)", annotation.Name, structDecl.Name, prev.File, prev.Line, prev.Col)
			continue
		}
		seen[annotation.Name] = annotation.Position
		if !isSupportedStructAnnotation(annotation.Name) {
			a.errorf(annotation.Position, "unknown struct annotation @%s on %q", annotation.Name, structDecl.Name)
			continue
		}
		switch annotation.Name {
		case "align":
			if len(annotation.Args) != 1 {
				a.errorf(annotation.Position, "@align on struct %q expects exactly one integer byte alignment", structDecl.Name)
				continue
			}
			parsed, ok := normalizeStructAlignmentAnnotationArg(annotation.Args[0])
			if !ok {
				a.errorf(annotation.Position, "@align on struct %q expects a positive power-of-two byte alignment, got %q", structDecl.Name, annotation.Args[0])
				continue
			}
			if hasAlignment && alignment != parsed {
				a.errorf(annotation.Position, "@align on struct %q conflicts with existing @%s request for %d-byte alignment", structDecl.Name, alignmentSource, alignment)
				continue
			}
			alignment = parsed
			hasAlignment = true
			alignmentSource = annotation.Name
		case "cacheline_aligned":
			if len(annotation.Args) != 0 {
				a.errorf(annotation.Position, "@cacheline_aligned on struct %q does not take arguments", structDecl.Name)
				continue
			}
			const cachelineAlignment = 64
			if hasAlignment && alignment != cachelineAlignment {
				a.errorf(annotation.Position, "@cacheline_aligned on struct %q conflicts with existing @%s request for %d-byte alignment", structDecl.Name, alignmentSource, alignment)
				continue
			}
			alignment = cachelineAlignment
			hasAlignment = true
			alignmentSource = annotation.Name
		}
	}
	if hasAlignment {
		structType.Alignment = alignment
		structType.HasAlignment = true
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
	var ownerSummaries []borrowedOwnerRefSummary
	for _, pathText := range annotation.Args {
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, false, "")
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			states = append(states, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			ownerSummaries = append(ownerSummaries, ownerSummary)
		}
	}
	if merged, ok := mergeRegionRefStates(states...); ok {
		fnType.ReturnProvenance = merged
	}
	if len(ownerSummaries) != 0 {
		mergedOwner := cloneBorrowedOwnerRefSummary(ownerSummaries[0])
		for i := 1; i < len(ownerSummaries); i++ {
			if next, ok := mergeBorrowedOwnerRefSummary(mergedOwner, ownerSummaries[i]); ok {
				mergedOwner = next
			}
		}
		fnType.ReturnBorrowedOwnerRefs = mergedOwner
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
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
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, false, returnFieldPath)
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			fnType.ReturnProvenance = assignRegionRefStateAtPath(fnType.ReturnProvenance, returnSteps, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			fnType.ReturnBorrowedOwnerRefs = assignBorrowedOwnerRefSummaryAtPath(fnType.ReturnBorrowedOwnerRefs, returnSteps, ownerSummary)
		}
	}
	if !hasRegionProvenance(fnType.ReturnProvenance) {
		fnType.ReturnProvenance = regionRefState{}
	}
	if !hasBorrowedOwnerRefSummary(fnType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
}

func (a *Analyzer) applyExternBorrowsReturnRebasedAnnotation(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation) {
	if len(annotation.Args) == 0 {
		a.errorf(annotation.Position, "@borrows_return_rebased on extern function %q expects at least one parameter path", fn.Name)
		return
	}
	var states []regionRefState
	var ownerSummaries []borrowedOwnerRefSummary
	for _, pathText := range annotation.Args {
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, true, "")
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			states = append(states, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			ownerSummaries = append(ownerSummaries, ownerSummary)
		}
	}
	if merged, ok := mergeRegionRefStates(states...); ok {
		fnType.ReturnProvenance = merged
	}
	if len(ownerSummaries) != 0 {
		mergedOwner := cloneBorrowedOwnerRefSummary(ownerSummaries[0])
		for i := 1; i < len(ownerSummaries); i++ {
			if next, ok := mergeBorrowedOwnerRefSummary(mergedOwner, ownerSummaries[i]); ok {
				mergedOwner = next
			}
		}
		fnType.ReturnBorrowedOwnerRefs = mergedOwner
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
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
		state, ownerSummary, ok := a.resolveExternBorrowAnnotationPath(fn, fnType, annotation, pathText, true, returnFieldPath)
		if !ok {
			continue
		}
		if hasRegionProvenance(state) {
			fnType.ReturnProvenance = assignRegionRefStateAtPath(fnType.ReturnProvenance, returnSteps, state)
		}
		if hasBorrowedOwnerRefSummary(ownerSummary) {
			fnType.ReturnBorrowedOwnerRefs = assignBorrowedOwnerRefSummaryAtPath(fnType.ReturnBorrowedOwnerRefs, returnSteps, ownerSummary)
		}
	}
	if !hasRegionProvenance(fnType.ReturnProvenance) {
		fnType.ReturnProvenance = regionRefState{}
	}
	if !hasBorrowedOwnerRefSummary(fnType.ReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnProvenanceKnown = true
	fnType.ReturnBorrowedOwnerRefsKnown = true
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
	key := regionFieldKeyForBorrowStep(steps[0])
	nextFields := cloneRegionRefFields(dst.Fields)
	if nextFields == nil {
		nextFields = map[string]regionRefState{}
	}
	child := nextFields[key]
	nextFields[key] = assignRegionRefStateAtPath(child, steps[1:], value)
	dst.Fields = nextFields
	dst.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(dst)
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

func (a *Analyzer) resolveExternBorrowAnnotationPath(fn *ast.ExternFuncDecl, fnType *FuncType, annotation ast.Annotation, pathText string, rebased bool, returnField string) (regionRefState, borrowedOwnerRefSummary, bool) {
	name, steps, ok := parseBorrowReturnAnnotationPath(pathText)
	if !ok {
		a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "has invalid path %q", pathText)
		return regionRefState{}, borrowedOwnerRefSummary{}, false
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
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	if index >= len(fnType.Params) {
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	paramType := fnType.Params[index]
	regionState, regionOK := a.abstractParamRegionRefState(paramType, index, map[string]bool{})
	ownerParam := &Symbol{Name: name, Kind: SymbolParam, Type: paramType, ParamIndex: index, Mutable: false}
	ownerState, ownerStateOK := a.abstractParamBorrowedOwnerRefState(paramType, affineValueKey{Root: ownerParam}, map[string]bool{})
	if !regionOK && !ownerStateOK {
		if returnField == "" {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot borrow from parameter %q of type %s", name, paramType.String())
		} else {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot borrow field %q from parameter %q of type %s", returnField, name, paramType.String())
		}
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	if len(steps) != 0 {
		if regionOK {
			regionState, regionOK = projectBorrowReturnAnnotationState(regionState, steps)
		}
		if ownerStateOK {
			ownerState, ownerStateOK = projectBorrowedOwnerRefStateAtSteps(ownerState, steps)
		}
		if !regionOK && !ownerStateOK {
			a.errorExternBorrowAnnotationPathError(fn, annotation, pathText, returnField, "cannot project path %q from parameter %q of type %s", pathText, name, fnType.Params[index].String())
			return regionRefState{}, borrowedOwnerRefSummary{}, false
		}
	}
	if rebased {
		if regionOK {
			regionState, regionOK = summarizeRegionIndexStates(regionState)
		}
		if ownerStateOK {
			ownerState, ownerStateOK = summarizeBorrowedOwnerRefIndexStates(ownerState)
		}
	}
	ownerSummary := borrowedOwnerRefSummary{}
	ownerOK := false
	if ownerStateOK {
		ownerSummary, ownerOK = abstractParamOnlyBorrowedOwnerRefSummary(ownerState)
	}
	if !regionOK && !ownerOK {
		return regionRefState{}, borrowedOwnerRefSummary{}, false
	}
	if !regionOK {
		regionState = regionRefState{}
	}
	if !ownerOK {
		ownerSummary = borrowedOwnerRefSummary{}
	}
	return regionState, ownerSummary, true
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
	case *ViewType:
		return a.containsAffineHandleValues(tt.Elem, seen)
	case *DArrayViewType:
		return a.containsAffineHandleValues(tt.Elem, seen)
	case *OptionalType:
		return a.containsAffineHandleValues(tt.Value, seen)
	case *DictType:
		return a.containsAffineHandleValues(tt.Key, seen) || a.containsAffineHandleValues(tt.Value, seen)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.containsAffineHandleValues(field.Type, seen) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.containsAffineHandleValues(payloadType, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		for _, field := range tt.Common {
			if a.containsAffineHandleValues(field.Type, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payloadType := range variant.Payload {
				if a.containsAffineHandleValues(payloadType, seen) {
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
		return true
	case *OptionalType:
		return a.typeCanContainRegionRefs(tt.Value, seen)
	case *ViewType:
		return true
	case *DArrayViewType:
		return true
	case *DStrType:
		return true
	case *SViewType:
		return true
	case *DictType:
		return a.typeCanContainRegionRefs(tt.Key, seen) || a.typeCanContainRegionRefs(tt.Value, seen)
	case *PackedVariantViewType:
		for _, field := range tt.Enum.Common {
			if a.typeCanContainRegionRefs(field.Type, seen) {
				return true
			}
		}
		for _, payloadType := range tt.Variant.Payload {
			if a.typeCanContainRegionRefs(payloadType, seen) {
				return true
			}
		}
		return false
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

func (a *Analyzer) abstractParamBorrowedOwnerRefState(t Type, baseKey affineValueKey, seen map[string]bool) (borrowedOwnerRefState, bool) {
	if t == nil || !a.containsBorrowedOwnerRefValues(t, map[string]bool{}) {
		return borrowedOwnerRefState{}, false
	}
	if _, ok := borrowableOwnerRefElemType(t); ok {
		return borrowedOwnerRefState{HasDirect: true, Direct: baseKey}, true
	}
	key := t.String()
	if seen[key] {
		return borrowedOwnerRefState{}, false
	}
	seen[key] = true
	state := borrowedOwnerRefState{}
	switch tt := t.(type) {
	case *OptionalType:
		return a.abstractParamBorrowedOwnerRefState(tt.Value, baseKey, seen)
	case *RefType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, baseKey, seen); ok {
			if elemState.HasDirect {
				state.HasDirect = true
				state.Direct = elemState.Direct
			}
			if len(elemState.Fields) != 0 {
				state.Fields = cloneBorrowedOwnerRefState(elemState).Fields
			}
		}
		return state, hasBorrowedOwnerRefState(state)
	case *StructType:
		for _, field := range tt.Fields {
			fieldState, ok := a.abstractParamBorrowedOwnerRefState(field.Type, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, field.Name)}, seen)
			if !ok {
				continue
			}
			if state.Fields == nil {
				state.Fields = map[string]borrowedOwnerRefState{}
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
				fieldState, ok := a.abstractParamBorrowedOwnerRefState(fieldType, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, field.Name)}, seen)
				if !ok {
					continue
				}
				if state.Fields == nil {
					state.Fields = map[string]borrowedOwnerRefState{}
				}
				state.Fields[field.Name] = fieldState
			}
			return state, hasBorrowedOwnerRefState(state)
		}
	case *ArrayType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	case *DArrayType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	case *ViewType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	case *DArrayViewType:
		if elemState, ok := a.abstractParamBorrowedOwnerRefState(tt.Elem, affineValueKey{Root: baseKey.Root, Path: joinAffinePath(baseKey.Path, regionAnyIndexFieldKey())}, seen); ok {
			state.Fields = map[string]borrowedOwnerRefState{regionAnyIndexFieldKey(): elemState}
		}
	}
	return state, hasBorrowedOwnerRefState(state)
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
	case *OptionalType:
		return a.abstractParamRegionRefState(tt.Value, paramIndex, seen)
	case *RefType:
		if elemState, ok := a.abstractParamRegionRefState(tt.Elem, paramIndex, seen); ok {
			if len(elemState.Fields) != 0 {
				state.Fields = cloneRegionRefState(elemState).Fields
			}
		}
		state.PackedStoreSummaryKnown = false
		return withPackedStoreProvenanceSummary(state), true
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
			state.PackedStoreSummaryKnown = false
			return withPackedStoreProvenanceSummary(state), true
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
			state.PackedStoreSummaryKnown = false
			return withPackedStoreProvenanceSummary(state), true
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
	state.PackedStoreSummaryKnown = false
	return withPackedStoreProvenanceSummary(state), true
}

func (a *Analyzer) analyzeDecls(decls []scopedDecl) {
	for _, scoped := range decls {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.ConstDecl:
				if sym, ok := a.globalScope.Lookup(joinQualifiedName(scoped.Namespace, n.Name)); ok {
					valueType := a.analyzeValueExprInScope(n.Value, sym.Type, a.globalScope)
					if !AssignableTo(sym.Type, valueType) {
						a.errorf(n.Pos(), "const %q expects %s, got %s", n.Name, sym.Type.String(), valueType.String())
					}
					if value, ok := a.evalConstExpr(n.Value); ok {
						a.constValues[joinQualifiedName(scoped.Namespace, n.Name)] = value
					} else {
						a.errorf(n.Value.Pos(), "const %q initializer must be a compile-time %s value", n.Name, sym.Type.String())
					}
				}
			case *ast.GlobalDecl:
				if n.Value != nil {
					if sym, ok := a.globalScope.Lookup(joinQualifiedName(scoped.Namespace, n.Name)); ok {
						valueType := a.analyzeValueExprInScope(n.Value, sym.Type, a.globalScope)
						if !AssignableTo(sym.Type, valueType) {
							a.errorf(n.Pos(), "global %q expects %s, got %s", n.Name, sym.Type.String(), valueType.String())
						}
					}
				}
			case *ast.FuncDecl:
				a.analyzeFunctionAnnotations(n)
				a.analyzeFunc(n)
			case *ast.ConstEnumDecl:
			case *ast.EnumDecl:
			case *ast.ErrorDecl:
			case *ast.PermissionDecl:
			case *ast.ExportTypeDecl, *ast.ExportFuncDecl, *ast.ExportGlobalDecl:
			}
		})
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
	if sym, ok := a.symbolForFuncDecl(fn); ok {
		signature, _ = sym.Type.(*FuncType)
	}
	accepted := make([]ast.Annotation, 0, len(valid))
	for _, annotation := range valid {
		if a.validateFunctionAnnotation(annotation, fn, signature) {
			switch annotation.Name {
			case "inline":
				a.applyFunctionInlineAnnotation(annotation, fn, signature)
			case "norecurse":
				a.applyFunctionNoRecurseAnnotation(annotation, fn, signature)
			case "hot", "cold":
				a.applyFunctionTemperatureAnnotation(annotation, fn, signature)
			case "guard_nonnull", "guard_variant":
				a.applyFunctionGuardAnnotation(annotation, fn, signature)
			default:
				accepted = append(accepted, annotation)
			}
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

func (a *Analyzer) applyFunctionInlineAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil || len(annotation.Args) != 1 {
		return
	}
	mode, ok := normalizeInlineAnnotationArg(annotation.Args[0])
	if !ok {
		return
	}
	signature.InlineMode = mode
	signature.HasInlineMode = true
}

func (a *Analyzer) applyFunctionNoRecurseAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil || len(annotation.Args) != 0 {
		return
	}
	signature.HasNoRecurse = true
}

func (a *Analyzer) applyFunctionTemperatureAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil || len(annotation.Args) != 0 {
		return
	}
	mode, ok := temperatureModeForAnnotationName(annotation.Name)
	if !ok {
		return
	}
	if signature.HasTemperatureMode {
		if signature.TemperatureMode != mode {
			a.errorf(annotation.Position, "@%s on function %q conflicts with existing @%s annotation", annotation.Name, fn.Name, string(signature.TemperatureMode))
		}
		return
	}
	signature.TemperatureMode = mode
	signature.HasTemperatureMode = true
}

func (a *Analyzer) applyFunctionGuardAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) {
	if signature == nil {
		return
	}
	switch annotation.Name {
	case "guard_nonnull":
		paramIndex, ok := functionAnnotationParamIndex(fn, annotation.Args[0])
		if !ok {
			return
		}
		signature.GuardEffects = append(signature.GuardEffects, FuncGuardEffect{Kind: FuncGuardKindNonNull, ParamIndex: paramIndex})
	case "guard_variant":
		paramIndex, ok := functionAnnotationParamIndex(fn, annotation.Args[0])
		if !ok {
			return
		}
		enumName, variantName, ok := parseFunctionGuardVariantPath(annotation.Args[1])
		if !ok {
			return
		}
		base, _, ok := a.lookupVisibleType(enumName)
		if !ok {
			return
		}
		enumType, ok := base.(*EnumType)
		if !ok || enumType == nil {
			return
		}
		signature.GuardEffects = append(signature.GuardEffects, FuncGuardEffect{Kind: FuncGuardKindPackedVariant, ParamIndex: paramIndex, EnumName: enumType.Name, VariantName: variantName})
	}
}

func (a *Analyzer) validateFunctionAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if signature == nil {
		a.errorf(annotation.Position, "cannot resolve signature for @%s function %q", annotation.Name, fn.Name)
		return false
	}
	if annotation.Name == "guard_nonnull" {
		return a.validateFunctionGuardNonNullAnnotation(annotation, fn, signature)
	}
	if annotation.Name == "guard_variant" {
		return a.validateFunctionGuardVariantAnnotation(annotation, fn, signature)
	}
	if annotation.Name == "skip" || annotation.Name == "ignore" {
		return true
	}
	if annotation.Name == "inline" {
		if len(annotation.Args) != 1 {
			a.errorf(annotation.Position, "@inline on function %q expects exactly one mode argument", fn.Name)
			return false
		}
		if _, ok := normalizeInlineAnnotationArg(annotation.Args[0]); !ok {
			a.errorf(annotation.Position, "@inline on function %q uses unsupported mode %q (expected always or never)", fn.Name, annotation.Args[0])
			return false
		}
		return true
	}
	if annotation.Name == "norecurse" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@norecurse on function %q does not take arguments", fn.Name)
			return false
		}
		return true
	}
	if annotation.Name == "hot" || annotation.Name == "cold" {
		if len(annotation.Args) != 0 {
			a.errorf(annotation.Position, "@%s on function %q does not take arguments", annotation.Name, fn.Name)
			return false
		}
		return true
	}
	if len(signature.TypeParams) > 0 || len(signature.RegionParams) > 0 || len(signature.ShapeParams) > 0 {
		a.errorf(annotation.Position, "@%s function %q must not have type or shape parameters; got %s", annotation.Name, fn.Name, signature.String())
		return false
	}
	if !annotationAllowsDeclaredPermissions(annotation.Name, signature) {
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

func (a *Analyzer) validateFunctionGuardSignature(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if signature.Return == nil || !IsBoolType(signature.Return) {
		a.errorf(annotation.Position, "@%s function %q must return bool, got %s", annotation.Name, fn.Name, signature.Return.String())
		return false
	}
	if signature.Variadic {
		a.errorf(annotation.Position, "@%s function %q must not be variadic", annotation.Name, fn.Name)
		return false
	}
	if len(signature.Permissions) != 0 {
		a.errorf(annotation.Position, "@%s function %q must not require permissions; got %s", annotation.Name, fn.Name, signature.String())
		return false
	}
	return true
}

func (a *Analyzer) validateFunctionGuardNonNullAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if !a.validateFunctionGuardSignature(annotation, fn, signature) {
		return false
	}
	if len(annotation.Args) != 1 {
		a.errorf(annotation.Position, "@guard_nonnull on function %q expects exactly one parameter name", fn.Name)
		return false
	}
	paramIndex, ok := functionAnnotationParamIndex(fn, annotation.Args[0])
	if !ok || paramIndex >= len(signature.Params) {
		a.errorf(annotation.Position, "@guard_nonnull on function %q references unknown parameter %q", fn.Name, annotation.Args[0])
		return false
	}
	if !guardNonNullParamType(signature.Params[paramIndex]) {
		a.errorf(annotation.Position, "@guard_nonnull on function %q requires a nullable reference or optional parameter, got %s", fn.Name, signature.Params[paramIndex].String())
		return false
	}
	return true
}

func (a *Analyzer) validateFunctionGuardVariantAnnotation(annotation ast.Annotation, fn *ast.FuncDecl, signature *FuncType) bool {
	if !a.validateFunctionGuardSignature(annotation, fn, signature) {
		return false
	}
	if len(annotation.Args) != 2 {
		a.errorf(annotation.Position, "@guard_variant on function %q expects a parameter name and Enum.Variant path", fn.Name)
		return false
	}
	paramIndex, ok := functionAnnotationParamIndex(fn, annotation.Args[0])
	if !ok || paramIndex >= len(signature.Params) {
		a.errorf(annotation.Position, "@guard_variant on function %q references unknown parameter %q", fn.Name, annotation.Args[0])
		return false
	}
	enumName, variantName, ok := parseFunctionGuardVariantPath(annotation.Args[1])
	if !ok {
		a.errorf(annotation.Position, "@guard_variant on function %q expects an Enum.Variant path, got %q", fn.Name, annotation.Args[1])
		return false
	}
	base, _, ok := a.lookupVisibleType(enumName)
	if !ok {
		a.errorf(annotation.Position, "@guard_variant on function %q references unknown enum %q", fn.Name, enumName)
		return false
	}
	enumType, ok := base.(*EnumType)
	if !ok || enumType == nil {
		a.errorf(annotation.Position, "@guard_variant on function %q expects an enum variant path, got %q", fn.Name, annotation.Args[1])
		return false
	}
	if !enumType.Packed {
		a.errorf(annotation.Position, "@guard_variant on function %q currently requires a packed enum variant path, got %q", fn.Name, annotation.Args[1])
		return false
	}
	if _, ok := enumType.Variant(variantName); !ok {
		a.errorf(annotation.Position, "enum %q has no variant %q", enumType.Name, variantName)
		return false
	}
	paramEnum, _, ok := resolveMatchableEnumType(signature.Params[paramIndex])
	if !ok || paramEnum == nil || !paramEnum.Packed {
		a.errorf(annotation.Position, "@guard_variant on function %q requires a packed enum parameter, got %s", fn.Name, signature.Params[paramIndex].String())
		return false
	}
	if paramEnum.Name != enumType.Name {
		a.errorf(annotation.Position, "@guard_variant on function %q expects parameter %q to use enum %q, got %q", fn.Name, annotation.Args[0], enumType.Name, paramEnum.Name)
		return false
	}
	return true
}

func guardNonNullParamType(t Type) bool {
	switch tt := StripAggregateStateType(t).(type) {
	case *RefType:
		return tt != nil && tt.State == RefStateNullable
	case *OptionalType:
		return tt != nil
	default:
		return false
	}
}

func functionAnnotationParamIndex(fn *ast.FuncDecl, name string) (int, bool) {
	if fn == nil || name == "" {
		return 0, false
	}
	for i, param := range fn.Params {
		if param.Name == name {
			return i, true
		}
	}
	return 0, false
}

func parseFunctionGuardVariantPath(text string) (string, string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", "", false
	}
	split := strings.LastIndex(trimmed, ".")
	if split <= 0 || split >= len(trimmed)-1 {
		return "", "", false
	}
	return trimmed[:split], trimmed[split+1:], true
}

func annotationAllowsDeclaredPermissions(annotationName string, signature *FuncType) bool {
	if signature == nil || len(signature.Permissions) == 0 {
		return true
	}
	if annotationName != "test" {
		return false
	}
	for _, ref := range signature.PermissionRefs {
		if ref.Name != "Abort" {
			return false
		}
	}
	return true
}

func isSupportedFunctionAnnotation(name string) bool {
	switch name {
	case "test", "bench", "fixture", "skip", "ignore", "inline", "norecurse", "hot", "cold", "guard_nonnull", "guard_variant":
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
	sym, _ := a.symbolForFuncDecl(fn)
	fnType, _ := sym.Type.(*FuncType)
	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedAffineValues := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	a.currentScope = NewScope(a.globalScope)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	if fnType != nil {
		a.currentReturn = fnType.Return
		a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	}
	a.withGenericParams(fn.GenericParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
					for i, param := range fn.Params {
						var ptype Type = invalidType
						if fnType != nil && i < len(fnType.Params) {
							ptype = fnType.Params[i]
						}
						sym := &Symbol{Name: param.Name, Kind: SymbolParam, Type: ptype, Node: fn, ParamIndex: i, Mutable: a.paramIsMutable(param)}
						a.defineLocal(sym, param.Position)
						a.bindActivePackedStoreType(ptype)
						a.recordValueBinding(sym, nil)
						a.recordBorrowedOwnerRefParam(sym)
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
		if hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
			fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs)
		} else {
			fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
		}
		fnType.ReturnBorrowedOwnerRefsKnown = true
		fnType.FreshReturnShapeParams = mergeShapeParamNames(fnType.FreshReturnShapeParams, inferredFreshReturnShapeParams(a.returnFreshShapeStatus))
		inferredRefs := canonicalizePermissionRefs(a.currentFunctionUsedPermissionRefs)
		inferredPermissions := permissionFamiliesFromRefs(inferredRefs)
		fnType.PermissionRefs = mergePermissionRefs(fnType.DeclaredPermissionRefs, inferredRefs)
		fnType.Permissions = mergePermissionFamilies(fnType.DeclaredPermissions, inferredPermissions)
		a.finalizeFunctionAnalysis(fn, fnType)
	}
	a.reportUnconsumedProtocolValues()
	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
}

func (a *Analyzer) inferFuncReturnProvenance(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || fnType.ReturnProvenanceKnown {
		return
	}
	if a.returnProvenanceInProgress[fn] {
		return
	}
	a.returnProvenanceInProgress[fn] = true
	defer delete(a.returnProvenanceInProgress, fn)

	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedAffineValues := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	savedSuppressDiagnostics := a.suppressDiagnostics

	a.currentScope = NewScope(a.globalScope)
	a.currentReturn = fnType.Return
	a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	a.suppressDiagnostics = true

	a.withGenericParams(fn.GenericParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
					for i, param := range fn.Params {
						var ptype Type = invalidType
						if i < len(fnType.Params) {
							ptype = fnType.Params[i]
						}
						sym := &Symbol{Name: param.Name, Kind: SymbolParam, Type: ptype, Node: fn, ParamIndex: i, Mutable: a.paramIsMutable(param)}
						a.defineLocal(sym, param.Position)
						a.bindActivePackedStoreType(ptype)
						a.recordValueBinding(sym, nil)
						a.recordBorrowedOwnerRefParam(sym)
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
	if hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs)
	} else {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnBorrowedOwnerRefsKnown = true

	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	a.suppressDiagnostics = savedSuppressDiagnostics
}

func (a *Analyzer) inferFuncReturnBorrowedOwnerRefs(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || fnType.ReturnBorrowedOwnerRefsKnown {
		return
	}
	if a.returnBorrowedOwnerRefInProgress[fn] {
		return
	}
	a.returnBorrowedOwnerRefInProgress[fn] = true
	defer delete(a.returnBorrowedOwnerRefInProgress, fn)

	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedReturnFreshStatus := a.returnFreshShapeStatus
	savedRegions := a.currentRegions
	savedRegionMarks := a.currentRegionMarks
	savedRegionRefs := a.currentRegionRefs
	savedAffineValues := a.currentAffineValues
	savedBorrowedOwnerRefs := a.currentBorrowedOwnerRefs
	savedFunctionValues := a.currentFunctionValues
	savedSpecializedValueTypes := a.currentSpecializedValueTypes
	savedValueBindings := a.currentValueBindings
	savedPackedVariantViews := a.currentPackedVariantViews
	savedPackedStores := a.currentPackedStores
	savedPackedStoreResolutions := a.currentPackedStoreResolutions
	savedFunctionPermissions := a.currentFunctionUsedPermissions
	savedFunctionPermissionRefs := a.currentFunctionUsedPermissionRefs
	savedReturnProvenance := a.currentReturnProvenance
	savedReturnBorrowedOwnerRefs := a.currentReturnBorrowedOwnerRefs
	savedSuppressDiagnostics := a.suppressDiagnostics

	a.currentScope = NewScope(a.globalScope)
	a.currentReturn = fnType.Return
	a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	a.currentRegions = map[*Symbol]regionState{}
	a.currentRegionMarks = map[*Symbol]regionMarkState{}
	a.currentRegionRefs = map[*Symbol]regionRefState{}
	a.currentAffineValues = map[affineValueKey]affineValueState{}
	a.currentBorrowedOwnerRefs = map[*Symbol]borrowedOwnerRefState{}
	a.currentFunctionValues = map[*Symbol]*FuncType{}
	a.currentSpecializedValueTypes = map[*Symbol]Type{}
	a.currentValueBindings = map[*Symbol]ast.Expr{}
	a.currentPackedVariantViews = map[*Symbol]*PackedVariantViewType{}
	a.currentPackedStores = map[string]*PackedEnumStoreType{}
	a.currentPackedStoreResolutions = map[*Symbol]packedStoreResolution{}
	a.currentFunctionUsedPermissions = map[string]bool{}
	a.currentFunctionUsedPermissionRefs = nil
	a.currentReturnProvenance = regionRefState{}
	a.currentReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	a.suppressDiagnostics = true

	a.withGenericParams(fn.GenericParams, nil, func() {
		a.withRegionParams(fn.RegionParams, func() {
			a.withPermissionParams(fn.PermissionParams, func() {
				a.withShapeParams(fnType.ShapeParams, func() {
					for i, param := range fn.Params {
						var ptype Type = invalidType
						if i < len(fnType.Params) {
							ptype = fnType.Params[i]
						}
						sym := &Symbol{Name: param.Name, Kind: SymbolParam, Type: ptype, Node: fn, ParamIndex: i, Mutable: a.paramIsMutable(param)}
						a.defineLocal(sym, param.Position)
						a.bindActivePackedStoreType(ptype)
						a.recordValueBinding(sym, nil)
						a.recordBorrowedOwnerRefParam(sym)
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

	if hasBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs) {
		fnType.ReturnBorrowedOwnerRefs = cloneBorrowedOwnerRefSummary(a.currentReturnBorrowedOwnerRefs)
	} else {
		fnType.ReturnBorrowedOwnerRefs = borrowedOwnerRefSummary{}
	}
	fnType.ReturnBorrowedOwnerRefsKnown = true

	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.returnFreshShapeStatus = savedReturnFreshStatus
	a.currentRegions = savedRegions
	a.currentRegionMarks = savedRegionMarks
	a.currentRegionRefs = savedRegionRefs
	a.currentAffineValues = savedAffineValues
	a.currentBorrowedOwnerRefs = savedBorrowedOwnerRefs
	a.currentFunctionValues = savedFunctionValues
	a.currentSpecializedValueTypes = savedSpecializedValueTypes
	a.currentValueBindings = savedValueBindings
	a.currentPackedVariantViews = savedPackedVariantViews
	a.currentPackedStores = savedPackedStores
	a.currentPackedStoreResolutions = savedPackedStoreResolutions
	a.currentFunctionUsedPermissions = savedFunctionPermissions
	a.currentFunctionUsedPermissionRefs = savedFunctionPermissionRefs
	a.currentReturnProvenance = savedReturnProvenance
	a.currentReturnBorrowedOwnerRefs = savedReturnBorrowedOwnerRefs
	a.suppressDiagnostics = savedSuppressDiagnostics
}
