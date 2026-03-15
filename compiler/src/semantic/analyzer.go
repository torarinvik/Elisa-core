package semantic

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

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
	FreshReturnShapes bool
}

var shapeTransformTable = map[string]ShapeTransformSpec{
	"resize": {FreshReturnShapes: true},
	"push":   {FreshReturnShapes: true},
	"concat": {FreshReturnShapes: true},
	"strcat": {FreshReturnShapes: true},
}

type Analyzer struct {
	file              *ast.File
	diagnostics       []Diagnostic
	namedTypes        map[string]Type
	globalScope       *Scope
	functionTypes     map[string]*FuncType
	constValues       map[string]ConstValue
	typeParamScopes   []map[string]Type
	shapeParamScopes  []map[string]Shape
	freshShapeCounter int
	currentScope      *Scope
	currentReturn     Type
}

func Analyze(file *ast.File) *Result {
	a := &Analyzer{
		file:          file,
		namedTypes:    map[string]Type{},
		globalScope:   NewScope(nil),
		functionTypes: map[string]*FuncType{},
		constValues:   map[string]ConstValue{},
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
	a.currentScope = NewScope(a.globalScope)
	if fnType != nil {
		a.currentReturn = fnType.Return
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
	a.currentScope = savedScope
	a.currentReturn = savedReturn
}

func (a *Analyzer) analyzeStmt(stmt ast.Stmt) {
	switch n := stmt.(type) {
	case *ast.VarDeclStmt:
		declType := a.resolveType(n.Type)
		if n.Value != nil {
			valueType := a.analyzeExpr(n.Value)
			if !AssignableTo(declType, valueType) {
				a.errorf(n.Pos(), "variable %q expects %s, got %s", n.Name, declType.String(), valueType.String())
			}
		}
		a.defineLocal(&Symbol{Name: n.Name, Kind: SymbolLocal, Type: declType, Node: n, Mutable: n.Mutable}, n.Pos())
	case *ast.AssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeExpr(n.Value)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
		}
		a.recordAssignmentRefinement(n.Target, targetType, valueType)
	case *ast.AugAssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeExpr(n.Value)
		if !IsNumericType(targetType) || !IsNumericType(valueType) {
			a.errorf(n.Pos(), "augmented assignment requires numeric operands")
		}
	case *ast.AsRefAssignStmt:
		targetType := a.asRefTargetType(n.Target, n.AsKind)
		valueType := a.analyzeExpr(n.Value)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
		}
		a.recordAssignmentRefinement(n.Target, targetType, targetType)
	case *ast.ReturnStmt:
		if n.Value == nil {
			if a.currentReturn != nil && !SameType(a.currentReturn, a.namedTypes["void"]) {
				a.errorf(n.Pos(), "return value required for %s", a.currentReturn.String())
			}
			return
		}
		valueType := a.analyzeExpr(n.Value)
		if a.currentReturn == nil {
			a.errorf(n.Pos(), "unexpected return value")
			return
		}
		expectedReturn := a.matchReturnType(valueType)
		if !AssignableTo(expectedReturn, valueType) {
			a.errorf(n.Pos(), "return type expects %s, got %s", expectedReturn.String(), valueType.String())
		}
	case *ast.IfStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "if condition must be bool, got %s", condType.String())
		}
		a.analyzeBlockInScope(n.Then, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
		for _, elif := range n.Elifs {
			elifType := a.analyzeExpr(elif.Cond)
			if !IsBoolType(elifType) {
				a.errorf(elif.Position, "elif condition must be bool, got %s", elifType.String())
			}
			a.analyzeBlockInScope(elif.Body, a.refinedScopeForCondition(a.currentScope, elif.Cond, true))
		}
		if len(n.Elifs) == 0 {
			a.analyzeBlockInScope(n.Else, a.refinedScopeForCondition(a.currentScope, n.Cond, false))
		} else {
			a.analyzeBlock(n.Else)
		}
		a.applyPostIfFallthroughRefinement(n)
	case *ast.WhileStmt:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "while condition must be bool, got %s", condType.String())
		}
		a.analyzeBlockInScope(n.Body, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
	case *ast.PassStmt:
		return
	case *ast.PanicStmt:
		a.analyzeExpr(n.Message)
	case *ast.ExprStmt:
		if cond, ok := assertedCondition(n.Expr); ok {
			condType := a.analyzeCondExpr(cond)
			if !IsBoolType(condType) {
				a.errorf(n.Pos(), "assert condition must be bool, got %s", condType.String())
			}
			a.applyConditionRefinements(a.currentScope, cond, true)
			return
		}
		a.analyzeExpr(n.Expr)
	case *ast.StaticIfStmt:
		for _, stmt := range a.activeStmtBranch(n) {
			a.analyzeStmt(stmt)
		}
	case *ast.StaticErrorStmt:
		if msg, ok := a.evalConstStringExpr(n.Message); ok {
			a.errorf(n.Pos(), "static error: %s", msg)
		} else {
			a.errorf(n.Pos(), "static error triggered")
		}
	case *ast.DiscardStmt:
		a.analyzeExpr(n.Value)
	}
}

