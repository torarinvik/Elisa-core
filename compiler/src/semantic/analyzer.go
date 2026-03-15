package semantic

import (
	"fmt"
	"strconv"

	"llcontext/src/ast"
	"llcontext/src/lexer"
)

type Analyzer struct {
	file          *ast.File
	diagnostics   []Diagnostic
	namedTypes    map[string]Type
	globalScope   *Scope
	functionTypes map[string]*FuncType
	currentScope  *Scope
	currentReturn Type
}

func Analyze(file *ast.File) *Result {
	a := &Analyzer{
		file:          file,
		namedTypes:    map[string]Type{},
		globalScope:   NewScope(nil),
		functionTypes: map[string]*FuncType{},
	}
	a.registerBuiltins()
	a.collectNamedTypes()
	a.populateStructFields()
	a.collectValueSymbols()
	a.analyzeDecls()
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

func (a *Analyzer) collectNamedTypes() {
	for _, decl := range a.file.Decls {
		switch n := decl.(type) {
		case *ast.StructDecl:
			if _, exists := a.namedTypes[n.Name]; exists {
				a.errorf(n.Pos(), "duplicate type %q", n.Name)
				continue
			}
			st := &StructType{Name: n.Name, Fields: map[string]Field{}, ReprC: n.ReprC, Decl: n}
			a.namedTypes[n.Name] = st
		case *ast.ExternTypeDecl:
			if _, exists := a.namedTypes[n.Name]; exists {
				a.errorf(n.Pos(), "duplicate type %q", n.Name)
				continue
			}
			a.namedTypes[n.Name] = &OpaqueType{Name: n.Name}
		case *ast.StaticIfDecl:
			a.errorf(n.Pos(), "static if is not supported in semantic MVP yet")
		}
	}
}

func (a *Analyzer) populateStructFields() {
	for _, decl := range a.file.Decls {
		stDecl, ok := decl.(*ast.StructDecl)
		if !ok {
			continue
		}
		st, _ := a.namedTypes[stDecl.Name].(*StructType)
		if st == nil {
			continue
		}
		for _, field := range stDecl.Fields {
			if _, exists := st.Fields[field.Name]; exists {
				a.errorf(field.Position, "duplicate field %q in struct %q", field.Name, stDecl.Name)
				continue
			}
			st.Fields[field.Name] = Field{
				Name:    field.Name,
				Type:    a.resolveType(field.Type),
				Mutable: field.Mutable,
				IsTail:  field.IsTail,
			}
		}
	}
}

func (a *Analyzer) collectValueSymbols() {
	for _, decl := range a.file.Decls {
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
			fnType := a.funcTypeFromDecl(n.Name, n.Params, n.ReturnType, false)
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternFuncDecl:
			fnType := a.funcTypeFromDecl(n.Name, n.Params, n.ReturnType, n.Variadic)
			a.functionTypes[n.Name] = fnType
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolExternFunc, Type: fnType, Node: n, Mutable: false}, n.Pos())
		case *ast.ExternVarDecl:
			declType := a.resolveType(n.Type)
			a.defineGlobal(&Symbol{Name: n.Name, Kind: SymbolExternVar, Type: declType, Node: n, Mutable: true}, n.Pos())
		}
	}
}

