package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

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
	a.registerBuiltinStructType("EnumerateView", []string{"S", "T"}, false, []builtinFieldSpec{
		{name: "source", typ: namedTypeExpr("S", false), mutable: false},
	})
	a.registerBuiltinStructType("ChunksExactView", []string{"T"}, false, []builtinFieldSpec{
		{name: "source", typ: genericTypeExpr("dview", namedTypeExpr("T", false)), mutable: false},
		{name: "chunk_size", typ: namedTypeExpr("usize", false), mutable: false},
		{name: "len", typ: namedTypeExpr("usize", false), mutable: false},
	})
	a.registerBuiltinStructType("TreeChildren", []string{"N", "T"}, false, []builtinFieldSpec{
		{name: "node", typ: namedTypeExpr("N", false), mutable: false},
	})
	a.registerBuiltinStructType("TreeAttributeSeq", []string{"S", "T"}, false, []builtinFieldSpec{
		{name: "source", typ: namedTypeExpr("S", false), mutable: false},
	})
	a.registerBuiltinStructType("NodeKey", []string{"T"}, false, []builtinFieldSpec{
		{name: "index", typ: namedTypeExpr("u32", false), mutable: false},
	})
	a.registerBuiltinStructType("NodeTable", []string{"N", "T"}, false, []builtinFieldSpec{
		{name: "values", typ: genericTypeExpr("dview", namedTypeExpr("T", false)), mutable: false},
	})
	a.registerBuiltinStructType("DictBucket", []string{"K", "T"}, false, []builtinFieldSpec{
		{name: "key", typ: namedTypeExpr("K", false), mutable: true},
		{name: "value", typ: namedTypeExpr("T", false), mutable: true},
	})
	a.registerBuiltinStructType("DynDict", []string{"K", "T"}, false, []builtinFieldSpec{
		{name: "items", typ: refToTypeExpr(genericTypeExpr("DictBucket", namedTypeExpr("K", false), namedTypeExpr("T", false)), true), mutable: true},
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
	case "SplitView", "ChunksExactView", "TreeChildren":
		return true
	case "EnumerateView":
		return true
	case "NodeKey", "NodeTable":
		return true
	case "PackedStoreAllocResult", "PackedStoreIndexAllocResult":
		return true
	default:
		return false
	}
}
