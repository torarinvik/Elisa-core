package semantic

import (
	"elisacore/src/ast"
)

// checkBufferReinterpretCast warns when an interior pointer of a sized buffer is
// cast to a reference whose pointee is *wider* than the buffer's element type
// (or to an unbounded string). Such a cast reinterprets the bytes at an
// element-bounded offset as a larger value, so reading through the result reads
// more bytes than the index guarantees in range — past the buffer into adjacent
// memory when the element is near the end. This is exactly the out-of-bounds /
// nondeterministic read class that corrupted symbol-table parsing here.
//
// It covers two shapes:
//   - `buf[i].ref[...].cast[static u8&]` / `cstr`  — an unbounded C-string scan.
//   - `buf[i].ref[u8&].cast[Struct&]` (or wider scalar/pointer) — a fixed but
//     larger reinterpretation (e.g. reading an `elf_symbol` out of a `darray[u8]`).
//
// Emitted as a warning, not a hard error: a caller that has already bounds-checked
// the index (the common loop pattern) is safe, but the cast erases that proof, so
// every site is surfaced for audit rather than rejected. Under
// EnforceUnsafePermissions, the same site also requires Unsafe.BufferReinterpret.
func (a *Analyzer) checkBufferReinterpretCast(cast *ast.CastExpr, dst Type) {
	if a == nil || cast == nil || cast.Operand == nil {
		return
	}
	bufKind, bufElem, ok := a.castOperandRootsAtBufferIndex(cast.Operand)
	if !ok {
		return
	}
	if isUnboundedStringRefType(dst) {
		a.recordUnsafeBufferReinterpret(cast)
		a.warnf(cast.Pos(), "casting an interior pointer of a sized %s to an unbounded string (%s) erases its length; a static_strlen / C-string scan can read past the %s into adjacent memory (out-of-bounds, nondeterministic). Prefer a length-bounded view (sview), or copy bounded bytes into a fresh allocation", bufKind, dst, bufKind)
		return
	}
	dstRef, ok := dst.(*RefType)
	if !ok || dstRef == nil || dstRef.Elem == nil {
		return
	}
	// Reinterpreting a byte buffer element as a wider value reads past the
	// single-element bound. (Same-width reinterpretations stay within the
	// indexed element and are not flagged.)
	if isSingleByteType(bufElem) && typeWiderThanByte(dstRef.Elem) {
		a.recordUnsafeBufferReinterpret(cast)
		a.warnf(cast.Pos(), "casting an interior pointer of a byte %s to %s reinterprets %s as a wider value; reading it accesses more bytes than the index bounds and can read past the %s (out-of-bounds, nondeterministic). Bounds-check the offset against the buffer length, or copy the bytes into a properly typed value", bufKind, dst, bufKind, bufKind)
	}
}

func (a *Analyzer) recordUnsafeBufferReinterpret(cast *ast.CastExpr) {
	if a == nil || cast == nil {
		return
	}
	if a.unsafeBufferReinterpretCasts == nil {
		a.unsafeBufferReinterpretCasts = make(map[*ast.CastExpr]bool)
	}
	a.unsafeBufferReinterpretCasts[cast] = true
}

// isUnboundedStringRefType reports whether a type is an unbounded C-string
// reference: a `static u8&` (static-storage byte reference used as a string) or
// a `cstr`/`DStr` borrowed string.
func isUnboundedStringRefType(t Type) bool {
	switch tt := t.(type) {
	case *RefType:
		if tt == nil {
			return false
		}
		b, ok := tt.Elem.(*BuiltinType)
		return ok && b != nil && b.Name == "u8" && tt.Storage == RefStorageStatic
	case *DStrType:
		return true
	}
	return false
}

// isSingleByteType reports whether a type occupies a single byte, so an index
// into a buffer of it guarantees exactly one byte in range.
func isSingleByteType(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok || b == nil {
		return false
	}
	switch b.Name {
	case "u8", "i8", "bool":
		return true
	}
	return false
}

// typeWiderThanByte reports whether reading a value of this type accesses more
// than one byte (so reinterpreting a single byte as it over-reads).
func typeWiderThanByte(t Type) bool {
	switch tt := t.(type) {
	case *BuiltinType:
		if tt == nil {
			return false
		}
		switch tt.Name {
		case "u8", "i8", "bool", "void":
			return false
		case "char":
			// char is byte-sized in this dialect; treat as single-byte.
			return false
		default:
			// u16/i16/u32/i32/f32/u64/i64/f64/usize/isize/uintptr/int, etc.
			return true
		}
	case *StructType, *ArrayType, *TupleType, *RefType, *DStrType, *DArrayType, *DArrayViewType:
		return true
	}
	return false
}

// castOperandRootsAtBufferIndex walks a cast operand through reference/cast/paren
// wrappers and reports whether it ultimately takes the address of an element of
// a sized buffer (darray / array / view). Returns the buffer kind (for the
// diagnostic) and the buffer's element type.
func (a *Analyzer) castOperandRootsAtBufferIndex(expr ast.Expr) (string, Type, bool) {
	for expr != nil {
		switch n := expr.(type) {
		case *ast.ParenExpr:
			expr = n.Inner
		case *ast.AddrOfExpr:
			expr = n.Operand
		case *ast.CastExpr:
			expr = n.Operand
		case *ast.IndexExpr:
			objType := a.exprTypes[n.Object]
			if objType == nil {
				objType = a.analyzeExpr(n.Object)
			}
			return sizedBufferKindAndElem(objType)
		default:
			return "", nil, false
		}
	}
	return "", nil, false
}

func sizedBufferKindAndElem(t Type) (string, Type, bool) {
	switch tt := t.(type) {
	case *DArrayType:
		return "darray", tt.Elem, true
	case *ArrayType:
		return "array", tt.Elem, true
	case *DArrayViewType:
		return "view", tt.Elem, true
	}
	return "", nil, false
}