func (a *Analyzer) analyzeDecls() {
	for _, decl := range a.file.Decls {
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
		case *ast.StaticIfDecl:
			// already diagnosed during discovery phase
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
	for i, param := range fn.Params {
		var ptype Type = invalidType
		if fnType != nil && i < len(fnType.Params) {
			ptype = fnType.Params[i]
		}
		a.defineLocal(&Symbol{Name: param.Name, Kind: SymbolParam, Type: ptype, Node: fn, Mutable: param.Mutable}, param.Position)
	}
	for _, stmt := range fn.Body {
		a.analyzeStmt(stmt)
	}
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
	case *ast.AugAssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeExpr(n.Value)
		if !IsNumericType(targetType) || !IsNumericType(valueType) {
			a.errorf(n.Pos(), "augmented assignment requires numeric operands")
		}
	case *ast.AsRefAssignStmt:
		targetType := a.assignmentTargetType(n.Target)
		valueType := a.analyzeExpr(n.Value)
		if !AssignableTo(targetType, valueType) {
			a.errorf(n.Pos(), "cannot assign %s to %s", valueType.String(), targetType.String())
		}
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
		if !AssignableTo(a.currentReturn, valueType) {
			a.errorf(n.Pos(), "return type expects %s, got %s", a.currentReturn.String(), valueType.String())
		}
	case *ast.IfStmt:
		condType := a.analyzeExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "if condition must be bool, got %s", condType.String())
		}
		a.analyzeBlock(n.Then)
		for _, elif := range n.Elifs {
			elifType := a.analyzeExpr(elif.Cond)
			if !IsBoolType(elifType) {
				a.errorf(elif.Position, "elif condition must be bool, got %s", elifType.String())
			}
			a.analyzeBlock(elif.Body)
		}
		a.analyzeBlock(n.Else)
	case *ast.WhileStmt:
		condType := a.analyzeExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "while condition must be bool, got %s", condType.String())
		}
		a.analyzeBlock(n.Body)
	case *ast.PassStmt:
		return
	case *ast.PanicStmt:
		a.analyzeExpr(n.Message)
	case *ast.ExprStmt:
		a.analyzeExpr(n.Expr)
	case *ast.StaticIfStmt:
		a.errorf(n.Pos(), "static if is not supported in semantic MVP yet")
	case *ast.StaticErrorStmt:
		a.errorf(n.Pos(), "static error is not supported in semantic MVP yet")
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

func (a *Analyzer) analyzeExprInScope(expr ast.Expr, scope *Scope) Type {
	saved := a.currentScope
	a.currentScope = scope
	result := a.analyzeExpr(expr)
	a.currentScope = saved
	return result
}

func (a *Analyzer) analyzeExpr(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.Ident:
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
		}
		return a.namedTypes["int"]
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], Nullable: false}
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
		condType := a.analyzeExpr(n.Cond)
		if !IsBoolType(condType) {
			a.errorf(n.Pos(), "ternary condition must be bool, got %s", condType.String())
		}
		left := a.analyzeExpr(n.Value)
		right := a.analyzeExpr(n.Alt)
		merged := MergeTypes(left, right)
		if IsInvalidType(merged) {
			a.errorf(n.Pos(), "ternary branches are incompatible: %s and %s", left.String(), right.String())
		}
		return merged
	case *ast.AddrOfExpr:
		inner := a.analyzeExpr(n.Operand)
		return &RefType{Elem: inner, Nullable: false}
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
		if !(AssignableTo(left, right) || AssignableTo(right, left) || (IsNullType(left) && isNullableRef(right)) || (IsNullType(right) && isNullableRef(left))) {
			a.errorf(expr.Pos(), "cannot compare %s and %s", left.String(), right.String())
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_LT, lexer.TOKEN_GT, lexer.TOKEN_LTEQ, lexer.TOKEN_GTEQ:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "comparison requires numeric operands")
		}
		return a.namedTypes["bool"]
	case lexer.TOKEN_PLUS, lexer.TOKEN_MINUS, lexer.TOKEN_STAR, lexer.TOKEN_SLASH,
		lexer.TOKEN_CARET, lexer.TOKEN_PIPE, lexer.TOKEN_AMPERSAND,
		lexer.TOKEN_LSHIFT, lexer.TOKEN_RSHIFT:
		if !IsNumericType(left) || !IsNumericType(right) {
			a.errorf(expr.Pos(), "operator requires numeric operands")
			return invalidType
		}
		return left
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
	limit := len(ft.Params)
	if len(expr.Args) < limit {
		limit = len(expr.Args)
	}
	for i := 0; i < len(expr.Args); i++ {
		argType := a.analyzeExpr(expr.Args[i])
		if i < limit && !AssignableTo(ft.Params[i], argType) {
			a.errorf(expr.Args[i].Pos(), "argument %d to %q expects %s, got %s", i+1, ft.Name, ft.Params[i].String(), argType.String())
		}
	}
	if ft.Return == nil {
		return a.namedTypes["void"]
	}
	return ft.Return
}

