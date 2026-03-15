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

type Shape interface {
	String() string
	isShape()
}

type InvalidType struct{}

type NullType struct{}

type BuiltinType struct {
	Name string
}

type TypeParamType struct {
	Name string
}

type ShapeParam struct {
	Name string
}

type NamedShape struct {
	Name string
}

type FreshShape struct {
	ID    int
	Label string
}

type RefState int

const (
	RefStateNonNull RefState = iota
	RefStateNullable
	RefStateNull
)

type RefType struct {
	Elem  Type
	State RefState
}

type ArrayType struct {
	Elem         Type
	Size         string
	HasConstSize bool
	ConstSize    int64
}

type DArrayType struct {
	Elem  Type
	Shape Shape
}

type DStrType struct {
	Shape Shape
}

type Field struct {
	Name    string
	Type    Type
	Mutable bool
	IsTail  bool
}

type StructType struct {
	Name       string
	TypeParams []string
	Fields     map[string]Field
	ReprC      bool
	Decl       *ast.StructDecl
}

type OpaqueType struct {
	Name string
}

type GenericInstanceType struct {
	Name string
	Base Type
	Args []Type
}

type FuncType struct {
	Name        string
	TypeParams  []string
	ShapeParams []string
	Params      []Type
	Return      Type
	Variadic    bool
}

func (*InvalidType) isType()         {}
func (*NullType) isType()            {}
func (*BuiltinType) isType()         {}
func (*TypeParamType) isType()       {}
func (*RefType) isType()             {}
func (*ArrayType) isType()           {}
func (*DArrayType) isType()          {}
func (*DStrType) isType()            {}
func (*StructType) isType()          {}
func (*OpaqueType) isType()          {}
func (*GenericInstanceType) isType() {}
func (*FuncType) isType()            {}

func (*ShapeParam) isShape() {}
func (*NamedShape) isShape() {}
func (*FreshShape) isShape() {}

func (*InvalidType) String() string { return "<invalid>" }
func (*NullType) String() string    { return "null" }
func (t *BuiltinType) String() string {
	return t.Name
}
func (t *TypeParamType) String() string { return t.Name }
func (s *ShapeParam) String() string    { return s.Name }
func (s *NamedShape) String() string    { return s.Name }
func (s *FreshShape) String() string {
	if s.Label != "" {
		return fmt.Sprintf("%s#%d", s.Label, s.ID)
	}
	return fmt.Sprintf("shape#%d", s.ID)
}
func (t *RefType) String() string {
	s := t.Elem.String()
	switch t.State {
	case RefStateNullable:
		s += "&?"
	case RefStateNull:
		s += "!"
	default:
		s += "&"
	}
	return s
}
func (t *ArrayType) String() string {
	return fmt.Sprintf("%s[%s]", t.Elem.String(), t.Size)
}
func (t *DArrayType) String() string {
	return fmt.Sprintf("DArray[%s, %s]", t.Elem.String(), t.Shape.String())
}
func (t *DStrType) String() string {
	return fmt.Sprintf("DStr[%s]", t.Shape.String())
}
func (t *StructType) String() string { return t.Name }
func (t *OpaqueType) String() string { return t.Name }
func (t *GenericInstanceType) String() string {
	parts := make([]string, 0, len(t.Args))
	for _, arg := range t.Args {
		parts = append(parts, arg.String())
	}
	return fmt.Sprintf("%s[%s]", t.Name, strings.Join(parts, ", "))
}
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

func IsTypeParamType(t Type) (*TypeParamType, bool) {
	tp, ok := t.(*TypeParamType)
	return tp, ok
}

func IsRefType(t Type) (*RefType, bool) {
	r, ok := t.(*RefType)
	return r, ok
}

func refStateAssignable(dst, src RefState) bool {
	switch dst {
	case RefStateNullable:
		return true
	case RefStateNonNull:
		return src == RefStateNonNull
	case RefStateNull:
		return src == RefStateNull
	default:
		return false
	}
}

