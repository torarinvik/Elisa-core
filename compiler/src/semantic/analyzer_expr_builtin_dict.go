package semantic

import (
	"fmt"
	"llcontext/src/ast"
	"llcontext/src/lexer"
)

func builtinDictEntryValueRefType(dictType *DictType) *RefType {
	if dictType == nil {
		return &RefType{Elem: invalidType, Mutable: true, State: RefStateNullable, Storage: RefStorageAny, ExplicitStorage: true}
	}
	return &RefType{Elem: dictType.Value, Mutable: true, State: RefStateNullable, Storage: RefStorageAny, ExplicitStorage: true}
}

func builtinDictEntryReceiverType(t Type) (*DictEntryType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if entryType, ok := StripAggregateStateType(t).(*DictEntryType); ok && entryType != nil {
		return entryType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	entryType, ok := StripAggregateStateType(refType.Elem).(*DictEntryType)
	if !ok || entryType == nil {
		return nil, nil, false
	}
	return entryType, refType, true
}

func builtinDictReceiverType(t Type) (*DictType, *RefType, bool) {
	if t == nil {
		return nil, nil, false
	}
	if dictType, ok := t.(*DictType); ok && dictType != nil {
		return dictType, nil, true
	}
	refType, ok := t.(*RefType)
	if !ok || refType == nil || refType.Elem == nil {
		return nil, nil, false
	}
	dictType, ok := refType.Elem.(*DictType)
	if !ok || dictType == nil {
		return nil, nil, false
	}
	return dictType, refType, true
}

func builtinDictReceiverTypeExpr(pos lexer.Pos, dictType *DictType, mutable bool) ast.TypeExpr {
	if dictType == nil {
		return refToTypeExprWithStorage(&ast.NamedType{Position: pos, Name: "void"}, false, ast.RefStorageAny)
	}
	keyExpr := astTypeExprForBuiltinMethodRewrite(pos, dictType.Key)
	valueExpr := astTypeExprForBuiltinMethodRewrite(pos, dictType.Value)
	elem := &ast.BuiltinTypeExpr{
		Position: pos,
		Name:     "dict",
		TypeArgs: []ast.TypeExpr{keyExpr, valueExpr},
	}
	if mutable {
		elem = &ast.BuiltinTypeExpr{
			Position: pos,
			Name:     "dict",
			TypeArgs: []ast.TypeExpr{keyExpr, valueExpr},
		}
		return refToTypeExprWithStorage(&ast.MutableType{Position: pos, Elem: elem}, false, ast.RefStorageAny)
	}
	return refToTypeExprWithStorage(elem, false, ast.RefStorageAny)
}

func astTypeExprForBuiltinMethodRewrite(pos lexer.Pos, typ Type) ast.TypeExpr {
	switch t := StripAggregateStateType(typ).(type) {
	case *BuiltinType:
		return &ast.NamedType{Position: pos, Name: t.Name}
	case *OptionalType:
		return &ast.OptionalTypeExpr{Position: pos, Value: astTypeExprForBuiltinMethodRewrite(pos, t.Value)}
	case *RefType:
		elem := astTypeExprForBuiltinMethodRewrite(pos, t.Elem)
		if t.Mutable {
			elem = &ast.MutableType{Position: pos, Elem: elem}
		}
		return refToTypeExprWithStorage(elem, t.State != RefStateNonNull, ast.RefStorage(t.Storage))
	case *DStrType:
		if isWildcardShape(t.Shape) {
			return &ast.BuiltinTypeExpr{Position: pos, Name: "cstr"}
		}
		return &ast.BuiltinTypeExpr{Position: pos, Name: "cstr", ValueArgs: []ast.Expr{&ast.Ident{Position: pos, Name: t.Shape.String()}}}
	case *DictType:
		return &ast.BuiltinTypeExpr{
			Position: pos,
			Name:     "dict",
			TypeArgs: []ast.TypeExpr{
				astTypeExprForBuiltinMethodRewrite(pos, t.Key),
				astTypeExprForBuiltinMethodRewrite(pos, t.Value),
			},
		}
	default:
		return &ast.NamedType{Position: pos, Name: typ.String()}
	}
}

func runtimeBackedDictSupportDiagnostic(dictType *DictType) string {
	if dictType == nil {
		return "runtime-backed dict operations currently support only dict[cstr, V]"
	}
	return fmt.Sprintf("runtime-backed dict operations currently support only dict[cstr, V], got %s", diagnosticTypeString(dictType))
}

func (a *Analyzer) ensureRuntimeBackedDictSupported(pos lexer.Pos, dictType *DictType) bool {
	if dictSupportsRuntimeBackedOps(dictType) {
		return true
	}
	a.errorf(pos, "%s", runtimeBackedDictSupportDiagnostic(dictType))
	return false
}

type builtinDictMethodRewriteStatus int

const (
	builtinDictMethodRewriteNone builtinDictMethodRewriteStatus = iota
	builtinDictMethodRewriteApplied
	builtinDictMethodRewriteInvalid
)

func dictMethodReceiverExpr(receiver ast.Expr, receiverType Type, mutable bool) ast.Expr {
	if _, ok := receiverType.(*RefType); ok {
		return receiver
	}
	dictType, _, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return &ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver}
	}
	return &ast.CastExpr{
		Position: receiver.Pos(),
		Operand:  &ast.AddrOfExpr{Position: receiver.Pos(), Operand: receiver},
		Target:   builtinDictReceiverTypeExpr(receiver.Pos(), dictType, mutable),
		Origin:   ast.CastExprOriginGeneral,
	}
}

