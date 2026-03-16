package semantic

import (
	"fmt"
	"sort"
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

type NeverType struct{}

type NullType struct{}

type BuiltinType struct {
	Name string
}

type TypeParamType struct {
	Name string
}

type ErrorSetType struct {
	Name string
	Tags []string
}

type ErrorUnionType struct {
	Value  Type
	Errors *ErrorSetType
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

type RefStorage int

const (
	RefStorageAny RefStorage = iota
	RefStorageHeap
	RefStorageStack
	RefStorageStatic
)

type RefType struct {
	Elem            Type
	State           RefState
	Storage         RefStorage
	ExplicitStorage bool
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
func (*NeverType) isType()           {}
func (*NullType) isType()            {}
func (*BuiltinType) isType()         {}
func (*TypeParamType) isType()       {}
func (*ErrorSetType) isType()        {}
func (*ErrorUnionType) isType()      {}
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
func (*NeverType) String() string   { return "<never>" }
func (*NullType) String() string    { return "null" }
func (t *BuiltinType) String() string {
	return t.Name
}
func (t *TypeParamType) String() string { return t.Name }
func (t *ErrorSetType) String() string  { return t.Name }
func (t *ErrorUnionType) String() string {
	if t == nil || t.Value == nil || t.Errors == nil {
		return "<invalid-error-union>"
	}
	return fmt.Sprintf("%s | %s", t.Value.String(), t.Errors.String())
}

func QualifyErrorTag(setName string, tagName string) string {
	if setName == "" {
		return tagName
	}
	if tagName == "" {
		return setName
	}
	return setName + "." + tagName
}

func SplitErrorTagName(name string) (string, string) {
	parts := strings.SplitN(name, ".", 2)
	if len(parts) != 2 {
		return "", name
	}
	return parts[0], parts[1]
}

func ErrorTagShortName(name string) string {
	_, tag := SplitErrorTagName(name)
	return tag
}

func ErrorTagDiagnosticName(tag string) string {
	if tag == "" {
		return "<invalid-error-tag>"
	}
	return tag
}

func ErrorSetDiagnosticName(errSet *ErrorSetType) string {
	if errSet == nil {
		return "<invalid-error-set>"
	}
	return errSet.String()
}

func ErrorTypeDiagnosticName(t Type) string {
	if t == nil {
		return "<invalid>"
	}
	switch tt := t.(type) {
	case *ErrorSetType:
		return ErrorSetDiagnosticName(tt)
	default:
		return t.String()
	}
}

func ErrorSetTagsEqual(a *ErrorSetType, b *ErrorSetType) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Tags) != len(b.Tags) {
		return false
	}
	for i := range a.Tags {
		if a.Tags[i] != b.Tags[i] {
			return false
		}
	}
	return true
}

func CanonicalizeErrorSetSelections(familySets map[string]*ErrorSetType, fullFamilies map[string]bool, selectedTags map[string]map[string]bool) *ErrorSetType {
	if len(familySets) == 0 {
		return &ErrorSetType{Name: "error[]"}
	}
	familyNames := make([]string, 0, len(familySets))
	for familyName := range familySets {
		familyNames = append(familyNames, familyName)
	}
	sort.Strings(familyNames)

	if len(familyNames) == 1 {
		familyName := familyNames[0]
		errSet := familySets[familyName]
		if fullFamilies[familyName] || errorSetSelectionIsFull(errSet, selectedTags[familyName]) {
			return errSet
		}
	}

	nameParts := make([]string, 0)
	canonicalTags := make([]string, 0)
	for _, familyName := range familyNames {
		errSet := familySets[familyName]
		if errSet == nil {
			continue
		}
		selected := selectedTags[familyName]
		if fullFamilies[familyName] || errorSetSelectionIsFull(errSet, selected) {
			nameParts = append(nameParts, familyName)
			canonicalTags = append(canonicalTags, errSet.Tags...)
			continue
		}
		for _, qualifiedTag := range errSet.Tags {
			if selected == nil || !selected[qualifiedTag] {
				continue
			}
			nameParts = append(nameParts, qualifiedTag)
			canonicalTags = append(canonicalTags, qualifiedTag)
		}
	}
	return &ErrorSetType{Name: "error[" + strings.Join(nameParts, ", ") + "]", Tags: canonicalTags}
}

func errorSetSelectionIsFull(errSet *ErrorSetType, selected map[string]bool) bool {
	if errSet == nil {
		return false
	}
	if len(errSet.Tags) == 0 {
		return true
	}
	if len(selected) < len(errSet.Tags) {
		return false
	}
	for _, tag := range errSet.Tags {
		if !selected[tag] {
			return false
		}
	}
	return true
}
func (s *ShapeParam) String() string { return s.Name }
func (s *NamedShape) String() string { return s.Name }
func (s *FreshShape) String() string {
	if s.Label != "" {
		return fmt.Sprintf("%s#%d", s.Label, s.ID)
	}
	return fmt.Sprintf("shape#%d", s.ID)
}

func RefStorageName(storage RefStorage) string {
	switch storage {
	case RefStorageHeap:
		return "heap"
	case RefStorageStack:
		return "stack"
	case RefStorageStatic:
		return "static"
	default:
		return "any"
	}
}

func (t *RefType) String() string {
	s := t.Elem.String()
	if t.ExplicitStorage || t.Storage != RefStorageAny {
		s = RefStorageName(t.Storage) + " " + s
	}
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
	neverType   = &NeverType{}
	nullType    = &NullType{}
)

func IsInvalidType(t Type) bool {
	_, ok := t.(*InvalidType)
	return ok
}

func IsNeverType(t Type) bool {
	_, ok := t.(*NeverType)
	return ok
}

func IsNullType(t Type) bool {
	_, ok := t.(*NullType)
	return ok
}

func (t *ErrorSetType) HasTag(name string) bool {
	if t == nil {
		return false
	}
	for _, tag := range t.Tags {
		if tag == name {
			return true
		}
	}
	return false
}

func (t *ErrorSetType) HasQualifiedTag(setName string, tagName string) bool {
	return t.HasTag(QualifyErrorTag(setName, tagName))
}

func (t *ErrorSetType) TagCode(name string) (uint32, bool) {
	if t == nil {
		return 0, false
	}
	for i, tag := range t.Tags {
		if tag == name {
			return uint32(i + 1), true
		}
	}
	return 0, false
}

func (t *ErrorSetType) TagCodeFor(setName string, tagName string) (uint32, bool) {
	return t.TagCode(QualifyErrorTag(setName, tagName))
}

func MatchErrorTag(dst *ErrorSetType, srcTag string) (string, bool) {
	if dst == nil {
		return "", false
	}
	if dst.HasTag(srcTag) {
		return srcTag, true
	}
	shortName := ErrorTagShortName(srcTag)
	match := ""
	for _, candidate := range dst.Tags {
		if ErrorTagShortName(candidate) != shortName {
			continue
		}
		if match != "" {
			return "", false
		}
		match = candidate
	}
	if match == "" {
		return "", false
	}
	return match, true
}

func ErrorSetAssignable(dst, src *ErrorSetType) bool {
	if dst == nil || src == nil {
		return dst == src
	}
	for _, tag := range src.Tags {
		if _, ok := MatchErrorTag(dst, tag); !ok {
			return false
		}
	}
	return true
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

func cloneRefTypeWithState(ref *RefType, state RefState) *RefType {
	if ref == nil {
		return nil
	}
	return &RefType{Elem: ref.Elem, State: state, Storage: ref.Storage, ExplicitStorage: ref.ExplicitStorage}
}

func cloneRefType(ref *RefType) *RefType {
	if ref == nil {
		return nil
	}
	return cloneRefTypeWithState(ref, ref.State)
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

func refStorageAssignable(dstStorage, srcStorage RefStorage, dstExplicit, srcExplicit bool) bool {
	if !dstExplicit || !srcExplicit {
		return true
	}
	return dstStorage == srcStorage
}

func mergeRefStorages(aStorage, bStorage RefStorage, aExplicit, bExplicit bool) (RefStorage, bool, bool) {
	if !aExplicit || !bExplicit {
		return RefStorageAny, false, true
	}
	if aStorage == bStorage {
		return aStorage, true, true
	}
	return RefStorageAny, false, false
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