func mergeRefStates(a, b RefState) (RefState, bool) {
	if a == b {
		return a, true
	}
	if a == RefStateNullable || b == RefStateNullable {
		return RefStateNullable, true
	}
	if (a == RefStateNonNull && b == RefStateNull) || (a == RefStateNull && b == RefStateNonNull) {
		return RefStateNullable, true
	}
	return RefStateNullable, false
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
	case *TypeParamType:
		tb, ok := b.(*TypeParamType)
		return ok && ta.Name == tb.Name
	case *RefType:
		tb, ok := b.(*RefType)
		return ok && ta.State == tb.State && SameType(ta.Elem, tb.Elem)
	case *ArrayType:
		tb, ok := b.(*ArrayType)
		return ok && arraySizesEqual(ta, tb) && SameType(ta.Elem, tb.Elem)
	case *DArrayType:
		tb, ok := b.(*DArrayType)
		return ok && SameType(ta.Elem, tb.Elem) && SameShape(ta.Shape, tb.Shape)
	case *DStrType:
		tb, ok := b.(*DStrType)
		return ok && SameShape(ta.Shape, tb.Shape)
	case *StructType:
		tb, ok := b.(*StructType)
		return ok && ta.Name == tb.Name
	case *OpaqueType:
		tb, ok := b.(*OpaqueType)
		return ok && ta.Name == tb.Name
	case *GenericInstanceType:
		tb, ok := b.(*GenericInstanceType)
		if !ok || ta.Name != tb.Name || len(ta.Args) != len(tb.Args) {
			return false
		}
		for i := range ta.Args {
			if !SameType(ta.Args[i], tb.Args[i]) {
				return false
			}
		}
		return SameType(ta.Base, tb.Base)
	case *FuncType:
		tb, ok := b.(*FuncType)
		if !ok || ta.Variadic != tb.Variadic || len(ta.TypeParams) != len(tb.TypeParams) || len(ta.ShapeParams) != len(tb.ShapeParams) || len(ta.Params) != len(tb.Params) || !SameType(ta.Return, tb.Return) {
			return false
		}
		for i := range ta.TypeParams {
			if ta.TypeParams[i] != tb.TypeParams[i] {
				return false
			}
		}
		for i := range ta.ShapeParams {
			if ta.ShapeParams[i] != tb.ShapeParams[i] {
				return false
			}
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
	if matchTypePattern(dst, src) {
		return true
	}
	if _, ok := dst.(*TypeParamType); ok {
		return true
	}
	if _, ok := src.(*TypeParamType); ok {
		return true
	}
	if SameType(dst, src) {
		return true
	}
	if IsNumericType(dst) && IsNumericType(src) {
		return true
	}
	if IsNullType(src) {
		if r, ok := dst.(*RefType); ok {
			return r.State != RefStateNonNull
		}
		return false
	}
	if dr, ok := dst.(*RefType); ok {
		if sr, ok := src.(*RefType); ok {
			if !SameType(dr.Elem, sr.Elem) {
				return false
			}
			return refStateAssignable(dr.State, sr.State)
		}
	}
	return false
}

func matchTypePattern(pattern, actual Type) bool {
	if pattern == nil || actual == nil {
		return pattern == actual
	}
	if IsInvalidType(pattern) || IsInvalidType(actual) {
		return true
	}
	if _, ok := pattern.(*TypeParamType); ok {
		return true
	}
	switch p := pattern.(type) {
	case *BuiltinType:
		a, ok := actual.(*BuiltinType)
		return ok && p.Name == a.Name
	case *NullType:
		_, ok := actual.(*NullType)
		return ok
	case *RefType:
		a, ok := actual.(*RefType)
		if !ok {
			return false
		}
		if !refStateAssignable(p.State, a.State) {
			return false
		}
		return matchTypePattern(p.Elem, a.Elem)
	case *ArrayType:
		a, ok := actual.(*ArrayType)
		return ok && arraySizesEqual(p, a) && matchTypePattern(p.Elem, a.Elem)
	case *DArrayType:
		a, ok := actual.(*DArrayType)
		return ok && matchTypePattern(p.Elem, a.Elem) && shapeMatchesPattern(p.Shape, a.Shape)
	case *DStrType:
		a, ok := actual.(*DStrType)
		return ok && shapeMatchesPattern(p.Shape, a.Shape)
	case *StructType:
		a, ok := actual.(*StructType)
		return ok && p.Name == a.Name
	case *OpaqueType:
		a, ok := actual.(*OpaqueType)
		return ok && p.Name == a.Name
	case *GenericInstanceType:
		a, ok := actual.(*GenericInstanceType)
		if !ok || p.Name != a.Name || len(p.Args) != len(a.Args) {
			return false
		}
		for i := range p.Args {
			if !matchTypePattern(p.Args[i], a.Args[i]) {
				return false
			}
		}
		return matchTypePattern(p.Base, a.Base)
	case *FuncType:
		a, ok := actual.(*FuncType)
		if !ok || p.Variadic != a.Variadic || len(p.ShapeParams) != len(a.ShapeParams) || len(p.Params) != len(a.Params) {
			return false
		}
		for i := range p.Params {
			if !matchTypePattern(p.Params[i], a.Params[i]) {
				return false
			}
		}
		return matchTypePattern(p.Return, a.Return)
	default:
		return SameType(pattern, actual)
	}
}

func MergeTypes(a, b Type) Type {
	if SameType(a, b) {
		return a
	}
	if IsNumericType(a) && IsNumericType(b) {
		return CommonNumericType(a, b)
	}
	if ar, ok := a.(*RefType); ok {
		if br, ok := b.(*RefType); ok && SameType(ar.Elem, br.Elem) {
			if state, ok := mergeRefStates(ar.State, br.State); ok {
				return &RefType{Elem: ar.Elem, State: state}
			}
		}
	}
	if IsNullType(a) {
		if r, ok := b.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return b
			case RefStateNonNull:
				return &RefType{Elem: r.Elem, State: RefStateNullable}
			}
		}
	}
	if IsNullType(b) {
		if r, ok := a.(*RefType); ok {
			switch r.State {
			case RefStateNull, RefStateNullable:
				return a
			case RefStateNonNull:
				return &RefType{Elem: r.Elem, State: RefStateNullable}
			}
		}
	}
	return invalidType
}

func CommonNumericType(a, b Type) Type {
	if !IsNumericType(a) || !IsNumericType(b) {
		return invalidType
	}
	if SameType(a, b) {
		return a
	}
	if ta, ok := a.(*BuiltinType); ok && ta.Name == "int" {
		return b
	}
	if tb, ok := b.(*BuiltinType); ok && tb.Name == "int" {
		return a
	}
	return a
}

func arraySizesEqual(a, b *ArrayType) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.HasConstSize && b.HasConstSize {
		return a.ConstSize == b.ConstSize
	}
	return a.Size == b.Size
}

func SameShape(a, b Shape) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch sa := a.(type) {
	case *ShapeParam:
		sb, ok := b.(*ShapeParam)
		return ok && sa.Name == sb.Name
	case *NamedShape:
		sb, ok := b.(*NamedShape)
		return ok && sa.Name == sb.Name
	case *FreshShape:
		sb, ok := b.(*FreshShape)
		return ok && sa.ID == sb.ID
	default:
		return false
	}
}

func shapeMatchesPattern(pattern, actual Shape) bool {
	if pattern == nil || actual == nil {
		return pattern == actual
	}
	return SameShape(pattern, actual)
}