func (a *Analyzer) analyzeBlock(stmts []ast.Stmt) {
	saved := a.currentScope
	a.currentScope = NewScope(saved)
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
	}
	a.currentScope = saved
}

func (a *Analyzer) analyzeBlockInScope(stmts []ast.Stmt, scope *Scope) {
	saved := a.currentScope
	a.currentScope = scope
	for _, stmt := range stmts {
		a.analyzeStmt(stmt)
	}
	a.currentScope = saved
}

func (a *Analyzer) refinedScopeForCondition(parent *Scope, cond ast.Expr, truthy bool) *Scope {
	scope := NewScope(parent)
	a.applyConditionRefinements(scope, cond, truthy)
	return scope
}

func (a *Analyzer) applyConditionRefinements(scope *Scope, expr ast.Expr, truthy bool) {
	switch n := expr.(type) {
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			if truthy {
				a.applyConditionRefinements(scope, n.Left, true)
				a.applyConditionRefinements(scope, n.Right, true)
			}
		case lexer.TOKEN_OR:
			if !truthy {
				a.applyConditionRefinements(scope, n.Left, false)
				a.applyConditionRefinements(scope, n.Right, false)
			}
		case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
			targetExpr, state, ok := refinedExprNullState(n, truthy)
			if ok {
				a.shadowRefinedExpr(scope, targetExpr, state)
			}
		}
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			a.applyConditionRefinements(scope, n.Operand, !truthy)
		}
	case *ast.ParenExpr:
		a.applyConditionRefinements(scope, n.Inner, truthy)
	}
}

func refinedExprNullState(expr *ast.BinaryExpr, truthy bool) (ast.Expr, RefState, bool) {
	_, leftNull := expr.Left.(*ast.NullLit)
	_, rightNull := expr.Right.(*ast.NullLit)

	targetExpr := ast.Expr(nil)
	switch {
	case rightNull:
		targetExpr = expr.Left
	case leftNull:
		targetExpr = expr.Right
	default:
		return nil, RefStateNullable, false
	}

	if _, ok := exprRefinementKey(targetExpr); !ok {
		return nil, RefStateNullable, false
	}

	if expr.Op == lexer.TOKEN_EQEQ {
		if truthy {
			return targetExpr, RefStateNull, true
		}
		return targetExpr, RefStateNonNull, true
	}
	if truthy {
		return targetExpr, RefStateNonNull, true
	}
	return targetExpr, RefStateNull, true
}

func (a *Analyzer) shadowRefinedExpr(scope *Scope, expr ast.Expr, state RefState) {
	if scope == nil {
		return
	}
	key, ok := exprRefinementKey(expr)
	if !ok {
		return
	}
	baseType := a.analyzeExprInScope(expr, scope)
	ref, ok := baseType.(*RefType)
	if !ok {
		return
	}
	if !refinementCompatible(ref.State, state) {
		return
	}
	scope.Refinements[key] = &RefType{Elem: ref.Elem, State: state}
}

func refinementCompatible(current, desired RefState) bool {
	switch desired {
	case RefStateNonNull:
		return current == RefStateNonNull || current == RefStateNullable
	case RefStateNull:
		return current == RefStateNull || current == RefStateNullable
	default:
		return true
	}
}

func exprRefinementKey(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.ParenExpr:
		return exprRefinementKey(n.Inner)
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := exprRefinementKey(n.Object)
		if !ok {
			return "", false
		}
		return base + "." + n.Field, true
	default:
		return "", false
	}
}

func (a *Analyzer) lookupRefinedExprType(expr ast.Expr) (Type, bool) {
	if a.currentScope == nil {
		return nil, false
	}
	key, ok := exprRefinementKey(expr)
	if !ok {
		return nil, false
	}
	return a.currentScope.LookupRefinement(key)
}