func (a *Analyzer) rewriteBuiltinDictMethodCall(expr *ast.CallExpr) builtinDictMethodRewriteStatus {
	if a == nil || expr == nil {
		return builtinDictMethodRewriteNone
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Object == nil {
		return builtinDictMethodRewriteNone
	}
	if a.exprResolvesToTypePath(fieldExpr.Object) {
		return builtinDictMethodRewriteNone
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	dictType, receiverRefType, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return builtinDictMethodRewriteNone
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), dictType) {
		return builtinDictMethodRewriteInvalid
	}
	method := fieldExpr.Field
	helperName := ""
	mutates := false
	needsAlloc := false
	switch method {
	case "get":
		helperName = "arena_dict_get"
	case "contains":
		helperName = "arena_dict_contains"
	case "remove":
		helperName = "arena_dict_remove"
		mutates = true
	case "clear":
		helperName = "arena_dict_clear"
		mutates = true
	case "reserve":
		helperName = "arena_dict_reserve"
		mutates = true
		needsAlloc = true
	case "put":
		helperName = "arena_dict_put"
		mutates = true
		needsAlloc = true
	case "get_or_insert":
		helperName = "arena_dict_get_or_insert"
		mutates = true
		needsAlloc = true
	default:
		return builtinDictMethodRewriteNone
	}
	if mutates && receiverRefType == nil && !a.exprCanYieldWritableRef(fieldExpr.Object) {
		a.errorf(fieldExpr.Object.Pos(), "dict %s requires a mutable dict receiver", method)
		return builtinDictMethodRewriteInvalid
	}
	if needsAlloc {
		if a.currentAllocExpr == nil {
			a.errorf(expr.Pos(), "dict %s requires an active in <arena>: scope", method)
			return builtinDictMethodRewriteInvalid
		}
	}
	rewrittenArgs := make([]ast.Expr, 0, len(expr.Args)+2)
	if needsAlloc {
		rewrittenArgs = append(rewrittenArgs, a.currentAllocExpr)
	}
	rewrittenArgs = append(rewrittenArgs, dictMethodReceiverExpr(fieldExpr.Object, receiverType, mutates))
	rewrittenArgs = append(rewrittenArgs, expr.Args...)
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: helperName}
	expr.Args = rewrittenArgs
	expr.ArgNames = nil
	return builtinDictMethodRewriteApplied
}

