package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"strings"
)

const StaticEvalCallDepthLimit = 64

// StaticEvalExprDepthLimit bounds recursive constant-expression shapes. Deep trees
// decline to a non-constant result; iterative folds such as long binary chains remain supported.
const StaticEvalExprDepthLimit = 128
const staticEvalLoopIterationLimit = 100000

func (a *Analyzer) resolveArrayType(expr *ast.ArrayType) Type {
	arr := &ArrayType{Elem: a.resolveType(expr.Elem), ElemTypeExpr: expr.Elem, Size: a.exprSummary(expr.Size)}
	value, ok := a.evalConstExpr(expr.Size)
	if !ok || value.Kind != ConstInt {
		if ident, identOK := expr.Size.(*ast.Ident); identOK {
			if _, paramOK := a.lookupConstParam(ident.Name); paramOK {
				arr.ConstParam = ident.Name
				return arr
			}
		}
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
		a.errorf(indexExpr.Pos(), "constant index %d out of bounds for %s", value.Int, arr)
	}
}

func (a *Analyzer) checkConstantArraySliceBounds(arr *ArrayType, startExpr ast.Expr, endExpr ast.Expr) {
	if arr == nil || !arr.HasConstSize {
		return
	}
	start, startOK := a.evalConstExpr(startExpr)
	end, endOK := a.evalConstExpr(endExpr)
	if !startOK || !endOK || start.Kind != ConstInt || end.Kind != ConstInt {
		return
	}
	if start.Int < 0 || start.Int > arr.ConstSize {
		a.errorf(startExpr.Pos(), "constant slice start %d out of bounds for %s", start.Int, arr)
	}
	if end.Int < 0 || end.Int > arr.ConstSize {
		a.errorf(endExpr.Pos(), "constant slice end %d out of bounds for %s", end.Int, arr)
	}
	if start.Int > end.Int {
		a.errorf(startExpr.Pos(), "constant slice start %d is after end %d for %s", start.Int, end.Int, arr)
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

func (a *Analyzer) analyzeStaticAssert(pos lexer.Pos, cond ast.Expr, message ast.Expr) {
	a.staticContextDepth++
	defer func() { a.staticContextDepth-- }()
	condType := a.analyzeCondExpr(cond)
	if !IsBoolType(condType) && !IsInvalidType(condType) {
		a.errorf(pos, "static assert condition must be bool, got %s", condType)
		return
	}
	if message != nil {
		a.analyzeExpr(message)
	}
	if cond, ok := a.evalConstBoolExpr(cond); ok && !cond {
		if message != nil {
			if msg, msgOK := a.evalConstStringExpr(message); msgOK {
				a.errorf(pos, "static assert failed: %s", msg)
			} else {
				a.errorf(pos, "static assert failed")
			}
		} else {
			a.errorf(pos, "static assert failed")
		}
	}
}

func (a *Analyzer) analyzeStaticOnlyStmts(stmts []ast.Stmt) {
	a.staticContextDepth++
	a.constEvalScopes = append(a.constEvalScopes, map[string]ConstValue{})
	_, _, ok := a.evalStaticStmtBlock(stmts, false)
	a.constEvalScopes = a.constEvalScopes[:len(a.constEvalScopes)-1]
	a.staticContextDepth--
	if !ok {
		return
	}
}

func (a *Analyzer) evalConstExpr(expr ast.Expr) (ConstValue, bool) {
	if a.constEvalDepth >= StaticEvalExprDepthLimit {
		return ConstValue{}, false
	}
	a.constEvalDepth++
	defer func() { a.constEvalDepth-- }()
	switch n := expr.(type) {
	case *ast.IntLit:
		// constEvalExpectedType lets a declared u64/usize/uintptr const accept a suffixless literal
		// that exceeds i64 max (it stands in for the `u64` suffix). nil in non-const contexts, where
		// this reduces to ParseIntLiteral.
		value, ok := ParseIntLiteralForType(n, a.constEvalExpectedType)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: value}, true
	case *ast.FloatLit:
		value, ok := ParseFloatLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstFloat, Float: value}, true
	case *ast.BoolLit:
		return ConstValue{Kind: ConstBool, Bool: n.Value}, true
	case *ast.StringLit:
		return ConstValue{Kind: ConstString, String: n.Value}, true
	case *ast.CharLit:
		value, ok := ParseCharLiteral(n)
		if !ok {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: value}, true
	case *ast.Ident:
		// A parameter, local, loop or pattern binding shadows every const of its name, and the
		// const tables below know nothing of function scopes (see nameIsRuntimeBinding).
		if a.nameIsRuntimeBinding(n.Name) {
			return ConstValue{}, false
		}
		if value, ok := a.lookupConstEvalValue(n.Name); ok {
			return value, true
		}
		if t, ok := a.lookupConstParam(n.Name); ok {
			if valueType, ok := t.(*ConstValueType); ok && valueType != nil {
				return valueType.Value, true
			}
			return ConstValue{}, false
		}
		value, ok := a.lookupVisibleConst(n.Name)
		return value, ok
	case *ast.FieldExpr:
		if value, ok := a.evalConstAggregateFieldExpr(n); ok {
			return value, true
		}
		if name, ok := constFieldExprName(n); ok {
			// `p.limit` names the LOCAL p's field when p is a binding, not a const `p`'s.
			if a.nameIsRuntimeBinding(constFieldExprRootName(name)) {
				return ConstValue{}, false
			}
			if value, exists := a.constValues[name]; exists {
				return value, true
			}
		}
		ident, ok := n.Object.(*ast.Ident)
		if !ok {
			return ConstValue{}, false
		}
		for _, candidate := range a.visibleNameCandidates(ident.Name) {
			if value, ok := a.constValues[candidate+"."+n.Field]; ok {
				return value, true
			}
		}
		// Type aliases do not add duplicate entries to constValues. Resolve the
		// visible enum identity before reading its exact member value.
		if !a.identNameResolvesAsValue(ident.Name) {
			if base, _, ok := a.lookupVisibleType(ident.Name); ok {
				if enum, ok := base.(*ConstEnumType); ok {
					if member, ok := enum.Member(n.Field); ok {
						return ConstValue{Kind: ConstInt, Int: member.Value}, true
					}
				}
			}
		}
		return ConstValue{}, false
	case *ast.ShorthandMemberExpr:
		constEnumType, ok := a.exprType(n).(*ConstEnumType)
		if !ok || constEnumType == nil {
			return ConstValue{}, false
		}
		member, ok := constEnumType.Member(strings.Join(n.Parts, "."))
		if !ok || member == nil {
			return ConstValue{}, false
		}
		return ConstValue{Kind: ConstInt, Int: member.Value}, true
	case *ast.ParenExpr:
		return a.evalConstExpr(n.Inner)
	case *ast.CastExpr:
		operand, ok := a.evalConstExpr(n.Operand)
		if !ok {
			return ConstValue{}, false
		}
		return CastConstValue(operand, a.resolveType(n.Target))
	case *ast.MoveExpr:
		return a.evalConstExpr(n.Operand)
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
			switch operand.Kind {
			case ConstInt:
				return ConstValue{Kind: ConstInt, Int: -operand.Int}, true
			case ConstFloat:
				return ConstValue{Kind: ConstFloat, Float: -operand.Float}, true
			default:
				return ConstValue{}, false
			}
		case lexer.TOKEN_TILDE:
			if operand.Kind != ConstInt {
				return ConstValue{}, false
			}
			return ConstValue{Kind: ConstInt, Int: ^operand.Int}, true
		default:
			return ConstValue{}, false
		}
	case *ast.BinaryExpr:
		// `a + b + c + …` parses left-leaning, so a flat chain is a tree as deep as it is long;
		// recursing on Left overflowed the Go stack at a few hundred thousand terms. Walk the left
		// spine iteratively, evaluate its leaf, then fold back up — the same left-to-right order.
		var spineBuf [8]*ast.BinaryExpr
		spine := append(spineBuf[:0], n)
		leaf := n.Left
		for {
			inner, ok := leaf.(*ast.BinaryExpr)
			if !ok {
				break
			}
			spine = append(spine, inner)
			leaf = inner.Left
		}
		value, ok := a.evalConstExpr(leaf)
		if !ok {
			return ConstValue{}, false
		}
		for i := len(spine) - 1; i >= 0; i-- {
			value, ok = a.evalConstBinaryWithLeft(spine[i], value)
			if !ok {
				return ConstValue{}, false
			}
		}
		return value, true
	case *ast.TernaryExpr:
		cond, ok := a.evalConstBoolExpr(n.Cond)
		if !ok {
			return ConstValue{}, false
		}
		if cond {
			return a.evalConstExpr(n.Value)
		}
		return a.evalConstExpr(n.Alt)
	case *ast.UnwrapElseExpr:
		return a.evalConstUnwrapElseExpr(n)
	case *ast.GetExpr:
		return a.evalConstGetExpr(n)
	case *ast.TupleExpr:
		elems := make([]ConstValue, 0, len(n.Elems))
		for _, elem := range n.Elems {
			value, ok := a.evalConstExpr(elem)
			if !ok {
				return ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return ConstValue{Kind: ConstTuple, Elems: elems}, true
	case *ast.ListLitExpr:
		if n.Owner != nil {
			return ConstValue{}, false
		}
		if n.Brace && len(n.Keys) > 0 {
			entries := make([]ConstDictEntry, 0, len(n.Elems))
			for i, elem := range n.Elems {
				if i >= len(n.Keys) || n.Keys[i] == nil {
					return ConstValue{}, false
				}
				if i < len(n.Spreads) && n.Spreads[i] {
					return ConstValue{}, false
				}
				key, ok := a.evalConstExpr(n.Keys[i])
				if !ok {
					return ConstValue{}, false
				}
				value, ok := a.evalConstExpr(elem)
				if !ok {
					return ConstValue{}, false
				}
				entries = append(entries, ConstDictEntry{Key: key, Value: value})
			}
			return ConstValue{Kind: ConstDict, Dict: entries}, true
		}
		elems := make([]ConstValue, 0, len(n.Elems))
		var expectedElement Type
		if expectedArray, ok := a.constEvalExpectedType.(*ArrayType); ok && expectedArray != nil && !n.Brace {
			expectedElement = expectedArray.Elem
		}
		for i, elem := range n.Elems {
			if i < len(n.Spreads) && n.Spreads[i] {
				return ConstValue{}, false
			}
			value, ok := a.evalConstArrayElement(elem, expectedElement)
			if !ok {
				return ConstValue{}, false
			}
			elems = append(elems, value)
		}
		return ConstValue{Kind: ConstList, Elems: elems}, true
	case *ast.StructLitExpr:
		if len(n.Spreads) != 0 {
			for _, spread := range n.Spreads {
				if spread != nil {
					return ConstValue{}, false
				}
			}
		}
		args := n.Args
		if n.ResolvedArgsValid {
			args = n.ResolvedArgs
		}
		fields := map[string]ConstValue{}
		for i, arg := range args {
			if i >= len(n.ArgNames) {
				return ConstValue{}, false
			}
			fieldName := n.ArgNames[i]
			if fieldName == "" {
				return ConstValue{}, false
			}
			value, ok := a.evalConstExpr(arg)
			if !ok {
				return ConstValue{}, false
			}
			fields[fieldName] = value
		}
		return ConstValue{Kind: ConstRecord, Fields: fields}, true
	case *ast.IndexExpr:
		return a.evalConstIndexExpr(n)
	case *ast.QueryExpr:
		return a.evalConstQueryExpr(n)
	case *ast.CallExpr:
		if value, ok := a.evalConstReflectionCall(n); ok {
			return value, true
		}
		return a.evalStaticFunctionCall(n)
	default:
		return ConstValue{}, false
	}
}

// evalConstArrayElement applies an array's element type only to a literal
// integer or a nested array literal. Other expressions keep their existing
// context, so the target type cannot leak into a condition or call operand.
func (a *Analyzer) evalConstArrayElement(expr ast.Expr, expected Type) (ConstValue, bool) {
	inner := expr
	for {
		paren, ok := inner.(*ast.ParenExpr)
		if !ok || paren == nil {
			break
		}
		inner = paren.Inner
	}
	useExpected := false
	switch n := inner.(type) {
	case *ast.IntLit:
		// This array compatibility path is limited to u64. The width of usize
		// and uintptr depends on the target and needs separate range handling.
		builtin, ok := expected.(*BuiltinType)
		useExpected = n != nil && n.Suffix == "" && ok && builtin != nil && builtin.Name == "u64"
	case *ast.ListLitExpr:
		_, useExpected = expected.(*ArrayType)
	}
	if !useExpected {
		return a.evalConstExpr(expr)
	}
	previous := a.constEvalExpectedType
	a.constEvalExpectedType = expected
	defer func() { a.constEvalExpectedType = previous }()
	return a.evalConstExpr(expr)
}

// constFieldExprRootName is the leading identifier of a dotted name built by constFieldExprName.
func constFieldExprRootName(name string) string {
	if idx := strings.Index(name, "."); idx > 0 {
		return name[:idx]
	}
	return name
}

func constFieldExprName(expr ast.Expr) (string, bool) {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name, true
	case *ast.FieldExpr:
		base, ok := constFieldExprName(n.Object)
		if !ok {
			return "", false
		}
		return base + "." + n.Field, true
	default:
		return "", false
	}
}

