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
	// A plain (non-packed) enum value may be postfix-cast to an unsigned integer type to obtain its
	// sequential tag ordinal. Packed enums are excluded: their runtime tag is NOT the sequential
	// ordinal (it is embedded in a bitfield word). The cast is a value extraction, not a reinterpret.
	if src, ok := src.(*EnumType); ok && src != nil && !src.Packed {
		return isUnsignedIntegerType(dst)
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
	if srcAddr, ok := src.(*AddressSpaceType); ok {
		if SameType(src, dst) {
			return true
		}
		return SameType(srcAddr.Storage, dst) || isPointerLikeCastType(dst)
	}
	if dstAddr, ok := dst.(*AddressSpaceType); ok {
		return SameType(dstAddr.Storage, src)
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

// isValueConversion reports whether a cast changes a value's numeric representation or domain — a
// conversion that is the CONSTRUCTOR's job (`T(x)` / `x.T()`), not a bit reinterpret. `.cast[T]` is
// the canonical reinterpret/bitcast and rejects these; the value forms accept them.
//
// It is deliberately width-agnostic: any plain-numeric -> different-plain-numeric counts (even a
// same-width sign reinterpret like usize -> i64, which `i64(x)` performs losslessly), as does
// const-enum <-> numeric. Pointer/ref reinterprets are NOT caught — a pointer/ref is not a numeric
// type, so `ptr.cast[i64]` / `int.cast[T&]` stay valid reinterprets.
func (a *Analyzer) isValueConversion(src, dst Type) bool {
	if SameType(src, dst) || IsInvalidType(src) || IsInvalidType(dst) {
		return false
	}
	// IDType / AddressSpaceType are tagged storage: a cast to/from them keeps the bits unchanged and
	// only adds/removes a phantom tag, so it is a reinterpret (`.cast`), not a value conversion —
	// even though IsNumericType reports them numeric via their storage.
	if isStorageTagType(src) || isStorageTagType(dst) {
		return false
	}
	if _, ok := src.(*ConstEnumType); ok {
		return IsNumericType(dst)
	}
	if _, ok := dst.(*ConstEnumType); ok {
		return IsNumericType(src)
	}
	// Plain enum → unsigned integer: a value extraction (tag ordinal), not a bit reinterpret.
	if src, ok := src.(*EnumType); ok && src != nil && !src.Packed {
		return isUnsignedIntegerType(dst)
	}
	return IsNumericType(src) && IsNumericType(dst)
}

func isStorageTagType(t Type) bool {
	switch t.(type) {
	case *IDType, *AddressSpaceType:
		return true
	default:
		return false
	}
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

func unsafeGuestHostPointerCastRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "GuestHostPointerCast"}}
}

func unsafePointerArithmeticRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "PointerArithmetic"}}
}

func unsafeIndirectCallRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "IndirectCall"}}
}

func unsafeLeakRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "Leak"}}
}

// isIndirectCallTarget reports whether a CallExpr.Func is the synthetic cast
// produced by the postfix `value.call_as[func(...)->T](args)` form, i.e. a raw
// function-pointer value being invoked through its asserted signature. Such a
// call is the operation gated by Unsafe.IndirectCall.
func isIndirectCallTarget(fn ast.Expr) bool {
	cast, ok := fn.(*ast.CastExpr)
	return ok && cast != nil && cast.Origin == ast.CastExprOriginIndirectCall
}

func unsafeRawExternRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "RawExtern"}}
}

func unsafeMutableGlobalRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "MutableGlobal"}}
}

func globalReadRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Global", Member: "Read"}}
}

func globalWriteRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Global", Member: "Write"}}
}

func unsafeUncheckedIndexRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "UncheckedIndex"}}
}

func unsafeThreadShareRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "ThreadShare"}}
}

func unsafeStaleRefRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "StaleRef"}}
}

func unsafeAliasRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "Alias"}}
}

func unsafeBufferReinterpretRefs(pos lexer.Pos) []ast.PermissionRef {
	return []ast.PermissionRef{{Position: pos, Name: "Unsafe", Member: "BufferReinterpret"}}
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
	if srcAddr, ok := src.(*AddressSpaceType); ok && srcAddr.Space == "guest" && isPointerLikeCastType(dst) {
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

func (a *Analyzer) castRequiresUnsafeGuestHostPointerCast(cast *ast.CastExpr, dst Type) bool {
	if a == nil || cast == nil {
		return false
	}
	if IsInvalidType(dst) || !isPointerLikeCastType(dst) {
		return false
	}
	// Producer/origin enforcement: converting ANY guest address carrier
	// (GuestVAddr[T]) into a host pointer requires Unsafe.GuestHostPointerCast,
	// even outside @boundary_pointer_args functions. This gates the *origin*
	// sites where guest addresses are produced (TLS/GOT resolution that returns
	// a GuestVAddr), not only HLE parameters, so a produced guest address cannot
	// be silently dereferenced as a host pointer.
	if src := a.exprTypes[cast.Operand]; src != nil {
		if addr, ok := src.(*AddressSpaceType); ok && addr.Space == "guest" {
			return true
		}
	}
	// Consumer-boundary enforcement: a cast of an @boundary_pointer_args param.
	if a.currentFuncDecl == nil || a.currentFuncType == nil || len(a.currentFuncType.BoundaryPointerParamIndices) == 0 {
		return false
	}
	operand := cast.Operand
	for {
		if paren, ok := operand.(*ast.ParenExpr); ok {
			operand = paren.Inner
			continue
		}
		break
	}
	ident, ok := operand.(*ast.Ident)
	if !ok || ident == nil || ident.Name == "" {
		return false
	}
	paramIndex, ok := a.functionAnnotationParamIndex(a.currentFuncDecl, ident.Name)
	if !ok {
		return false
	}
	return intSliceContains(a.currentFuncType.BoundaryPointerParamIndices, paramIndex)
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