func (a *Analyzer) analyzeBuiltinDictEntryCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "entry" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	dictType, receiverRefType, ok := builtinDictReceiverType(receiverType)
	if !ok || dictType == nil {
		return nil, false
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), dictType) {
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "dict entry expects 1 argument, got %d", len(expr.Args))
	}
	if len(expr.Args) >= 1 {
		keyType := a.analyzeValueExpr(expr.Args[0], dictType.Key)
		if !AssignableTo(dictType.Key, keyType) {
			a.errorf(expr.Args[0].Pos(), "dict entry expects key of type %s, got %s", dictType.Key, keyType)
		}
	}
	mutable := false
	if receiverRefType != nil {
		mutable = receiverRefType.Mutable
	} else {
		mutable = a.exprCanYieldWritableRef(fieldExpr.Object)
	}
	resultType := &DictEntryType{Dict: dictType, Mutable: mutable}
	a.exprTypes[expr.Func] = &FuncType{Name: "dict.entry", Params: []Type{receiverType, dictType.Key}, Return: resultType}
	a.exprTypes[expr] = resultType
	return resultType, true
}

func (a *Analyzer) analyzeBuiltinDictEntryInsertCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "insert" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	entryType, _, ok := builtinDictEntryReceiverType(receiverType)
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, false
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), entryType.Dict) {
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !entryType.Mutable {
		a.errorf(fieldExpr.Object.Pos(), "dict entry insert requires an entry created from a mutable dict receiver")
	}
	if a.currentAllocExpr == nil {
		a.errorf(expr.Pos(), "dict entry insert requires an active in <arena>: scope")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "dict entry insert expects 1 argument, got %d", len(expr.Args))
	}
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], entryType.Dict.Value)
		if !AssignableTo(entryType.Dict.Value, argType) {
			a.errorf(expr.Args[0].Pos(), "dict entry insert expects %s, got %s", entryType.Dict.Value, argType)
		}
		a.consumeAffineValueExpr(expr.Args[0], entryType.Dict.Value, "move into dict entry insert")
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	a.exprTypes[expr.Func] = &FuncType{Name: "dict.entry.insert", Params: []Type{receiverType, entryType.Dict.Value}, Return: valueRefType}
	a.exprTypes[expr] = valueRefType
	return valueRefType, true
}

func (a *Analyzer) analyzeBuiltinDictEntryGetOrInsertCall(expr *ast.CallExpr) (Type, bool) {
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "get_or_insert" || fieldExpr.Object == nil {
		return nil, false
	}
	receiverType := a.analyzeExpr(fieldExpr.Object)
	entryType, _, ok := builtinDictEntryReceiverType(receiverType)
	if !ok || entryType == nil || entryType.Dict == nil {
		return nil, false
	}
	if !a.ensureRuntimeBackedDictSupported(fieldExpr.Object.Pos(), entryType.Dict) {
		a.exprTypes[expr] = invalidType
		return invalidType, true
	}
	if !entryType.Mutable {
		a.errorf(fieldExpr.Object.Pos(), "dict entry get_or_insert requires an entry created from a mutable dict receiver")
	}
	if a.currentAllocExpr == nil {
		a.errorf(expr.Pos(), "dict entry get_or_insert requires an active in <arena>: scope")
	}
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "dict entry get_or_insert expects 1 argument, got %d", len(expr.Args))
	}
	if len(expr.Args) >= 1 {
		argType := a.analyzeValueExpr(expr.Args[0], entryType.Dict.Value)
		if !AssignableTo(entryType.Dict.Value, argType) {
			a.errorf(expr.Args[0].Pos(), "dict entry get_or_insert expects %s, got %s", entryType.Dict.Value, argType)
		}
		a.consumeAffineValueExpr(expr.Args[0], entryType.Dict.Value, "move into dict entry get_or_insert")
	}
	valueRefType := builtinDictEntryValueRefType(entryType.Dict)
	a.exprTypes[expr.Func] = &FuncType{Name: "dict.entry.get_or_insert", Params: []Type{receiverType, entryType.Dict.Value}, Return: valueRefType}
	a.exprTypes[expr] = valueRefType
	return valueRefType, true
}