func (a *Analyzer) applyPostIfFallthroughRefinement(stmt *ast.IfStmt) {
	if a.currentScope == nil || len(stmt.Elifs) > 0 {
		return
	}
	if blockDefinitelyExits(stmt.Then) {
		a.applyConditionRefinements(a.currentScope, stmt.Cond, false)
	}
	if len(stmt.Else) > 0 && blockDefinitelyExits(stmt.Else) {
		a.applyConditionRefinements(a.currentScope, stmt.Cond, true)
	}
}

func blockDefinitelyExits(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	return stmtDefinitelyExits(stmts[len(stmts)-1])
}

func stmtDefinitelyExits(stmt ast.Stmt) bool {
	switch n := stmt.(type) {
	case *ast.ReturnStmt, *ast.PanicStmt, *ast.StaticErrorStmt:
		return true
	case *ast.IfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	case *ast.StaticIfStmt:
		if !blockDefinitelyExits(n.Then) {
			return false
		}
		for _, elif := range n.Elifs {
			if !blockDefinitelyExits(elif.Body) {
				return false
			}
		}
		return len(n.Else) > 0 && blockDefinitelyExits(n.Else)
	default:
		return false
	}
}

func (a *Analyzer) recordAssignmentRefinement(target ast.Expr, targetType Type, valueType Type) {
	if a.currentScope == nil {
		return
	}
	key, ok := exprRefinementKey(target)
	if !ok {
		return
	}
	refined := assignedRefinementType(targetType, valueType)
	if refined == nil {
		delete(a.currentScope.Refinements, key)
		return
	}
	a.currentScope.Refinements[key] = refined
}

func assignedRefinementType(targetType Type, valueType Type) Type {
	targetRef, ok := targetType.(*RefType)
	if !ok {
		return nil
	}
	if IsNullType(valueType) {
		return &RefType{Elem: targetRef.Elem, State: RefStateNull}
	}
	if valueRef, ok := valueType.(*RefType); ok {
		return &RefType{Elem: valueRef.Elem, State: valueRef.State}
	}
	return targetRef
}

func assertedCondition(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident.Name != "assert" {
		return nil, false
	}
	return call.Args[0], true
}

func (a *Analyzer) analyzeExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	result := a.analyzeExpr(expr)
	a.currentScope = saved
	return result
}

func (a *Analyzer) analyzeCondExpr(expr ast.Expr) Type {
	return a.analyzeCondExprInScope(expr, a.currentScope)
}

func (a *Analyzer) analyzeCondExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	defer func() { a.currentScope = saved }()

	switch n := expr.(type) {
	case *ast.ParenExpr:
		return a.analyzeCondExprInScope(n.Inner, scope)
	case *ast.UnaryExpr:
		if n.Op == lexer.TOKEN_NOT {
			operand := a.analyzeCondExprInScope(n.Operand, scope)
			if !IsBoolType(operand) {
				a.errorf(n.Pos(), "not operator requires bool operand")
			}
			return a.namedTypes["bool"]
		}
		return a.analyzeExpr(n)
	case *ast.BinaryExpr:
		switch n.Op {
		case lexer.TOKEN_AND:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, true))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		case lexer.TOKEN_OR:
			left := a.analyzeCondExprInScope(n.Left, scope)
			right := a.analyzeCondExprInScope(n.Right, a.refinedScopeForCondition(scope, n.Left, false))
			if !IsBoolType(left) || !IsBoolType(right) {
				a.errorf(n.Pos(), "logical operator requires bool operands")
			}
			return a.namedTypes["bool"]
		default:
			return a.analyzeExpr(n)
		}
	default:
		return a.analyzeExpr(expr)
	}
}

