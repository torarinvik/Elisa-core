package semantic

import (
	"fmt"
	"strings"

	"llcontext/src/ast"
)

type Type interface {
	String() string
	isType()
}

type InvalidType struct{}

type NullType struct{}

type BuiltinType struct {
	Name string
}

type RefType struct {
	Elem     Type
	Nullable bool
}

type ArrayType struct {
	Elem Type
	Size string
}

type Field struct {
	Name    string
	Type    Type
	Mutable bool
	IsTail  bool
}

type StructType struct {
	Name   string
	Fields map[string]Field
	ReprC  bool
	Decl   *ast.StructDecl
}

type OpaqueType struct {
	Name string
}

type FuncType struct {
	Name     string
	Params   []Type
	Return   Type
	Variadic bool
}

func (*InvalidType) isType() {}
func (*NullType) isType()    {}
func (*BuiltinType) isType() {}
func (*RefType) isType()     {}
func (*ArrayType) isType()   {}
func (*StructType) isType()  {}
func (*OpaqueType) isType()  {}
func (*FuncType) isType()    {}

func (*InvalidType) String() string { return "<invalid>" }
func (*NullType) String() string    { return "null" }
func (t *BuiltinType) String() string {
	return t.Name
}
func (t *RefType) String() string {
	s := t.Elem.String() + "&"
	if t.Nullable {
		s += "?"
	}
	return s
}
func (t *ArrayType) String() string {
	return fmt.Sprintf("%s[%s]", t.Elem.String(), t.Size)
}
func (t *StructType) String() string { return t.Name }
func (t *OpaqueType) String() string { return t.Name }
func (t *FuncType) String() string {
	parts := make([]string, 0, len(t.Params))
	for _, p := range t.Params {
		parts = append(parts, p.String())
	}
	if t.Variadic {
		parts = append(parts, "...")
	}
	if t.Return == nil {
		return fmt.Sprintf("func(%s)", strings.Join(parts, ", "))
	}
	return fmt.Sprintf("func(%s) -> %s", strings.Join(parts, ", "), t.Return.String())
}

var (
	invalidType = &InvalidType{}
	nullType    = &NullType{}
)

func IsInvalidType(t Type) bool {
	_, ok := t.(*InvalidType)
	return ok
}

func IsNullType(t Type) bool {
	_, ok := t.(*NullType)
	return ok
}

func IsBoolType(t Type) bool {
	b, ok := t.(*BuiltinType)
	return ok && b.Name == "bool"
}

func IsNumericType(t Type) bool {
	b, ok := t.(*BuiltinType)
	if !ok {
		return false
	}
	switch b.Name {
	case "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
		return true
	default:
		return false
	}
}

func IsRefType(t Type) (*RefType, bool) {
	r, ok := t.(*RefType)
	return r, ok
}

func SameType(a, b Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if IsInvalidType(a) || IsInvalidType(b) {
		return true
	}
	switch ta := a.(type) {
	case *NullType:
		_, ok := b.(*NullType)
		return ok
	case *BuiltinType:
		tb, ok := b.(*BuiltinType)
		return ok && ta.Name == tb.Name
	case *RefType:
		tb, ok := b.(*RefType)
		return ok && ta.Nullable == tb.Nullable && SameType(ta.Elem, tb.Elem)
	case *ArrayType:
		tb, ok := b.(*ArrayType)
		return ok && ta.Size == tb.Size && SameType(ta.Elem, tb.Elem)
	case *StructType:
		tb, ok := b.(*StructType)
		return ok && ta.Name == tb.Name
	case *OpaqueType:
		tb, ok := b.(*OpaqueType)
		return ok && ta.Name == tb.Name
	case *FuncType:
		tb, ok := b.(*FuncType)
		if !ok || ta.Variadic != tb.Variadic || len(ta.Params) != len(tb.Params) || !SameType(ta.Return, tb.Return) {
			return false
		}
		for i := range ta.Params {
			if !SameType(ta.Params[i], tb.Params[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func AssignableTo(dst, src Type) bool {
	if dst == nil || src == nil {
		return false
	}
	if IsInvalidType(dst) || IsInvalidType(src) {
		return true
	}
	if SameType(dst, src) {
		return true
	}
	if IsNullType(src) {
		if r, ok := dst.(*RefType); ok {
			return r.Nullable
		}
		return false
	}
	if dr, ok := dst.(*RefType); ok {
		if sr, ok := src.(*RefType); ok {
			if !SameType(dr.Elem, sr.Elem) {
				return false
			}
			if dr.Nullable {
				return true
			}
			return !sr.Nullable
		}
	}
	return false
}

func MergeTypes(a, b Type) Type {
	if SameType(a, b) {
		return a
	}
	if IsNullType(a) {
		if r, ok := b.(*RefType); ok && r.Nullable {
			return b
		}
	}
	if IsNullType(b) {
		if r, ok := a.(*RefType); ok && r.Nullable {
			return a
		}
	}
	return invalidType
}