func (a *Analyzer) evalConstIndexExpr(expr *ast.IndexExpr) (ConstValue, bool) {
	if expr == nil {
		return ConstValue{}, false
	}
	object, ok := a.evalConstExpr(expr.Object)
	if !ok {
		return ConstValue{}, false
	}
	index, ok := a.evalConstExpr(expr.Index)
	if !ok {
		return ConstValue{}, false
	}
	if object.Kind == ConstDict {
		objType := a.exprType(expr.Object)
		dictType, _, ok := builtinDictReceiverType(objType)
		if !ok || dictType == nil {
			return ConstValue{}, false
		}
		key, ok := constDictKeyFingerprint(dictType.Key, index)
		if !ok {
			return ConstValue{}, false
		}
		for _, entry := range object.Dict {
			entryKey, ok := constDictKeyFingerprint(dictType.Key, entry.Key)
			if ok && entryKey == key {
				return entry.Value, true
			}
		}
		return ConstValue{}, false
	}
	if index.Kind != ConstInt {
		return ConstValue{}, false
	}
	if index.Int >= 0 {
		slot := int(index.Int)
		if slot < len(object.Elems) && (object.Kind == ConstTuple || object.Kind == ConstList) {
			return object.Elems[slot], true
		}
	}
	if expr.Fallback != nil {
		return a.evalConstExpr(expr.Fallback)
	}
	return ConstValue{}, false
}