func (a *Analyzer) analyzeExpr(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		if t, ok := a.lookupRefinedExprType(n); ok {
			return t
		}
		if a.currentScope != nil {
			if sym, ok := a.currentScope.Lookup(n.Name); ok {
				return sym.Type
			}
		}
		if sym, ok := a.globalScope.Lookup(n.Name); ok {
			return sym.Type
		}
		a.errorf(n.Pos(), "undefined identifier %q", n.Name)
		return invalidType
	case *ast.IntLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				return t
			}
			switch n.Suffix {
			case "u":
				return a.namedTypes["usize"]
			case "i":
				return a.namedTypes["int"]
			}
		}
		return a.namedTypes["int"]
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull}
	case *ast.BoolLit:
		return a.namedTypes["bool"]
	case *ast.NullLit:
		return nullType
	case *ast.ZeroedLit:
		return invalidType
	case *ast.BinaryExpr:
		return a.analyzeBinaryExpr(n)
	case *ast.UnaryExpr:
		return a.analyzeUnaryExpr(n)
	case *ast.CallExpr:
		return a.analyzeCallExpr(n)
	case *ast.FieldExpr:
		if t, ok := a.lookupRefinedExprType(n); ok {
			return t
		}
		return a.analyzeFieldExpr(n)
	case *ast.IndexExpr:
		return a.analyzeIndexExpr(n)
	case *ast.CastExpr:
		src := a.analyzeExpr(n.Operand)
		dst := a.resolveType(n.Target)
		if !a.validCast(src, dst) {
			a.errorf(n.Pos(), "invalid cast from %s to %s", src.String(), dst.String())
		}
		return dst
	case *ast.SizeofExpr:
		a.resolveType(n.Type)
		return a.namedTypes["usize"]
	case *ast.TernaryExpr:
		condType := a.analyzeCondExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "ternary condition must be bool, got %s", condType.String())
		}
		left := a.analyzeExprInScope(n.Value, a.refinedScopeForCondition(a.currentScope, n.Cond, true))
		right := a.analyzeExprInScope(n.Alt, a.refinedScopeForCondition(a.currentScope, n.Cond, false))
		merged := MergeTypes(left, right)
		if IsInvalidType(merged) {
			a.errorf(n.Pos(), "ternary branches are incompatible: %s and %s", left.String(), right.String())
		}
		return merged
	case *ast.AddrOfExpr:
		inner := a.analyzeExpr(n.Operand)
		return &RefType{Elem: inner, State: RefStateNonNull}
	case *ast.StructLitExpr:
		if t, ok := a.namedTypes[n.Name]; ok {
			if st, ok := t.(*StructType); ok {
				return st
			}
		}
		a.errorf(n.Pos(), "unknown struct %q", n.Name)
		return invalidType
	case *ast.ParenExpr:
		return a.analyzeExpr(n.Inner)
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeBinaryExpr(expr *ast.BinaryExpr) Type {
	left := a.analyzeExpr(expr.Left)
	right := a.analyzeExpr(expr.Right)
	switch expr.Op {
	case lexer.TOKEN_AND, lexer.TOKEN_OR:
		if !IsBoolType(left) || !IsBoolType(right) {
			a.errorf(expr.Pos(), "logical operator requires bool operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_EQEQ, lexer.TOKEN_BANGEQ:
		if IsNumericType(left) && IsNumericType(right) {
			return a.namedTypes["bool"]
		}
		if !(AssignableTo(left, right) || AssignableTo(right, left) || (IsNullType(left) && isRefLike(right)) || (IsNullType(right) && isRefLike(left))) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left.String(), right.String())
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS:
		if lref, ok := left.(*RefType); ok && IsNumericType(right) {
			return lref
		}
		if expr.Op == lexer.TOKEN_PLUS {
			if rref, ok := right.(*RefType); ok && IsNumericType(left) {
				return rref
			}
		}
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	case lexer.TOKEN_STAR, lexer.TOKEN_SLASH,
		lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return CommonNumericType(left, right)
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeUnaryExpr(expr *ast.UnaryExpr) Type {
	operand := a.analyzeExpr(expr.Operand)
	switch expr.Op {
	case lexer.TOKEN_NOT:
		if !IsBoolType(operand) {
			a.errorf(expr.Pos(), "not operator requires bool operand")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_MINUS, lexer.TOKEN_TILDE:
		if !IsNumericType(operand) {
			a.errorf(expr.Pos(), "unary operator requires numeric operand")
		}
		return operand
	default:
		return invalidType
	}
}

func (a *Analyzer) analyzeCallExpr(expr *ast.CallExpr) Type {
	fnType := a.analyzeExpr(expr.Func)
	ft, ok := fnType.(*FuncType)
	if !ok {
		a.errorf(expr.Pos(), "cannot call non-function value of type %s", fnType.String())
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		return invalidType
	}
	if !ft.Variadic && len(expr.Args) != len(ft.Params) {
		a.errorf(expr.Pos(), "function %q expects %d arguments, got %d", ft.Name, len(ft.Params), len(expr.Args))
	}
	if ft.Variadic && len(expr.Args) < len(ft.Params) {
		a.errorf(expr.Pos(), "variadic function %q expects at least %d arguments, got %d", ft.Name, len(ft.Params), len(expr.Args))
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	limit := len(ft.Params)
	if len(expr.Args) < limit {
		limit = len(expr.Args)
	}
	for i := 0; i < len(expr.Args); i++ {
		argType := a.analyzeExpr(expr.Args[i])
		if i < limit {
			a.collectTypeBindings(ft.Params[i], argType, bindings, shapeBindings)
			expectedType := a.substituteType(ft.Params[i], bindings, shapeBindings)
			if !AssignableTo(expectedType, argType) {
				a.errorf(expr.Args[i].Pos(), "argument %d to %q expects %s, got %s", i+1, ft.Name, expectedType.String(), argType.String())
			}
		}
	}
	if ft.Return == nil {
		return a.namedTypes["void"]
	}
	a.bindFreshReturnShapes(ft, shapeBindings)
	return a.substituteType(ft.Return, bindings, shapeBindings)
}

func (a *Analyzer) collectTypeBindings(pattern, actual Type, bindings map[string]Type, shapeBindings map[string]Shape) {
	if pattern == nil || actual == nil {
		return
	}
	switch p := pattern.(type) {
	case *TypeParamType:
		if _, exists := bindings[p.Name]; !exists {
			bindings[p.Name] = actual
		}
	case *RefType:
		if act, ok := actual.(*RefType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
		}
	case *ArrayType:
		if act, ok := actual.(*ArrayType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
		}
	case *DArrayType:
		if act, ok := actual.(*DArrayType); ok {
			a.collectTypeBindings(p.Elem, act.Elem, bindings, shapeBindings)
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *DStrType:
		if act, ok := actual.(*DStrType); ok {
			a.collectShapeBinding(p.Shape, act.Shape, shapeBindings)
		}
	case *GenericInstanceType:
		if act, ok := actual.(*GenericInstanceType); ok && p.Name == act.Name && len(p.Args) == len(act.Args) {
			for i := range p.Args {
				a.collectTypeBindings(p.Args[i], act.Args[i], bindings, shapeBindings)
			}
		}
	case *FuncType:
		if act, ok := actual.(*FuncType); ok {
			limit := len(p.Params)
			if len(act.Params) < limit {
				limit = len(act.Params)
			}
			for i := 0; i < limit; i++ {
				a.collectTypeBindings(p.Params[i], act.Params[i], bindings, shapeBindings)
			}
			a.collectTypeBindings(p.Return, act.Return, bindings, shapeBindings)
		}
	}
}

func (a *Analyzer) collectShapeBinding(pattern, actual Shape, bindings map[string]Shape) {
	param, ok := pattern.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; !exists {
		bindings[param.Name] = actual
	}
}

func (a *Analyzer) matchReturnType(actual Type) Type {
	if a.currentReturn == nil || actual == nil {
		return a.currentReturn
	}
	bindings := map[string]Type{}
	shapeBindings := map[string]Shape{}
	a.collectTypeBindings(a.currentReturn, actual, bindings, shapeBindings)
	return a.substituteType(a.currentReturn, bindings, shapeBindings)
}

func (a *Analyzer) bindFreshReturnShapes(fn *FuncType, bindings map[string]Shape) {
	if fn == nil || fn.Return == nil {
		return
	}
	spec, ok := shapeTransformTable[fn.Name]
	if !ok || !spec.FreshReturnShapes {
		return
	}
	a.bindFreshShapesInType(fn.Return, bindings)
}

func (a *Analyzer) bindFreshShapesInType(t Type, bindings map[string]Shape) {
	if t == nil {
		return
	}
	switch n := t.(type) {
	case *RefType:
		a.bindFreshShapesInType(n.Elem, bindings)
	case *ArrayType:
		a.bindFreshShapesInType(n.Elem, bindings)
	case *DArrayType:
		a.bindFreshShape(n.Shape, bindings)
		a.bindFreshShapesInType(n.Elem, bindings)
	case *DStrType:
		a.bindFreshShape(n.Shape, bindings)
	case *GenericInstanceType:
		for _, arg := range n.Args {
			a.bindFreshShapesInType(arg, bindings)
		}
	case *FuncType:
		for _, param := range n.Params {
			a.bindFreshShapesInType(param, bindings)
		}
		a.bindFreshShapesInType(n.Return, bindings)
	}
}

func (a *Analyzer) bindFreshShape(shape Shape, bindings map[string]Shape) {
	param, ok := shape.(*ShapeParam)
	if !ok {
		return
	}
	if _, exists := bindings[param.Name]; exists {
		return
	}
	a.freshShapeCounter++
	bindings[param.Name] = &FreshShape{ID: a.freshShapeCounter, Label: param.Name}
}

func (a *Analyzer) analyzeFieldExpr(expr *ast.FieldExpr) Type {
	field, ok := a.lookupField(a.analyzeExpr(expr.Object), expr.Field, expr.Pos())
	if !ok {
		return invalidType
	}
	return field.Type
}

func (a *Analyzer) analyzeIndexExpr(expr *ast.IndexExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	indexType := a.analyzeExpr(expr.Index)
	if !IsNumericType(indexType) {
		a.errorf(expr.Index.Pos(), "index must be numeric, got %s", indexType.String())
	}
	if arr, ok := objType.(*ArrayType); ok {
		a.checkConstantArrayIndexBounds(arr, expr.Index)
		return arr.Elem
	}
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(expr.Pos(), "indexing requires proven non-null reference, got %s", objType.String())
			return invalidType
		}
		if arr, ok := ref.Elem.(*ArrayType); ok {
			a.checkConstantArrayIndexBounds(arr, expr.Index)
			return arr.Elem
		}
		return ref.Elem
	}
	a.errorf(expr.Pos(), "indexing requires array or reference type, got %s", objType.String())
	return invalidType
}

func (a *Analyzer) assignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, ok = a.globalScope.Lookup(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			if ref, ok := sym.Type.(*RefType); ok {
				return ref.Elem
			}
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
			return sym.Type
		}
		if a.currentScope != nil {
			if current, exists := a.currentScope.Symbols[n.Name]; exists && current == sym && a.currentScope.Parent != nil {
				if parent, ok := a.currentScope.Parent.Lookup(n.Name); ok && parent.Node == sym.Node && parent.Kind == sym.Kind && parent.Mutable {
					return parent.Type
				}
			}
		}
		return sym.Type
	case *ast.FieldExpr:
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		return field.Type
	case *ast.IndexExpr:
		return a.analyzeIndexExpr(n)
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) asRefTargetType(expr ast.Expr, asKind string) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		var (
			sym *Symbol
			ok  bool
		)
		if a.currentScope != nil {
			sym, ok = a.currentScope.Lookup(n.Name)
		}
		if !ok {
			if sym, ok = a.globalScope.Lookup(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
		}
		return a.refTypeWithAsKind(sym.Type, asKind)
	case *ast.FieldExpr:
		field, ok := a.lookupField(a.analyzeExpr(n.Object), n.Field, n.Pos())
		if !ok {
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q is immutable", n.Field)
		}
		return a.refTypeWithAsKind(field.Type, asKind)
	case *ast.IndexExpr:
		return a.refTypeWithAsKind(a.analyzeIndexExpr(n), asKind)
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
	}
}

func (a *Analyzer) refTypeWithAsKind(t Type, asKind string) Type {
	ref, ok := t.(*RefType)
	if !ok {
		return t
	}
	switch asKind {
	case "&":
		return &RefType{Elem: ref.Elem, State: RefStateNonNull}
	case "!":
		return &RefType{Elem: ref.Elem, State: RefStateNull}
	default:
		return t
	}
}

func (a *Analyzer) lookupField(objType Type, fieldName string, pos lexer.Pos) (Field, bool) {
	if ref, ok := objType.(*RefType); ok {
		if ref.State != RefStateNonNull {
			a.errorf(pos, "field access requires proven non-null reference, got %s", objType.String())
			return Field{}, false
		}
		objType = ref.Elem
	}
	switch t := objType.(type) {
	case *StructType:
		field, ok := t.Fields[fieldName]
		if !ok {
			a.errorf(pos, "struct %q has no field %q", t.Name, fieldName)
			return Field{}, false
		}
		return field, true
	case *GenericInstanceType:
		baseStruct, ok := t.Base.(*StructType)
		if !ok {
			a.errorf(pos, "field access requires struct type, got %s", objType.String())
			return Field{}, false
		}
		field, ok := baseStruct.Fields[fieldName]
		if !ok {
			a.errorf(pos, "struct %q has no field %q", baseStruct.Name, fieldName)
			return Field{}, false
		}
		bindings := map[string]Type{}
		for i, name := range baseStruct.TypeParams {
			if i < len(t.Args) {
				bindings[name] = t.Args[i]
			}
		}
		field.Type = a.substituteType(field.Type, bindings, nil)
		return field, true
	default:
		a.errorf(pos, "field access requires struct type, got %s", objType.String())
		return Field{}, false
	}
}

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
	return &FuncType{Name: name, TypeParams: append([]string(nil), typeParams...), ShapeParams: shapeParams, Params: ptypes, Return: retType, Variadic: variadic}
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
	case *ast.RefType:
		return &RefType{Elem: a.resolveType(n.Elem), State: RefState(n.State)}
	case *ast.ArrayType:
		return a.resolveArrayType(n)
	case *ast.MutableType:
		return a.resolveType(n.Elem)
	case *ast.TailType:
		return &RefType{Elem: a.resolveType(n.Elem), State: RefStateNonNull}
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

func (a *Analyzer) resolveDynamicShapeType(expr *ast.GenericType) (Type, bool) {
	switch expr.Name {
	case "DArray":
		if len(expr.Args) != 2 {
			a.errorf(expr.Pos(), "DArray expects 2 arguments, got %d", len(expr.Args))
			return invalidType, true
		}
		return &DArrayType{Elem: a.resolveType(expr.Args[0]), Shape: a.resolveShapeArg(expr.Args[1])}, true
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
		return &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull}
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
	case *RefType:
		return &RefType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), State: n.State}
	case *ArrayType:
		return &ArrayType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), Size: n.Size, HasConstSize: n.HasConstSize, ConstSize: n.ConstSize}
	case *DArrayType:
		return &DArrayType{Elem: a.substituteType(n.Elem, bindings, shapeBindings), Shape: a.substituteShape(n.Shape, shapeBindings)}
	case *DStrType:
		return &DStrType{Shape: a.substituteShape(n.Shape, shapeBindings)}
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
		return &FuncType{Name: n.Name, TypeParams: append([]string(nil), n.TypeParams...), ShapeParams: append([]string(nil), n.ShapeParams...), Params: params, Return: a.substituteType(n.Return, bindings, shapeBindings), Variadic: n.Variadic}
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
	case *ast.RefType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.MutableType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.TailType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
	case *ast.ArrayType:
		a.collectImplicitShapeParamsFromType(n.Elem, seen, order)
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
		case "DStr":
			if len(n.Args) > 0 {
				if name, ok := shapeNameFromTypeExpr(n.Args[0]); ok && isImplicitShapeWitnessName(name) && !seen[name] {
					seen[name] = true
					*order = append(*order, name)
				}
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

func (a *Analyzer) resolveArrayType(expr *ast.ArrayType) Type {
	arr := &ArrayType{Elem: a.resolveType(expr.Elem), Size: a.exprSummary(expr.Size)}
	value, ok := a.evalConstExpr(expr.Size)
	if !ok || value.Kind != ConstInt {
		a.errorf(expr.Size.Pos(), "array size must be a compile-time integer")
		return arr
	}
	if value.Int < 0 {
		a.errorf(expr.Size.Pos(), "array size must be non-negative, got %d", value.Int)
		return arr
	}
	arr.HasConstSize = true
	arr.ConstSize = value.Int
	return arr
}

func (a *Analyzer) checkConstantArrayIndexBounds(arr *ArrayType, indexExpr ast.Expr) {
	if arr == nil || !arr.HasConstSize {
		return
	}
	value, ok := a.evalConstExpr(indexExpr)
	if !ok || value.Kind != ConstInt {
		return
	}
	if value.Int < 0 || value.Int >= arr.ConstSize {
		a.errorf(indexExpr.Pos(), "constant index %d out of bounds for %s", value.Int, arr.String())
	}
}

func (a *Analyzer) evalConstBoolExpr(expr ast.Expr) (bool, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstBool {
		return false, false
	}
	return value.Bool, true
}

func (a *Analyzer) evalConstStringExpr(expr ast.Expr) (string, bool) {
	value, ok := a.evalConstExpr(expr)
	if !ok || value.Kind != ConstString {
		return "", false
	}
	return value.String, true
}

func (a *Analyzer) evalConstExpr(expr ast.Expr) (ConstValue, bool) {
	switch n := expr.(type) {
	case *ast.IntLit:
		value, ok := ParseIntLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: value}, true
	case *ast.BoolLit:
		return ConstValue{Kind: ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return ConstValue{Kind: ConstString, String: n.Value}, true
	case *ast.Ident:
		value, ok := a.constValues[n.Name]
		return value, ok
	case *ast.ParenExpr:
		return a.evalConstExpr(n.Inner)
	case *ast.UnaryExpr:
		operand, ok := a.evalConstExpr(n.Operand)
		if !ok {
			return ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_NOT:
			if operand.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: !operand.Bool}, true
		case lexer.TOKEN_MINUS:
			if operand.Kind != ConstInt {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: -operand.Int}, true
		case lexer.TOKEN_TILDE:
			if operand.Kind != ConstInt {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: ^operand.Int}, true
		default:
			return ConstValue{}, false
		}
	case *ast.BinaryExpr:
		left, ok := a.evalConstExpr(n.Left)
		if !ok {
			return ConstValue{}, false
		}
		right, ok := a.evalConstExpr(n.Right)
		if !ok {
			return ConstValue{}, false
		}
		switch n.Op {
		case lexer.TOKEN_AND:
			if left.Kind != ConstBool || right.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: left.Bool && right.Bool}, true
		case lexer.TOKEN_OR:
			if left.Kind != ConstBool || right.Kind != ConstBool {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstBool, Bool: left.Bool || right.Bool}, true
		case lexer.TOKEN_EQEQ:
			return a.evalConstEquality(left, right, true)
		case lexer.TOKEN_BANGEQ:
			return a.evalConstEquality(left, right, false)
		case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ,
			lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH,
			lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
			lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
			if left.Kind != ConstInt || right.Kind != ConstInt {
				return ConstValue{}, false
			}
			return evalConstIntBinary(n.Op, left.Int, right.Int)
		default:
			return ConstValue{}, false
		}
	case *ast.TernaryExpr:
		cond, ok := a.evalConstBoolExpr(n.Cond)
		if !ok {
			return ConstValue{}, false
		}
		if cond {
			return a.evalConstExpr(n.Value)
		}
		return a.evalConstExpr(n.Alt)
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) evalConstEquality(left, right ConstValue, equal bool) (ConstValue, bool) {
	matched := false
	switch {
	case left.Kind == ConstInt && right.Kind == ConstInt:
		matched = left.Int == right.Int
	case left.Kind == ConstBool && right.Kind == ConstBool:
		matched = left.Bool == right.Bool
	case left.Kind == ConstString && right.Kind == ConstString:
		matched = left.String == right.String
	default:
		return ConstValue{}, false
	}
	if !equal {
		matched = !matched
	}
	return ConstValue{Kind: ConstBool, Bool: matched}, true
}