func (a *Analyzer) analyzeFieldExpr(expr *ast.FieldExpr) Type {
	objType := a.analyzeExpr(expr.Object)
	if ref, ok := objType.(*RefType); ok {
		objType = ref.Elem
	}
	st, ok := objType.(*StructType)
	if !ok {
		a.errorf(expr.Pos(), "field access requires struct type, got %s", objType.String())
		return invalidType
	}
	field, ok := st.Fields[expr.Field]
	if !ok {
		a.errorf(expr.Pos(), "struct %q has no field %q", st.Name, expr.Field)
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
		return arr.Elem
	}
	if ref, ok := objType.(*RefType); ok {
		if arr, ok := ref.Elem.(*ArrayType); ok {
			return arr.Elem
		}
	}
	a.errorf(expr.Pos(), "indexing requires array type, got %s", objType.String())
	return invalidType
}

func (a *Analyzer) assignmentTargetType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.Ident:
		sym, ok := a.currentScope.Lookup(n.Name)
		if !ok {
			if sym, ok = a.globalScope.Lookup(n.Name); !ok {
				a.errorf(n.Pos(), "undefined assignment target %q", n.Name)
				return invalidType
			}
		}
		if !sym.Mutable {
			a.errorf(n.Pos(), "cannot assign to immutable %s %q", sym.Kind, sym.Name)
		}
		return sym.Type
	case *ast.FieldExpr:
		objType := a.analyzeExpr(n.Object)
		if ref, ok := objType.(*RefType); ok {
			objType = ref.Elem
		}
		st, ok := objType.(*StructType)
		if !ok {
			a.errorf(n.Pos(), "field assignment requires struct type, got %s", objType.String())
			return invalidType
		}
		field, ok := st.Fields[n.Field]
		if !ok {
			a.errorf(n.Pos(), "struct %q has no field %q", st.Name, n.Field)
			return invalidType
		}
		if !field.Mutable {
			a.errorf(n.Pos(), "field %q on %s is immutable", n.Field, st.Name)
		}
		return field.Type
	case *ast.IndexExpr:
		return a.analyzeIndexExpr(n)
	default:
		a.errorf(expr.Pos(), "invalid assignment target")
		return invalidType
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

func (a *Analyzer) funcTypeFromDecl(name string, params []ast.ParamDecl, ret ast.TypeExpr, variadic bool) *FuncType {
	ptypes := make([]Type, 0, len(params))
	for _, p := range params {
		ptypes = append(ptypes, a.resolveType(p.Type))
	}
	retType := a.namedTypes["void"]
	if ret != nil {
		retType = a.resolveType(ret)
	}
	return &FuncType{Name: name, Params: ptypes, Return: retType, Variadic: variadic}
}

func (a *Analyzer) resolveType(expr ast.TypeExpr) Type {
	switch n := expr.(type) {
	case *ast.NamedType:
		if t, ok := a.namedTypes[n.Name]; ok {
			return t
		}
		a.errorf(n.Pos(), "unknown type %q", n.Name)
		return invalidType
	case *ast.RefType:
		return &RefType{Elem: a.resolveType(n.Elem), Nullable: n.Nullable}
	case *ast.ArrayType:
		return &ArrayType{Elem: a.resolveType(n.Elem), Size: a.exprSummary(n.Size)}
	case *ast.MutableType:
		return a.resolveType(n.Elem)
	case *ast.TailType:
		return a.resolveType(n.Elem)
	case *ast.GenericType:
		a.errorf(n.Pos(), "generic types are not supported in semantic MVP yet")
		return invalidType
	default:
		return invalidType
	}
}

func (a *Analyzer) inferLiteralType(expr ast.Expr) Type {
	switch n := expr.(type) {
	case *ast.IntLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				return t
			}
		}
		return a.namedTypes["int"]
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], Nullable: false}
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
	if IsNumericType(src) && IsNumericType(dst) {
		return true
	}
	if _, ok := src.(*RefType); ok {
		_, ok = dst.(*RefType)
		return ok
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

func (a *Analyzer) errorf(pos lexer.Pos, format string, args ...interface{}) {
	a.diagnostics = append(a.diagnostics, Diagnostic{Pos: pos, Message: fmt.Sprintf(format, args...)})
}

func isNullableRef(t Type) bool {
	r, ok := t.(*RefType)
	return ok && r.Nullable
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
