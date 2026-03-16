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
	ID     int
	Label  string
	Origin string
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
	SurfaceName  string
}

type DArrayType struct {
	Elem        Type
	Shape       Shape
	SurfaceName string
}

type DArrayViewType struct {
	Elem        Type
	Begin       string
	End         string
	SurfaceName string
}

type DListType struct {
	Elem  Type
	Shape Shape
}

type DListViewType struct {
	Elem Type
}

type DStrType struct {
	Shape       Shape
	SurfaceName string
}

type SViewType struct {
	Begin string
	End   string
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
	Builtin    bool
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
	Name                   string
	TypeParams             []string
	ShapeParams            []string
	FreshReturnShapeParams []string
	Params                 []Type
	Return                 Type
	Variadic               bool
}

func (*InvalidType) isType()         {}
func (*NullType) isType()            {}
func (*BuiltinType) isType()         {}
func (*TypeParamType) isType()       {}
func (*RefType) isType()             {}
func (*ArrayType) isType()           {}
func (*DArrayType) isType()          {}
func (*DArrayViewType) isType()      {}
func (*DListType) isType()           {}
func (*DListViewType) isType()       {}
func (*DStrType) isType()            {}
func (*SViewType) isType()           {}
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
	if t.SurfaceName == "str" || t.SurfaceName == "string" {
		return fmt.Sprintf("str[%s]", t.Size)
	}
	if t.SurfaceName == "array" {
		return fmt.Sprintf("array[%s, %s]", t.Elem.String(), t.Size)
	}
	return fmt.Sprintf("%s[%s]", t.Elem.String(), t.Size)
}
func (t *DArrayType) String() string {
	if t.SurfaceName == "darray" {
		return fmt.Sprintf("darray[%s, %s]", t.Elem.String(), t.Shape.String())
	}
	return fmt.Sprintf("DArray[%s, %s]", t.Elem.String(), t.Shape.String())
}
func (t *DArrayViewType) String() string {
	if t.SurfaceName == "view" || t.Begin != "" || t.End != "" {
		if t.Begin != "" || t.End != "" {
			return fmt.Sprintf("view[%s, %s, %s]", t.Elem.String(), t.Begin, t.End)
		}
		return fmt.Sprintf("view[%s]", t.Elem.String())
	}
	return fmt.Sprintf("DArrayView[%s]", t.Elem.String())
}
func (t *DListType) String() string {
	return fmt.Sprintf("DList[%s, %s]", t.Elem.String(), t.Shape.String())
}
func (t *DListViewType) String() string {
	return fmt.Sprintf("DListView[%s]", t.Elem.String())
}
func (t *DStrType) String() string {
	if t.SurfaceName == "dstr" || t.SurfaceName == "dstring" {
		return fmt.Sprintf("dstr[%s]", t.Shape.String())
	}
	return fmt.Sprintf("DStr[%s]", t.Shape.String())
}
func (t *SViewType) String() string {
	return fmt.Sprintf("sview[%s, %s]", t.Begin, t.End)
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
	case "char", "int", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "uintptr":
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

func CommonNumericType(a, b Type) Type {
	if !IsNumericType(a) || !IsNumericType(b) {
		return invalidType
	}
	if SameType(a, b) {
		return a
	}
	if ta, ok := a.(*BuiltinType); ok && ta.Name == "char" {
		return b
	}
	if tb, ok := b.(*BuiltinType); ok && tb.Name == "char" {
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