func evalConstIntBinary(op lexer.TokenKind, left, right int64) (ConstValue, bool) {
	switch op {
	case lexer.TOKEN_LT:
		return ConstValue{Kind: ConstBool, Bool: left < right}, true
	case lexer.TOKEN_GT:
		return ConstValue{Kind: ConstBool, Bool: left > right}, true
	case lexer.TOKEN_LTEQ:
		return ConstValue{Kind: ConstBool, Bool: left <= right}, true
	case lexer.TOKEN_GTEQ:
		return ConstValue{Kind: ConstBool, Bool: left >= right}, true
	case lexer.TOKEN_PLUS:
		return ConstValue{Kind: ConstInt, Int: left + right}, true
	case lexer.TOKEN_MINUS:
		return ConstValue{Kind: ConstInt, Int: left - right}, true
	case lexer.TOKEN_STAR:
		return ConstValue{Kind: ConstInt, Int: left * right}, true
	case lexer.TOKEN_SLASH:
		if right == 0 {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: left / right}, true
	case lexer.TOKEN_CARET:
		return ConstValue{Kind: ConstInt, Int: left ^ right}, true
	case lexer.TOKEN_PIPE:
		return ConstValue{Kind: ConstInt, Int: left | right}, true
	case lexer.TOKEN_AMPERSAND:
		return ConstValue{Kind: ConstInt, Int: left & right}, true
	case lexer.TOKEN_LSHIFT:
		return ConstValue{Kind: ConstInt, Int: left << right}, true
	case lexer.TOKEN_RSHIFT:
		return ConstValue{Kind: ConstInt, Int: left >> right}, true
	default:
		return ConstValue{}, false
	}
}

func (a *Analyzer) errorf(pos lexer.Pos, format string, args ...interface{}) {
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Message: fmt.Sprintf(format, args...)})
}

func isNullableRef(t Type) bool {
	r, ok := t.(*RefType)
	return ok && r.State == RefStateNullable
}

func isRefLike(t Type) bool {
	_, ok := t.(*RefType)
	return ok
}

func ParseIntLiteral(expr *ast.IntLit) (int64, bool) {
	base := 10
	text := expr.Value
	if expr.IsHex {
		base = 0
	}
	v, err := strconv.ParseInt(text, base, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
