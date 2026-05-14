package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
)

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
	case *ast.FloatLit:
		if n.Suffix != "" {
			if t, ok := a.namedTypes[n.Suffix]; ok {
				return t
			}
		}
		return a.namedTypes["f64"]
	case *ast.StringLit:
		return &RefType{Elem: a.namedTypes["u8"], State: RefStateNonNull, Storage: RefStorageStatic, ExplicitStorage: true}
	case *ast.CharLit:
		return a.namedTypes["char"]
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
	if AssignableTo(dst, src) {
		return true
	}
	if _, ok := src.(*ConstEnumType); ok {
		return SameType(src, dst) || IsNumericType(dst)
	}
	if _, ok := dst.(*ConstEnumType); ok {
		return IsNumericType(src)
	}
	if _, ok := src.(*TypeParamType); ok {
		return true
	}
	if _, ok := dst.(*TypeParamType); ok {
		return true
	}
	if srcID, ok := src.(*IDType); ok {
		if SameType(src, dst) {
			return true
		}
		return SameType(srcID.Storage, dst)
	}
	if dstID, ok := dst.(*IDType); ok {
		return SameType(dstID.Storage, src)
	}
	if IsNumericType(src) && IsNumericType(dst) {
		return true
	}
	if IsNullType(src) {
		if ref, ok := dst.(*RefType); ok {
			return ref.State != RefStateNonNull
		}
		if _, ok := dst.(*OptionalType); ok {
			return true
		}
	}
	if srcRef, ok := src.(*RefType); ok {
		if dstRef, ok := dst.(*RefType); ok {
			return refStateAssignable(dstRef.State, srcRef.State) && refRegionAssignable(dstRef.Region, srcRef.Region)
		}
	}
	if isPointerLikeCastType(src) && isPointerLikeCastType(dst) {
		return true
	}
	if isPointerLikeCastType(src) && IsNumericType(dst) {
		return true
	}
	if IsNumericType(src) && isPointerLikeCastType(dst) {
		return true
	}
	return false
}

func isPointerLikeCastType(t Type) bool {
	switch t.(type) {
	case *RefType, *DStrType, *FuncType:
		return true
	default:
		return false
	}
}

func unsafePointerCastRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "PointerCast"}}
}

func unsafePointerArithmeticRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "PointerArithmetic"}}
}

func unsafeRawExternRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "RawExtern"}}
}

func unsafeMutableGlobalRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "MutableGlobal"}}
}

func unsafeUncheckedIndexRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "UncheckedIndex"}}
}

func unsafeThreadShareRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "ThreadShare"}}
}

func binaryExprRequiresUnsafePointerArithmetic(op lexer.TokenKind, left, right Type) bool {
	if op != lexer.TOKEN_PLUS && op != lexer.TOKEN_MINUS {
		return false
	}
	if _, ok := left.(*RefType); ok && IsIntegralStorageType(right) {
		return true
	}
	if op == lexer.TOKEN_PLUS {
		if _, ok := right.(*RefType); ok && IsIntegralStorageType(left) {
			return true
		}
	}
	return false
}

func indexExprRequiresUnsafeUncheckedIndex(obj Type) bool {
	if IsInvalidType(obj) {
		return false
	}
	ref, ok := obj.(*RefType)
	if !ok || ref == nil {
		return false
	}
	return IsNumericType(ref.Elem) || IsBoolType(ref.Elem)
}

func castRequiresUnsafePointerCast(src, dst Type) bool {
	if IsInvalidType(src) || IsInvalidType(dst) || SameType(src, dst) || AssignableTo(dst, src) {
		return false
	}
	if _, ok := src.(*TypeParamType); ok {
		return false
	}
	if _, ok := dst.(*TypeParamType); ok {
		return false
	}
	if IsNullType(src) {
		return false
	}
	if isPointerLikeCastType(src) && isPointerLikeCastType(dst) {
		return true
	}
	if isPointerLikeCastType(src) && IsNumericType(dst) {
		return true
	}
	if IsNumericType(src) && isPointerLikeCastType(dst) {
		return true
	}
	return false
}

func (a *Analyzer) regionQualifierDefined(name string) bool {
	if name == "" {
		return false
	}
	if a.currentScope != nil {
		if sym, ok := a.currentScope.Lookup(name); ok {
			if sym.Kind == SymbolRegion {
				return true
			}
			if (sym.Kind == SymbolLocal || sym.Kind == SymbolParam) && IsArenaValueOrRefType(sym.Type) {
				return true
			}
		}
	}
	return a.lookupRegionParam(name)
}

func (a *Analyzer) exprSummary(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.IntLit:
		return n.Value
	case *ast.Ident:
		return n.Name
	case *ast.FieldExpr:
		object := a.exprSummary(n.Object)
		if object == "?" {
			return "?"
		}
		return fmt.Sprintf("%s.%s", object, n.Field)
	case *ast.IndexExpr:
		object := a.exprSummary(n.Object)
		index := a.exprSummary(n.Index)
		if object == "?" || index == "?" {
			return "?"
		}
		if n.Fallback == nil {
			return fmt.Sprintf("%s[%s]", object, index)
		}
		fallback := a.exprSummary(n.Fallback)
		if fallback == "?" {
			return "?"
		}
		return fmt.Sprintf("%s[%s] else %s", object, index, fallback)
	case *ast.BinaryExpr:
		return fmt.Sprintf("%s%s%s", a.exprSummary(n.Left), lexer.TokenName(n.Op), a.exprSummary(n.Right))
	case *ast.ParenExpr:
		return a.exprSummary(n.Inner)
	case *ast.CastExpr:
		return a.exprSummary(n.Operand)
	case *ast.MoveExpr:
		return a.exprSummary(n.Operand)
	case *ast.CanExpr:
		return a.exprSummary(n.Expr)
	default:
		return "?"
	}
}
