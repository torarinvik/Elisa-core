package semantic

import (
	"llcontext/src/ast"
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
	"ctx_stage1rt_list_new":               {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_list_new_reserve":       {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_list_reserve":           {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_list_push":              {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_list_push_mut":          {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_list_concat":            {FreshReturnShapeParams: []string{"shape_result"}},
	"ctx_stage1rt_list_truncate":          {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_list_clear":             {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_list_from_view":         {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_new":              {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_new_reserve":      {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_reserve":          {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_push":             {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_push_mut":         {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_concat":           {FreshReturnShapeParams: []string{"shape_result"}},
	"ctx_stage1rt_tlist_truncate":         {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_clear":            {FreshReturnShapeParams: []string{"shape_out"}},
	"ctx_stage1rt_tlist_from_view":        {FreshReturnShapeParams: []string{"shape_out"}},
	"arena_da_from_view":                  {FreshReturnShapeParams: []string{"shape_out"}},
}

type freshReturnStatus int

const (
	freshReturnUnknown freshReturnStatus = iota
	freshReturnAlways
	freshReturnNotFresh
)

type Analyzer struct {
	file                   *ast.File
	diagnostics            []Diagnostic
	namedTypes             map[string]Type
	globalScope            *Scope
	functionTypes          map[string]*FuncType
	constValues            map[string]ConstValue
	exprTypes              map[ast.Expr]Type
	typeParamScopes        []map[string]Type
	shapeParamScopes       []map[string]Shape
	freshShapeCounter      int
	returnFreshShapeStatus map[string]freshReturnStatus
	currentScope           *Scope
	currentReturn          Type
}

func Analyze(file *ast.File) *Result {
	a := &Analyzer{
		file:          file,
		namedTypes:    map[string]Type{},
		globalScope:   NewScope(nil),
		functionTypes: map[string]*FuncType{},
		constValues:   map[string]ConstValue{},
		exprTypes:     map[ast.Expr]Type{},
	}
	a.registerBuiltins()
	a.collectConstValues(file.Decls)
	activeDecls := a.expandActiveDecls(file.Decls)
	a.collectNamedTypes(activeDecls)
	a.populateStructFields(activeDecls)
	a.collectValueSymbols(activeDecls)
	a.analyzeDecls(activeDecls)
	return &Result{
		File:        file,
		GlobalScope: a.globalScope,
		NamedTypes:  a.namedTypes,
		ConstValues: a.constValues,
		ExprTypes:   a.exprTypes,
		Diagnostics: a.diagnostics,
	}
}

func (a *Analyzer) registerBuiltins() {
	for _, name := range []string{"void", "bool", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr"} {
		a.namedTypes[name] = &BuiltinType{Name: name}
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
			if _, exists := a.namedTypes[n.Name]; exists {
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
		case *ast.ExternTypeDecl:
			if _, exists := a.namedTypes[n.Name]; exists {
				a.errorf(n.Pos(), "duplicate type %q", n.Name)
				continue
			}
			a.namedTypes[n.Name] = &OpaqueType{Name: n.Name}
		}
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
		a.withTypeParams(stDecl.TypeParams, nil, func() {
			for _, field := range stDecl.Fields {
				if _, exists := st.Fields[field.Name]; exists {
					a.errorf(field.Position, "duplicate field %q in struct %q", field.Name, stDecl.Name)
					continue
				}
				fieldType := a.resolveType(field.Type)
				if field.IsTail {
					fieldType = &RefType{Elem: fieldType, State: RefStateNonNull}
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
			fnType := a.funcTypeFromDecl(n.Name, n.TypeParams, n.Params, n.ReturnType, false)
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternFuncDecl:
			fnType := a.funcTypeFromDecl(n.Name, nil, n.Params, n.ReturnType, n.Variadic)
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolExternFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternVarDecl:
			declType := a.resolveType(n.Type)
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolExternVar, Type: declType, Node: n, Mutable: true}, n.Pos())
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
			a.analyzeFunc(n)
		}
	}
}

func (a *Analyzer) analyzeFunc(fn *ast.FuncDecl) {
	sym, _ := a.globalScope.Lookup(fn.Name)
	fnType, _ := sym.Type.(*FuncType)
	savedScope := a.currentScope
	savedReturn := a.currentReturn
	savedReturnFreshStatus := a.returnFreshShapeStatus
	a.currentScope = NewScope(a.globalScope)
	if fnType != nil {
		a.currentReturn = fnType.Return
		a.returnFreshShapeStatus = freshReturnTracker(fnType.Return)
	}
	a.withTypeParams(fn.TypeParams, nil, func() {
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
	if fnType != nil {
		fnType.FreshReturnShapeParams = mergeShapeParamNames(fnType.FreshReturnShapeParams, inferredFreshReturnShapeParams(a.returnFreshShapeStatus))
	}
	a.currentScope = savedScope
	a.currentReturn = savedReturn
	a.returnFreshShapeStatus = savedReturnFreshStatus
}
